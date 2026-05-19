package render

import (
	"image"
	"math"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// atom is one breakable unit on a line.
type atom struct {
	kind       atomKind
	text       string // for word/space; for atomBookmark, the anchor name
	props      docx.RunProps
	imageID    string
	linkRID    string
	linkAnchor string
	fontFamily string
	width      float64
	height     float64
	// anchored signals an image from wp:anchor (floating). When true the
	// renderer respects anchorAlignH for horizontal placement instead of
	// drawing at the inline cursor.
	anchored     bool
	anchorAlignH string // "left", "center", "right", "inside", "outside"
	// anchorWrap mirrors w:wrap on wp:anchor: "" (none/behind/through —
	// no flow effect), "topAndBottom", "square", "tight". For
	// topAndBottom we force a line break before and after the image so
	// body text doesn't overlap. square/tight downgrade to topAndBottom
	// since true shape-exclusion layout is out of scope.
	anchorWrap string
	// anchorOffsetXPt / anchorOffsetYPt mirror w:positionH/positionV w:posOffset
	// in points. When AnchorAlignH is empty, the renderer places the
	// floating image at (marL+OffsetX, currentY+OffsetY). When wrap is
	// "none"/"behind", these are also used to draw the image without
	// affecting flow text.
	anchorOffsetXPt, anchorOffsetYPt float64
	// anchorWrapPolygon carries wp:wrapPolygon vertices in 1/21600 of the
	// shape's bbox. When non-empty and anchorWrap is "tight" / "through",
	// the renderer scales the polygon into the float-wrap band so text
	// follows the contour instead of the bbox.
	anchorWrapPolygon []docx.WrapPathPoint
	// anchorSimplePosUsed / anchorSimplePosXPt / anchorSimplePosYPt mirror
	// wp:anchor@simplePos. When the flag is set the renderer places the
	// shape at the absolute page-relative coordinates instead of the
	// inline cursor — legacy Word 2003 positioning.
	anchorSimplePosUsed                    bool
	anchorSimplePosXPt, anchorSimplePosYPt float64
	// imageRotationDeg / imageFlipH / imageFlipV mirror a:xfrm on the
	// source drawing. Applied at draw time around the image's bounding
	// box.
	imageRotationDeg float64
	imageFlipH       bool
	imageFlipV       bool
	// shape, when non-nil, carries a VML geometric primitive that should
	// be drawn at the current cursor position.
	shape *docx.VMLShape
	// math, when non-zero, carries a pre-laid-out OMML expression box.
	// Drawn at the line's baseline at draw time.
	math mathBox
	// ruby, when non-empty, is the annotation text Word stored in
	// <w:ruby>. The renderer paints it in a smaller font centered above
	// the base atom. The base atom keeps its original width so adjacent
	// flow text doesn't shift.
	ruby string
	// ptab, when non-nil, overrides the tab-stop computation for this
	// atomTab. Alignment / RelativeTo determine the dynamic stop;
	// Leader supplies the fill pattern.
	ptab *docx.PtabInfo
	// dirOverride mirrors Run.DirOverride — "ltr" / "rtl" — set when a
	// w:bdo or w:dir wraps the run.
	dirOverride string
	// ptabPaddingPt, when an atomSpace atom is paired with a ptab,
	// records width padding that we want consumed at end-of-line as
	// trailing space (the gap before a right-aligned w:ptab).
	ptabPaddingPt float64
	// fitScale, when > 0 and != 1, scales the rendered font size of
	// this atom to honor run-level w:fitText squeeze. Width has
	// already been multiplied by the same scale at emit time.
	fitScale float64
	// eaTopText / eaBottomText carry the two halves of an atomEACombine
	// (w:eastAsianLayout w:combine="lines"). Rendered as two half-height
	// rows stacked vertically; the surrounding atom width matches the
	// wider of the two halves so adjacent flow doesn't overlap.
	eaTopText, eaBottomText       string
	eaOpenBracket, eaCloseBracket string
}

// glowOffsets is the unit-vector ring used to stamp glow/outline halos
// around glyphs. Eight directions at 45° produce a recognizable halo
// without driving up PDF size too much. Each offset is multiplied by the
// effect radius/width at call time.
var glowOffsets = [8][2]float64{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{0.707, 0.707}, {-0.707, 0.707},
	{0.707, -0.707}, {-0.707, -0.707},
}

type atomKind int

const (
	atomWord atomKind = iota
	atomSpace
	atomBreak     // soft line break inside paragraph (w:br with no type)
	atomPageBreak // hard page break (w:br w:type="page")
	atomImage
	atomTab
	atomBookmark  // zero-width marker; registers a named PDF anchor at this position
	atomVMLShape  // inline geometric primitive (v:rect/v:line/v:oval/...)
	atomMath      // 2D-laid-out OMML expression
	atomEACombine // w:eastAsianLayout w:combine="lines": two half-height rows stacked
)

// nextTabAfterWithAlign returns the next tab stop strictly past relX
// (measured from line left margin), along with its leader, alignment, and
// a found flag. When no explicit w:tabs apply, falls back to a uniform
// grid — using the doc's w:defaultTabStop when present, else the
// half-inch (720 twips) Word default.
func (r *renderer) nextTabAfterWithAlign(relX float64, p docx.RunProps) (float64, string, string, bool) {
	for _, ts := range r.activeTabs {
		// w:val="bar" is a decorative vertical rule, not a stop the cursor
		// advances to. Skip when looking for the next actual stop.
		if ts.Val == "bar" {
			continue
		}
		if ts.Pos > relX+0.5 {
			return ts.Pos, ts.Leader, ts.Val, true
		}
	}
	grid := 36.0 // 720 twips = 36pt, Word's default
	if r.doc != nil && r.doc.Settings.DefaultTabStopTwips > 0 {
		grid = twipsToPt(r.doc.Settings.DefaultTabStopTwips)
	}
	if grid <= 0 {
		grid = 36.0
	}
	next := (float64(int(relX/grid)) + 1) * grid
	if next > r.contentW {
		return 0, "", "", false
	}
	return next, "", "", true
}

// drawBarTabsForLine paints a vertical rule at every w:tab w:val="bar"
// position. lineLeftX is the paragraph's left start for this line; the
// rule spans the line's full vertical extent.
func (r *renderer) drawBarTabsForLine(lineLeftX, top, height float64) {
	for _, ts := range r.activeTabs {
		if ts.Val != "bar" {
			continue
		}
		x := lineLeftX + ts.Pos
		r.pdf.SetLineWidth(0.5)
		r.pdf.SetStrokeColor(0, 0, 0)
		r.pdf.Line(x, top, x, top+height)
	}
}

// drawTabLeader fills the gap between fromX..toX with the leader pattern.
func drawTabLeader(r *renderer, leader string, fromX, toX, baseline float64, props docx.RunProps, defSize float64) {
	if toX-fromX < 4 {
		return
	}
	// "heavy" is a thick continuous rule, not a glyph repeat. Stroke a
	// single horizontal line at the baseline.
	if leader == "heavy" {
		r.pdf.SetLineWidth(1.5)
		r.pdf.SetStrokeColor(0, 0, 0)
		r.pdf.Line(fromX, baseline, toX, baseline)
		return
	}
	var ch string
	switch leader {
	case "dot":
		ch = "."
	case "middleDot":
		ch = "·"
	case "hyphen":
		ch = "-"
	case "underscore":
		ch = "_"
	default:
		return
	}
	_ = r.applyFontFamily(props, "")
	chW, _ := r.pdf.MeasureTextWidth(ch)
	if chW <= 0 {
		return
	}
	y := baseline - fontAscent(props, defSize)
	for x := fromX; x+chW <= toX; x += chW {
		r.pdf.SetX(x)
		r.pdf.SetY(y)
		_ = r.pdf.Cell(nil, ch)
	}
}

// applyDropCap rewrites a run list so its very first rune becomes its own
// run at an enlarged font size. We don't attempt wrap-around-the-cap layout.
func applyDropCap(runs []docx.Run, lines int) []docx.Run {
	if lines < 2 {
		lines = 3
	}
	for i, run := range runs {
		if run.Text == "" {
			continue
		}
		rs := []rune(run.Text)
		if len(rs) == 0 {
			continue
		}
		first := string(rs[0])
		rest := string(rs[1:])

		capSize := 11.0
		if run.Props.FontSize > 0 {
			capSize = run.Props.FontSize
		}
		capSize *= float64(lines) * 0.9

		capRun := run
		capRun.Text = first
		capRun.Props.FontSize = capSize
		capRun.Props.Bold = true

		restRun := run
		restRun.Text = rest

		out := make([]docx.Run, 0, len(runs)+1)
		out = append(out, runs[:i]...)
		out = append(out, capRun, restRun)
		out = append(out, runs[i+1:]...)
		return out
	}
	return runs
}

// transformText applies w:caps / w:smallCaps. We approximate smallCaps as
// full uppercase — proper small-caps would need per-rune size variation.
func transformText(s string, p docx.RunProps) string {
	if p.Caps || p.SmallCaps {
		return strings.ToUpper(s)
	}
	return s
}

func (r *renderer) runsToAtoms(runs []docx.Run) []atom {
	runs = flattenFields(runs, r.fields)
	runs = r.applyRevisionPolicy(runs)
	var out []atom
	for _, run := range runs {
		if run.Props.Vanish {
			continue
		}
		// Capture the output index before emission so we can decorate
		// any atoms produced for this run with its ruby annotation.
		startIdx := len(out)
		// Complex-script substitution: when the run is tagged w:cs (or
		// rendered inside an RTL paragraph), Word reads bold/italic/size
		// from the complex-script siblings (BCs/ICs/SzCs) rather than the
		// Latin attrs. Doing this here means downstream layout, atom
		// construction, and font selection all see the corrected props
		// without each having to know about the cs/bidi split.
		run.Props = applyComplexScriptProps(run.Props, r.paragraphRTL)
		// w:eastAsianLayout w:combine — render the combined text as a
		// two-row stack inside a single line slot. This is the visual
		// Word uses for "Asian Layout: Combine in characters" so the
		// reading order packs vertically into one line. We split the
		// text in half by rune count and emit one atomEACombine
		// carrying both halves. The surrounding bracket pair (if any)
		// is rendered alongside the stack.
		if run.Props.EALayout != nil && run.Props.EALayout.Combine && run.Text != "" {
			openB, closeB := combineBracketChars(run.Props.EALayout.CombineBrackets)
			runes := []rune(run.Text)
			mid := (len(runes) + 1) / 2
			top := string(runes[:mid])
			bot := string(runes[mid:])
			_ = r.applyRunFont(run.Props)
			halfSize := r.opts.DefaultFontSize
			// Each half is rendered at half height, so measurement
			// uses the half-height font size.
			topW, _ := r.pdf.MeasureTextWidth(top)
			botW, _ := r.pdf.MeasureTextWidth(bot)
			w := topW
			if botW > w {
				w = botW
			}
			w *= 0.5 // half-size font width
			bw := 0.0
			if openB != "" {
				obw, _ := r.pdf.MeasureTextWidth(openB)
				cbw, _ := r.pdf.MeasureTextWidth(closeB)
				bw = obw + cbw
			}
			out = append(out, atom{
				kind:           atomEACombine,
				eaTopText:      top,
				eaBottomText:   bot,
				eaOpenBracket:  openB,
				eaCloseBracket: closeB,
				props:          run.Props,
				width:          w + bw + 1.0,
				height:         halfSize * 1.2,
			})
			continue
		}
		if run.FootnoteID != "" && !r.drawingFootnotes {
			// Skip page-bottom queueing for footnotes when the doc
			// declared w:footnotePr w:pos="docEnd"/"sectEnd" — those get
			// emitted as a single trailer at doc end via
			// appendNotesSection. Endnotes already defer either to
			// section end or doc end, so the gate only fires on the
			// footnote half.
			deferToTrailer := false
			if !run.IsEndnote && r.doc != nil && anySectionDocEndFootnotes(r.doc.Sections) {
				deferToTrailer = true
			}
			if !deferToTrailer {
				r.pendingFootnotes = append(r.pendingFootnotes, pendingNote{
					id: run.FootnoteID, endnote: run.IsEndnote,
				})
			}
			// Rewrite the reference text from "[id]" to the configured
			// label (decimal/roman/letter/symbol).
			labels := r.footnoteLabels
			if run.IsEndnote {
				labels = r.endnoteLabels
			}
			if labels != nil {
				if lbl, ok := labels[run.FootnoteID]; ok && lbl != "" {
					run.Text = lbl
				}
			}
		}
		if run.Bookmark != "" {
			out = append(out, atom{kind: atomBookmark, text: run.Bookmark})
			continue
		}
		if run.VMLShape != nil {
			s := run.VMLShape
			w, h := s.WidthPt, s.HeightPt
			if w <= 0 {
				w = 48
			}
			if h <= 0 {
				h = 24
			}
			if w > r.contentW {
				scale := r.contentW / w
				w *= scale
				h *= scale
			}
			out = append(out, atom{
				kind:                atomVMLShape,
				shape:               s,
				width:               w,
				height:              h,
				props:               run.Props,
				anchored:            run.ImageAnchored,
				anchorAlignH:        run.AnchorAlignH,
				anchorWrap:          run.AnchorWrap,
				anchorOffsetXPt:     run.AnchorOffsetXPt,
				anchorOffsetYPt:     run.AnchorOffsetYPt,
				anchorWrapPolygon:   run.AnchorWrapPolygon,
				anchorSimplePosUsed: run.AnchorSimplePosUsed,
				anchorSimplePosXPt:  run.AnchorSimplePosXPt,
				anchorSimplePosYPt:  run.AnchorSimplePosYPt,
				linkRID:             run.LinkURL,
				linkAnchor:          run.LinkAnchor,
			})
			continue
		}
		if run.ImageID != "" {
			img, ok := r.doc.Images[run.ImageID]
			if !ok {
				// EMF vector replay: when emfToVMLShape produced a
				// shape, render that instead of the placeholder label.
				if r.doc.EMFShapes != nil {
					if shape := r.doc.EMFShapes[run.ImageID]; shape != nil {
						w := run.ImageWidthPt
						h := run.ImageHeightPt
						if w <= 0 {
							w = shape.WidthPt
						}
						if h <= 0 {
							h = shape.HeightPt
						}
						scaled := *shape
						scaled.WidthPt = w
						scaled.HeightPt = h
						out = append(out, atom{
							kind:   atomVMLShape,
							width:  w,
							height: h,
							shape:  &scaled,
							props:  run.Props,
						})
						continue
					}
				}
				// Unsupported media (EMF/WMF/etc.) or unresolved rId. If
				// the docx declared a w:extent (ImageWidthPt/HeightPt),
				// emit a sized outline box so the layout reflects the
				// real estate the original image occupied. Falls back
				// to a text placeholder when no extent is known.
				label, hasLabel := r.doc.UnsupportedMedia[run.ImageID]
				if !hasLabel {
					label = "image"
				}
				if run.ImageWidthPt > 0 && run.ImageHeightPt > 0 {
					w := run.ImageWidthPt
					h := run.ImageHeightPt
					if w > r.contentW {
						scale := r.contentW / w
						w *= scale
						h *= scale
					}
					shape := &docx.VMLShape{
						Kind:           "rect",
						WidthPt:        w,
						HeightPt:       h,
						StrokeColor:    "888888",
						StrokeWeightPt: 0.5,
						TextBox:        "[" + label + "]",
					}
					out = append(out, atom{
						kind:   atomVMLShape,
						width:  w,
						height: h,
						shape:  shape,
						props:  run.Props,
					})
					continue
				}
				if run.AltText != "" {
					stub := run
					stub.ImageID = ""
					stub.AltText = ""
					stub.Text = "[" + run.AltText + "]"
					out = append(out, r.runsToAtoms([]docx.Run{stub})...)
					continue
				}
				if hasLabel {
					placeholder := "[" + label + " image]"
					stub := run
					stub.ImageID = ""
					stub.Text = placeholder
					subOut := r.runsToAtoms([]docx.Run{stub})
					out = append(out, subOut...)
				}
				continue
			}
			cropped := run.CropTopPct > 0 || run.CropBottomPct > 0 || run.CropLeftPct > 0 || run.CropRightPct > 0
			hasEffects := len(run.ImageEffects) > 0
			imgID := run.ImageID
			if cropped || hasEffects {
				if cropped {
					img = cropImage(img, run.CropTopPct, run.CropBottomPct, run.CropLeftPct, run.CropRightPct)
				}
				if hasEffects {
					img = applyImageEffects(img, run.ImageEffects)
				}
				if r.croppedCache == nil {
					r.croppedCache = map[string]image.Image{}
				}
				imgID = run.ImageID + ":fx"
				r.croppedCache[imgID] = img
			}
			var w, h float64
			// wp14:pctWidth / pctHeight (thousandths of a percent) trump
			// an explicit wp:extent. When only one of the two percentages
			// is specified, the other dimension is scaled to keep the
			// image's natural aspect ratio.
			pctW := r.resolvePctSize(run.ImagePctWidth, run.ImagePctRelFromH, true)
			pctH := r.resolvePctSize(run.ImagePctHeight, run.ImagePctRelFromV, false)
			switch {
			case pctW > 0 && pctH > 0:
				w, h = pctW, pctH
			case pctW > 0:
				w = pctW
				natW, natH := r.fitImage(img)
				if natW > 0 {
					h = pctW * natH / natW
				} else {
					h = pctW
				}
			case pctH > 0:
				h = pctH
				natW, natH := r.fitImage(img)
				if natH > 0 {
					w = pctH * natW / natH
				} else {
					w = pctH
				}
			case run.ImageWidthPt > 0 && run.ImageHeightPt > 0:
				w, h = run.ImageWidthPt, run.ImageHeightPt
				if w > r.contentW {
					scale := r.contentW / w
					w *= scale
					h *= scale
				}
			default:
				w, h = r.fitImage(img)
			}
			out = append(out, atom{
				kind:                atomImage,
				imageID:             imgID,
				width:               w,
				height:              h,
				props:               run.Props,
				linkRID:             run.LinkURL,
				linkAnchor:          run.LinkAnchor,
				anchored:            run.ImageAnchored,
				anchorAlignH:        run.AnchorAlignH,
				anchorWrap:          run.AnchorWrap,
				anchorOffsetXPt:     run.AnchorOffsetXPt,
				anchorOffsetYPt:     run.AnchorOffsetYPt,
				anchorWrapPolygon:   run.AnchorWrapPolygon,
				anchorSimplePosUsed: run.AnchorSimplePosUsed,
				anchorSimplePosXPt:  run.AnchorSimplePosXPt,
				anchorSimplePosYPt:  run.AnchorSimplePosYPt,
				imageRotationDeg:    run.ImageRotationDeg,
				imageFlipH:          run.ImageFlipH,
				imageFlipV:          run.ImageFlipV,
			})
			continue
		}
		if run.IsBreak {
			if run.Text == "\f" {
				out = append(out, atom{kind: atomPageBreak, props: run.Props})
			} else {
				out = append(out, atom{kind: atomBreak, props: run.Props})
			}
			continue
		}
		if run.Math != nil {
			fs := run.Props.FontSize
			if fs == 0 {
				fs = r.opts.DefaultFontSize
			}
			_ = r.applyRunFont(run.Props)
			box := r.buildMathBox(run.Math, fs)
			if box.w > 0 {
				out = append(out, atom{
					kind:   atomMath,
					props:  run.Props,
					width:  box.w,
					height: box.height(),
					math:   box,
				})
				continue
			}
		}
		if run.Text == "" {
			continue
		}

		// Walk by rune. Group non-CJK runes that share a font family into one
		// word atom; emit each CJK rune as its own atom so the greedy line-
		// breaker can wrap mid-sentence (CJK has no inter-word spaces).
		var (
			buf       strings.Builder
			bufFamily string
		)
		flushBuf := func() {
			if buf.Len() == 0 {
				return
			}
			_ = r.applyFontFamily(run.Props, bufFamily)
			text := buf.String()
			// In an RTL paragraph, an all-RTL word atom is laid out by
			// Bidi reordering: when ANY rune is RTL or the paragraph
			// is RTL we run a UAX#9 paragraph-level resolver on this
			// segment so embedded LTR digits/words in an Arabic paragraph
			// (and embedded RTL words in an LTR paragraph) come out in
			// the correct visual order. Pure-LTR with no RTL chars is a
			// fast no-op.
			//
			// After the bidi pass we apply Arabic letter shaping so the
			// glyphs use their Initial/Medial/Final/Isolated forms — the
			// difference between unshaped "ا ل ع ر ب ي ة" and shaped
			// "العربية".
			hasRTLChar := false
			for _, ru := range text {
				if isRTL(ru) {
					hasRTLChar = true
					break
				}
			}
			if r.paragraphRTL || hasRTLChar {
				text = reorderBidi(text, r.paragraphRTL)
				text = shapeArabic(text)
			}
			w, _ := r.pdf.MeasureTextWidth(text)
			out = append(out, atom{
				kind:       atomWord,
				text:       text,
				props:      run.Props,
				fontFamily: bufFamily,
				width:      w,
				linkRID:    run.LinkURL,
				linkAnchor: run.LinkAnchor,
			})
			buf.Reset()
			bufFamily = ""
		}
		text := transformText(run.Text, run.Props)
		for _, rn := range text {
			switch {
			case rn == '\n':
				flushBuf()
				out = append(out, atom{kind: atomBreak, props: run.Props})
			case rn == '\t':
				flushBuf()
				_ = r.applyFontFamily(run.Props, r.selectFont(run.Props))
				w, _ := r.pdf.MeasureTextWidth("    ")
				out = append(out, atom{
					kind:        atomTab,
					props:       run.Props,
					width:       w,
					ptab:        run.Ptab,
					dirOverride: run.DirOverride,
				})
			case rn == ' ':
				flushBuf()
				_ = r.applyFontFamily(run.Props, r.selectFont(run.Props))
				w, _ := r.pdf.MeasureTextWidth(" ")
				out = append(out, atom{kind: atomSpace, text: " ", props: run.Props, width: w})
			case isCJK(rn) || isSymbolGlyph(rn) || isPUA(rn):
				// CJK, symbol-block runes, and Private-Use-Area glyphs
				// share a code path: each becomes its own atom. CJK
				// because we need per-character line breaks (no inter-
				// word spaces); symbols because their natural font may
				// differ from the surrounding Latin (e.g. ✓ routes to
				// fallback while ASCII stays on the regular face); PUA
				// because vendor/EUDC fonts are different from the run's
				// nominal face and bundling them with surrounding Latin
				// would render them as missing-glyph boxes.
				flushBuf()
				fam := r.chooseFamily(rn, run.Props)
				_ = r.applyFontFamily(run.Props, fam)
				s := string(rn)
				w, _ := r.pdf.MeasureTextWidth(s)
				// docGrid w:charSpace adds extra space between CJK
				// characters when the section enables the
				// linesAndChars grid. Apply only to CJK ranges so
				// symbols/Latin in a CJK doc don't get over-padded.
				if isCJK(rn) {
					w += r.activeCharSpacePt()
				}
				out = append(out, atom{
					kind:       atomWord,
					text:       s,
					props:      run.Props,
					fontFamily: fam,
					width:      w,
					linkRID:    run.LinkURL,
					linkAnchor: run.LinkAnchor,
				})
			default:
				fam := r.chooseFamily(rn, run.Props)
				if buf.Len() > 0 && fam != bufFamily {
					flushBuf()
				}
				if buf.Len() == 0 {
					bufFamily = fam
				}
				buf.WriteRune(rn)
			}
		}
		flushBuf()
		// Decorate the freshly-emitted atoms for this run with its
		// w:ruby annotation. We attach the ruby text to every atom
		// the run produced so multi-character bases get the same
		// label drawn above each char; Word's typical use is single
		// CJK char + Latin transliteration.
		if run.Ruby != nil && run.Ruby.Text != "" {
			for i := startIdx; i < len(out); i++ {
				if out[i].kind == atomWord {
					out[i].ruby = run.Ruby.Text
				}
			}
		}
		// w:fitText horizontal squeeze (run-level). When the run
		// declares a target width, scale every atom we just emitted
		// so the run's natural width hits exactly FitTextWidthPt.
		// gopdf has no Tz (text horizontal scale) operator, so we
		// fold the scale into the atom width and the per-atom font
		// size — readable, just isotropic instead of true squash.
		if run.Props.FitTextWidthPt > 0 {
			var natural float64
			for i := startIdx; i < len(out); i++ {
				natural += out[i].width
			}
			if natural > 0 {
				scale := run.Props.FitTextWidthPt / natural
				if scale > 0 && scale != 1 {
					for i := startIdx; i < len(out); i++ {
						out[i].width *= scale
						out[i].fitScale = scale
					}
				}
			}
		}
		// Stamp dirOverride onto the run's atoms so reorderBidi at
		// draw time treats them as if they live in a directional
		// override (w:bdo / w:dir).
		if run.DirOverride != "" {
			for i := startIdx; i < len(out); i++ {
				out[i].dirOverride = run.DirOverride
			}
		}
	}
	return out
}

func (r *renderer) resolveURL(rid string) string {
	if rid == "" {
		return ""
	}
	if v, ok := r.doc.Hyperlink[rid]; ok {
		return v
	}
	// HYPERLINK field encodes the URL directly (no rels entry).
	if strings.HasPrefix(rid, "http://") || strings.HasPrefix(rid, "https://") ||
		strings.HasPrefix(rid, "mailto:") || strings.HasPrefix(rid, "ftp://") {
		return rid
	}
	return ""
}

func (r *renderer) layoutLine(atoms []atom, align docx.Alignment) error {
	var line []atom
	var lineW float64
	var lineMaxH float64

	// Hanging indent: the first physical line gets `hang` extra width and
	// starts `hang` to the left. Captured once here so it can't change
	// mid-paragraph; consumed and zeroed on the first flush.
	hang := r.firstLineHangPt

	flush := func(isLast bool) error {
		if len(line) == 0 {
			r.cursorY += r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
			// An empty first line still "uses up" the hanging — clear so the
			// next non-empty line wraps at the normal margin.
			hang = 0
			return nil
		}
		// RTL paragraphs draw their atoms in reverse visual order: the
		// logically-first atom appears at the right edge. Width totals and
		// per-atom metadata are unchanged — only the iteration order flips.
		if r.paragraphRTL {
			for i, j := 0, len(line)-1; i < j; i, j = i+1, j-1 {
				line[i], line[j] = line[j], line[i]
			}
		}
		if lineMaxH == 0 {
			lineMaxH = r.opts.DefaultFontSize * 1.2
		}
		lineMaxH = r.applyLineHeight(lineMaxH)
		r.ensureRoom(lineMaxH)

		// Effective geometry for this specific line: first physical line gets
		// the hanging outdent; later lines use the paragraph's normal margin.
		x := r.marL - hang
		effW := r.contentW + hang
		// w:wrap="square" / "tight" with a side anchor: if a floating
		// image is still active vertically, shift this line's left edge
		// past the image (when image is on the left) or shrink the
		// right edge (when image is on the right). Expired bands clear
		// automatically the first time the cursor drops below them.
		if x0, w0, ok := r.lineBandAdjust(r.cursorY, x, effW); ok {
			x, effW = x0, w0
		}
		extraSpace := 0.0
		switch align {
		case docx.AlignCenter:
			x = r.marL + (r.contentW-lineW)/2
		case docx.AlignRight:
			x = r.marL + r.contentW - lineW
		case docx.AlignJustify:
			if !isLast {
				spaces := 0
				for _, a := range line {
					if a.kind == atomSpace {
						spaces++
					}
				}
				if spaces > 0 && effW > lineW {
					extraSpace = (effW - lineW) / float64(spaces)
				}
			}
		}
		// One-shot: subsequent flushes use the normal margin.
		hang = 0

		baseline := r.cursorY + lineMaxH*0.8

		// Bar tab stops paint a vertical rule at their absolute X across
		// every line of the paragraph. They are decorative (the cursor
		// doesn't advance to them — nextTabAfterWithAlign skips bars).
		r.drawBarTabsForLine(x, r.cursorY, lineMaxH)

		if r.pendingMarker != nil {
			pm := r.pendingMarker
			if pm.image != nil {
				em := r.opts.DefaultFontSize
				_ = r.drawImage(pm.image, pm.x, baseline-em, em, em)
			} else {
				if pm.fontFamily != "" && r.fonts[pm.fontFamily] {
					_ = r.applyFontFamily(pm.props, pm.fontFamily)
				} else {
					_ = r.applyRunFont(pm.props)
				}
				drawX := pm.x
				// w:lvlJc — when the marker should be right- or center-
				// aligned within its hanging column, measure the rendered
				// text and shift drawX so that the right edge sits at
				// (pm.x + pm.colWidth).
				if pm.colWidth > 0 && (pm.jc == "right" || pm.jc == "end" || pm.jc == "center") {
					w, _ := r.pdf.MeasureTextWidth(pm.text)
					switch pm.jc {
					case "right", "end":
						drawX = pm.x + pm.colWidth - w
					case "center":
						drawX = pm.x + (pm.colWidth-w)/2
					}
					if drawX < pm.x {
						drawX = pm.x
					}
				}
				r.pdf.SetX(drawX)
				r.pdf.SetY(baseline - fontAscent(pm.props, r.opts.DefaultFontSize))
				if err := r.pdf.Cell(nil, pm.text); err != nil {
					return err
				}
			}
			r.pendingMarker = nil
		}

		cx := x
		for i, a := range line {
			switch a.kind {
			case atomWord:
				// Run-level w:fitText: temporarily fold the per-atom
				// fitScale into the renderer's existing fitTextScale
				// path so applyFontFamily picks up the squeezed size.
				// Restored below at end of this case branch.
				prevFit := r.fitTextScale
				if a.fitScale > 0 && a.fitScale != 1 {
					if prevFit > 0 {
						r.fitTextScale = prevFit * a.fitScale
					} else {
						r.fitTextScale = a.fitScale
					}
				}
				if err := r.applyFontFamily(a.props, a.fontFamily); err != nil {
					r.fitTextScale = prevFit
					return err
				}
				ascent := fontAscent(a.props, r.opts.DefaultFontSize)
				topY := baseline - ascent
				switch a.props.VertAlign {
				case "superscript":
					topY -= ascent * 0.4
				case "subscript":
					topY += ascent * 0.25
				}
				if a.props.PositionPt != 0 {
					topY -= a.props.PositionPt
				}
				if br, bg, bb, ok := runBackgroundRGB(a.props); ok {
					r.pdf.SetFillColor(br, bg, bb)
					r.pdf.Rectangle(cx, topY, cx+a.width, baseline+1, "F", 0, 0)
				}
				r.pdf.SetX(cx)
				r.pdf.SetY(topY)
				// w14:scene3d / w14:props3d — draw a stack of three offset
				// "depth" copies behind the glyph in progressively darker
				// shades so flat-PDF can't render real 3D extrusion but
				// the text still reads as raised. We respect any explicit
				// w:color when computing the depth shade so 3D-on-blue
				// text doesn't go gray.
				if a.props.W14Has3D && a.text != "" {
					baseR, baseG, baseB := uint8(64), uint8(64), uint8(64)
					if a.props.Color != "" {
						baseR, baseG, baseB = parseHexColor(a.props.Color)
					}
					for d := 3; d >= 1; d-- {
						df := float64(d)
						// Mix toward white as depth increases: lighter
						// layers sit further "back" and read as recession.
						mix := func(c uint8) uint8 {
							t := 1.0 - df*0.25
							v := float64(c)*t + 255.0*(1.0-t)
							if v > 255 {
								v = 255
							}
							if v < 0 {
								v = 0
							}
							return uint8(v)
						}
						r.pdf.SetTextColor(mix(baseR), mix(baseG), mix(baseB))
						r.pdf.SetX(cx + df*0.5)
						r.pdf.SetY(topY + df*0.5)
						_ = r.pdf.Cell(nil, a.text)
					}
					r.pdf.SetTextColor(baseR, baseG, baseB)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				}
				switch a.props.TextEffect {
				case "emboss":
					rOff, gOff, bOff := uint8(220), uint8(220), uint8(220)
					savedR, savedG, savedB := uint8(0), uint8(0), uint8(0)
					if a.props.Color != "" {
						savedR, savedG, savedB = parseHexColor(a.props.Color)
					}
					r.pdf.SetTextColor(rOff, gOff, bOff)
					r.pdf.SetX(cx + 0.5)
					r.pdf.SetY(topY + 0.5)
					_ = r.pdf.Cell(nil, a.text)
					r.pdf.SetTextColor(savedR, savedG, savedB)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				case "imprint":
					r.pdf.SetTextColor(140, 140, 140)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY + 0.5)
					_ = r.pdf.Cell(nil, a.text)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				case "outline":
					r.pdf.SetTextColor(160, 160, 160)
				}
				// w14:glow — stamp the glyph 8 times around the target in
				// the glow color before drawing the real text so it looks
				// haloed. Radius is the offset distance in points.
				if a.props.W14GlowColor != "" && a.text != "" {
					rad := a.props.W14GlowRadiusPt
					if rad <= 0 {
						rad = 2
					}
					if rad > 6 {
						rad = 6
					}
					gr, gg, gb := parseHexColor(a.props.W14GlowColor)
					savedR, savedG, savedB := uint8(0), uint8(0), uint8(0)
					if a.props.Color != "" {
						savedR, savedG, savedB = parseHexColor(a.props.Color)
					}
					r.pdf.SetTextColor(gr, gg, gb)
					for _, off := range glowOffsets {
						r.pdf.SetX(cx + off[0]*rad)
						r.pdf.SetY(topY + off[1]*rad)
						_ = r.pdf.Cell(nil, a.text)
					}
					r.pdf.SetTextColor(savedR, savedG, savedB)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				}
				// w14:textOutline — stamp the glyph at 8 small offsets in
				// the outline color, then draw the real text on top. This
				// fakes a stroke without needing PDF text-rendering-mode
				// access. Width drives the offset distance.
				if a.props.W14OutlineColor != "" && a.props.W14OutlineWidthPt > 0 && a.text != "" {
					w := a.props.W14OutlineWidthPt
					if w > 1.5 {
						w = 1.5
					}
					or, og, ob := parseHexColor(a.props.W14OutlineColor)
					savedR, savedG, savedB := uint8(0), uint8(0), uint8(0)
					if a.props.Color != "" {
						savedR, savedG, savedB = parseHexColor(a.props.Color)
					}
					r.pdf.SetTextColor(or, og, ob)
					for _, off := range glowOffsets {
						r.pdf.SetX(cx + off[0]*w)
						r.pdf.SetY(topY + off[1]*w)
						_ = r.pdf.Cell(nil, a.text)
					}
					r.pdf.SetTextColor(savedR, savedG, savedB)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				}
				// w14:textFill — override the run fill color before the
				// real glyph stamp. Doesn't disturb the prior outline pass
				// because that already set its own color.
				if a.props.W14TextFillColor != "" {
					fr, fg, fb := parseHexColor(a.props.W14TextFillColor)
					r.pdf.SetTextColor(fr, fg, fb)
				}
				if err := r.pdf.Cell(nil, a.text); err != nil {
					return err
				}
				// w14:reflection — draw a flipped copy beneath the text
				// in a lighter shade, simulating the mirror Word renders.
				// We can't scale vertically in gopdf without state, so we
				// just stamp at baseline+ascent in a gray tone.
				if a.props.W14Reflection && a.text != "" {
					savedR, savedG, savedB := uint8(0), uint8(0), uint8(0)
					if a.props.Color != "" {
						savedR, savedG, savedB = parseHexColor(a.props.Color)
					}
					r.pdf.SetTextColor(200, 200, 200)
					r.pdf.SetX(cx)
					r.pdf.SetY(baseline + ascent*0.05)
					_ = r.pdf.Cell(nil, a.text)
					r.pdf.SetTextColor(savedR, savedG, savedB)
					r.pdf.SetX(cx)
					r.pdf.SetY(topY)
				}
				// w:ruby — paint the interlinear annotation in a smaller
				// font centered above the base char. We approximate the
				// raise as 1/2 ascent and the ruby size as 0.5×base.
				if a.ruby != "" {
					rubyFs := r.opts.DefaultFontSize * 0.5
					savedFs := a.props.FontSize
					props := a.props
					props.FontSize = rubyFs
					_ = r.applyFontFamily(props, a.fontFamily)
					rw, _ := r.pdf.MeasureTextWidth(a.ruby)
					rubyX := cx + (a.width-rw)/2
					if rubyX < cx {
						rubyX = cx
					}
					r.pdf.SetX(rubyX)
					r.pdf.SetY(topY - rubyFs*0.95)
					_ = r.pdf.Cell(nil, a.ruby)
					props.FontSize = savedFs
					_ = r.applyFontFamily(a.props, a.fontFamily)
				}
				// Faux bold: when the run wants bold but no bold face was
				// registered, re-draw the same glyph stream at a small
				// horizontal offset so the strokes look thicker. This is
				// the same trick browsers use for fonts that don't ship a
				// bold variant — readable, not pretty. A real bold TTF
				// (Options.FontBold) is always better when available.
				if a.props.Bold && !r.fonts[boldFamily] && a.text != "" {
					r.pdf.SetX(cx + 0.3)
					r.pdf.SetY(topY)
					_ = r.pdf.Cell(nil, a.text)
				}
				if a.props.Underline || a.props.Strike || a.props.DStrike {
					r.pdf.SetStrokeColor(0, 0, 0)
					if a.props.Color != "" {
						rr, gg, bb := parseHexColor(a.props.Color)
						r.pdf.SetStrokeColor(rr, gg, bb)
					}
					if a.props.Underline {
						drawUnderline(r, a, cx, baseline)
					}
					if a.props.Strike {
						r.pdf.SetLineWidth(0.5)
						strikeY := baseline - fontAscent(a.props, r.opts.DefaultFontSize)*0.35
						r.pdf.Line(cx, strikeY, cx+a.width, strikeY)
					}
					if a.props.DStrike {
						r.pdf.SetLineWidth(0.5)
						mid := baseline - fontAscent(a.props, r.opts.DefaultFontSize)*0.35
						r.pdf.Line(cx, mid-0.7, cx+a.width, mid-0.7)
						r.pdf.Line(cx, mid+0.7, cx+a.width, mid+0.7)
					}
				}
				if url := r.resolveURL(a.linkRID); url != "" {
					h := fontAscent(a.props, r.opts.DefaultFontSize) * 1.1
					r.pdf.AddExternalLink(url, cx, topY, a.width, h)
				} else if a.linkAnchor != "" {
					h := fontAscent(a.props, r.opts.DefaultFontSize) * 1.1
					r.pdf.AddInternalLink(a.linkAnchor, cx, topY, a.width, h)
				}
				if a.props.Em != "" && a.text != "" {
					drawEmphasisMark(r, a, cx, baseline)
				}
				// w:bdr (run-level box border around the atom) and form-
				// field control affordances. Draw last so they sit over the
				// fill/strike layers but under the next atom.
				if a.props.TextBorder.Has() && a.text != "" {
					drawRunTextBorder(r, a.props.TextBorder, cx, topY, a.width, ascent*1.15)
				}
				cx += a.width
				// Restore the renderer-wide fitTextScale we possibly
				// overrode at the top of this case for run-level
				// w:fitText. No-op when prevFit was already current.
				r.fitTextScale = prevFit
			case atomSpace:
				cx += a.width + extraSpace
			case atomTab:
				var stopX float64
				var leader, tabAlign string
				var ok bool
				if a.ptab != nil {
					// Positional tab: compute the stop from the
					// alignment + relativeTo anchor instead of the
					// paragraph's w:tabs list. Page-relative anchors
					// use the section width; the simpler approach (and
					// what Word does on a single-column body) is to
					// anchor to the line's right edge / center.
					rightEdge := r.contentW
					if a.ptab.RelativeTo == "page" {
						// page-relative: include the left margin so the
						// stop sits at the actual page right edge.
						rightEdge = r.contentW + r.marL + r.marR
					}
					switch a.ptab.Alignment {
					case "right":
						stopX = rightEdge
					case "center":
						stopX = rightEdge / 2
					default:
						stopX = cx - x + a.width
					}
					leader = a.ptab.Leader
					if leader == "none" {
						leader = ""
					}
					tabAlign = a.ptab.Alignment
					ok = true
				} else {
					stopX, leader, tabAlign, ok = r.nextTabAfterWithAlign(cx-x, a.props)
				}
				if !ok {
					cx += a.width
					break
				}
				absStop := stopX + x

				switch tabAlign {
				case "right", "decimal":
					totalW := 0.0
					for j := i + 1; j < len(line); j++ {
						if line[j].kind == atomTab || line[j].kind == atomBreak {
							break
						}
						totalW += line[j].width
					}
					start := absStop - totalW
					if start < cx {
						start = cx
					}
					if leader != "" {
						drawTabLeader(r, leader, cx, start, baseline, a.props, r.opts.DefaultFontSize)
					}
					cx = start
				default:
					if leader != "" {
						drawTabLeader(r, leader, cx, absStop, baseline, a.props, r.opts.DefaultFontSize)
					}
					cx = absStop
				}
			case atomBookmark:
				r.pdf.SetX(cx)
				r.pdf.SetY(baseline - r.opts.DefaultFontSize*0.8)
				r.pdf.SetAnchor(a.text)
				if r.fields.bookmarkPages != nil {
					r.fields.bookmarkPages[a.text] = r.pdf.GetNumberOfPages()
				}
			case atomImage:
				var img image.Image
				if strings.Contains(a.imageID, ":crop") {
					img = r.croppedCache[a.imageID]
				} else {
					img = r.doc.Images[a.imageID]
				}
				if img == nil {
					continue
				}
				// For anchored (wp:anchor) images, honor positionH alignment.
				// We can't implement full text-wrap layout, but we can at
				// least shift the image horizontally so it lands roughly
				// where the source asked. Vertical alignment is intentionally
				// not adjusted — we still draw at the current cursor y so
				// the surrounding flow text isn't pushed.
				imgX := cx
				if a.anchored {
					switch a.anchorAlignH {
					case "right", "outside":
						imgX = r.marL + r.contentW - a.width
					case "center":
						imgX = r.marL + (r.contentW-a.width)/2
					case "left", "inside":
						imgX = r.marL
					}
				}
				if err := r.drawTransformedImage(img, imgX, r.cursorY, a.width, a.height, a.imageRotationDeg, a.imageFlipH, a.imageFlipV); err != nil {
					return err
				}
				// Image hyperlink: a:hlinkClick on wp:docPr / pic:cNvPr.
				// LinkURL holds the relationship rId (resolved here); LinkAnchor
				// is an in-document bookmark name. Emit a PDF link annotation
				// covering the drawn image bounds. Also applies to anchored
				// images — they get their click target where they land.
				if url := r.resolveURL(a.linkRID); url != "" {
					r.pdf.AddExternalLink(url, imgX, r.cursorY, a.width, a.height)
				} else if a.linkAnchor != "" {
					r.pdf.AddInternalLink(a.linkAnchor, imgX, r.cursorY, a.width, a.height)
				}
				if a.anchored {
					// Anchored image — don't advance the inline cursor.
				} else {
					cx += a.width
				}
			case atomVMLShape:
				if a.shape != nil {
					drawVMLShape(r, a.shape, cx, r.cursorY, a.width, a.height)
				}
				// Shape hyperlink: a:hlinkClick / v:shape@href / o:OLEObject
				// @href all converge onto the run's LinkURL or LinkAnchor.
				if url := r.resolveURL(a.linkRID); url != "" {
					r.pdf.AddExternalLink(url, cx, r.cursorY, a.width, a.height)
				} else if a.linkAnchor != "" {
					r.pdf.AddInternalLink(a.linkAnchor, cx, r.cursorY, a.width, a.height)
				}
				cx += a.width
			case atomMath:
				if a.math.draw != nil {
					a.math.draw(r, cx, baseline)
				}
				cx += a.width
			case atomEACombine:
				// Two-row stack: top half above midline, bottom below.
				// Both rendered at half the surrounding font size so
				// the combined glyph occupies one logical character
				// width inside one line of flow text.
				_ = r.applyRunFont(a.props)
				halfSize := r.opts.DefaultFontSize * 0.5
				orig := r.opts.DefaultFontSize
				savedProps := a.props
				smallProps := savedProps
				smallProps.FontSize = halfSize
				_ = r.applyRunFont(smallProps)
				bracketX := cx
				if a.eaOpenBracket != "" {
					r.pdf.SetX(bracketX)
					r.pdf.SetY(baseline - fontAscent(savedProps, orig))
					_ = r.pdf.Cell(nil, a.eaOpenBracket)
					obw, _ := r.pdf.MeasureTextWidth(a.eaOpenBracket)
					bracketX += obw
				}
				topW, _ := r.pdf.MeasureTextWidth(a.eaTopText)
				botW, _ := r.pdf.MeasureTextWidth(a.eaBottomText)
				stackW := topW
				if botW > stackW {
					stackW = botW
				}
				// Center each half horizontally inside the stack column.
				r.pdf.SetX(bracketX + (stackW-topW)/2)
				r.pdf.SetY(baseline - lineMaxH*0.9)
				_ = r.pdf.Cell(nil, a.eaTopText)
				r.pdf.SetX(bracketX + (stackW-botW)/2)
				r.pdf.SetY(baseline - lineMaxH*0.45)
				_ = r.pdf.Cell(nil, a.eaBottomText)
				bracketX += stackW
				if a.eaCloseBracket != "" {
					r.pdf.SetX(bracketX)
					r.pdf.SetY(baseline - fontAscent(savedProps, orig))
					_ = r.applyRunFont(savedProps)
					_ = r.pdf.Cell(nil, a.eaCloseBracket)
				}
				cx += a.width
			}
		}

		r.cursorY += lineMaxH
		line = line[:0]
		lineW = 0
		lineMaxH = 0
		return nil
	}

	for _, a := range atoms {
		if a.kind == atomBreak {
			if err := flush(true); err != nil {
				return err
			}
			continue
		}
		if a.kind == atomPageBreak {
			if err := flush(true); err != nil {
				return err
			}
			r.drawFootnotesAtBottom()
			r.newPage()
			continue
		}
		// Anchored image/shape with text-flow wrap. Three modes:
		//   * none / behind / through: image is drawn at its anchored
		//     position but does NOT participate in flow — surrounding
		//     text continues at the same baseline as if the image weren't
		//     there.
		//   * square / tight + anchor on left/right: draw the image to
		//     the side immediately, then let surrounding text continue
		//     to flow beside it on the opposite side until the cursor
		//     drops past the image's bottom (real wrap behavior).
		//   * topAndBottom OR square/tight without a side anchor: take
		//     the full line height for the image so following text
		//     stacks below it (the legacy behavior, still right for
		//     centered or "auto" positioned drawings).
		if (a.kind == atomImage || a.kind == atomVMLShape) && a.anchored &&
			(a.anchorWrap == "" || a.anchorWrap == "none" || a.anchorWrap == "behind" || a.anchorWrap == "through") {
			r.drawFloatingShapeBehind(&a)
			continue
		}
		if (a.kind == atomImage || a.kind == atomVMLShape) && a.anchored &&
			(a.anchorWrap == "square" || a.anchorWrap == "tight") &&
			(a.anchorAlignH == "left" || a.anchorAlignH == "right" ||
				a.anchorAlignH == "inside" || a.anchorAlignH == "outside") {
			r.drawFloatingShapeWithWrap(&a)
			continue
		}
		if (a.kind == atomImage || a.kind == atomVMLShape) && a.anchored &&
			(a.anchorWrap == "topAndBottom" || a.anchorWrap == "square" || a.anchorWrap == "tight") {
			if len(line) > 0 {
				if err := flush(false); err != nil {
					return err
				}
			}
			// Force the image's full height to be reserved.
			lineMaxH = a.height
			line = append(line, a)
			lineW = a.width
			if err := flush(false); err != nil {
				return err
			}
			continue
		}
		h := atomHeight(a, r.opts.DefaultFontSize)
		// First line gets hang extra width; subsequent lines use r.contentW.
		// hang is zeroed inside flush() so this naturally tightens after the
		// first wrap.
		effW := r.contentW + hang
		// When a floating image's wrap band is still active vertically,
		// the actual available width is reduced — must agree with the
		// adjustment flush() applies so wrap decisions and the painted
		// line use the same metric.
		if _, bw, ok := r.lineBandAdjust(r.cursorY, r.marL-hang, effW); ok {
			effW = bw
		}
		// Over-wide word: a single word atom wider than the line's
		// effective width can't be wrapped by the normal "atom-vs-atom"
		// break logic. Try giving it a fresh line first — if it still
		// doesn't fit (truly over-wide, e.g. "submission_timestamp" in a
		// narrow column), fall back to splitting it per rune. Without
		// this fresh-line attempt, an atom like "Name" that is just
		// barely too wide for the remaining space on the current line
		// would split mid-word ("Nam\ne") even though it fits cleanly
		// when placed on the next line.
		if a.kind == atomWord && effW > 0 && a.width > effW && a.text != "" {
			if len(line) > 0 {
				if line[len(line)-1].kind == atomSpace {
					lineW -= line[len(line)-1].width
					line = line[:len(line)-1]
				}
				if err := flush(false); err != nil {
					return err
				}
			}
			if a.width > effW {
				// Try Liang hyphenation before per-rune fallback. If a
				// legal soft-break fits effW we get a clean two-piece
				// split with a trailing hyphen at the line break.
				if r.doc != nil && (r.doc.Settings.AutoHyphenation || r.opts.AutoHyphenation) {
					if pair := r.splitWordAtomByHyphenation(a, effW); pair != nil {
						for _, sub := range pair {
							if lineW+sub.width > effW && len(line) > 0 {
								if line[len(line)-1].kind == atomSpace {
									lineW -= line[len(line)-1].width
									line = line[:len(line)-1]
								}
								if err := flush(false); err != nil {
									return err
								}
							}
							line = append(line, sub)
							lineW += sub.width
							sh := atomHeight(sub, r.opts.DefaultFontSize)
							if sh > lineMaxH {
								lineMaxH = sh
							}
						}
						continue
					}
				}
				subs := r.splitWordAtomByRune(a)
				for _, sub := range subs {
					if lineW+sub.width > effW && len(line) > 0 {
						if line[len(line)-1].kind == atomSpace {
							lineW -= line[len(line)-1].width
							line = line[:len(line)-1]
						}
						if err := flush(false); err != nil {
							return err
						}
					}
					line = append(line, sub)
					lineW += sub.width
					sh := atomHeight(sub, r.opts.DefaultFontSize)
					if sh > lineMaxH {
						lineMaxH = sh
					}
				}
				continue
			}
		}
		if lineW+a.width > effW && len(line) > 0 {
			// Kinsoku (East Asian line-break): when the atom that
			// would start the next line is a "no-start" punctuation
			// character (close-bracket, full-stop, comma, etc.),
			// keep it on the current line — Word's
			// w:overflowPunct=true / w:kinsoku=true semantics: the
			// punctuation is allowed to overhang the right margin
			// rather than orphan onto a fresh line.
			if r.paragraphKinsoku && r.paragraphOverflowPunct && isKinsokuNoStart(a) {
				line = append(line, a)
				lineW += a.width
				if h > lineMaxH {
					lineMaxH = h
				}
				continue
			}
			// Symmetric rule: when the LAST atom on the current line
			// is a "no-end" opener like "（「『", peel it off and let
			// it start the next line with its trailing content.
			if r.paragraphKinsoku && len(line) > 0 && isKinsokuNoEnd(line[len(line)-1]) {
				orphan := line[len(line)-1]
				line = line[:len(line)-1]
				lineW -= orphan.width
				if len(line) > 0 && line[len(line)-1].kind == atomSpace {
					lineW -= line[len(line)-1].width
					line = line[:len(line)-1]
				}
				if err := flush(false); err != nil {
					return err
				}
				line = append(line, orphan, a)
				lineW = orphan.width + a.width
				if h > lineMaxH {
					lineMaxH = h
				}
				continue
			}
			if len(line) > 0 && line[len(line)-1].kind == atomSpace {
				lineW -= line[len(line)-1].width
				line = line[:len(line)-1]
			}
			if err := flush(false); err != nil {
				return err
			}
			if a.kind == atomSpace {
				continue
			}
		}
		line = append(line, a)
		lineW += a.width
		if h > lineMaxH {
			lineMaxH = h
		}
	}
	return flush(true)
}

// isKinsokuNoStart reports whether the atom's leading rune is a CJK
// punctuation character that must not appear at the START of a line.
// The list covers the most common entries from the JIS X 4051 strict
// no-start set: Japanese closing punctuation, Chinese fullwidth
// punctuation, and the standalone marker glyphs Word emits.
func isKinsokuNoStart(a atom) bool {
	if a.kind != atomWord || a.text == "" {
		return false
	}
	r := []rune(a.text)[0]
	switch r {
	case '、', '。', '，', '．', '：', '；', '！', '？',
		'）', '］', '｝', '〉', '》', '」', '』', '】', '〕', '〗', '〙', '〛',
		'…', '‥', '‼', '⁇', '⁈', '⁉',
		'゠', '〜', '゛', '゜', 'ー', 'ヽ', 'ヾ', 'ゝ', 'ゞ',
		'々', '〻', '・',
		'%', '％', '‰', '°', '′', '″', '℃',
		',', '.', ':', ';', '!', '?', ')', ']', '}':
		return true
	}
	return false
}

// isKinsokuNoEnd reports whether the atom's leading rune is a CJK
// opening punctuation character that must not END a line. When such a
// character lands at the end of a line, the renderer pulls it down to
// the start of the next line with its content.
func isKinsokuNoEnd(a atom) bool {
	if a.kind != atomWord || a.text == "" {
		return false
	}
	r := []rune(a.text)[0]
	switch r {
	case '（', '［', '｛', '〈', '《', '「', '『', '【', '〔', '〖', '〘', '〚',
		'‘', '“', '〝',
		'$', '＄', '￥', '￡', '￦', '€',
		'(', '[', '{':
		return true
	}
	return false
}

// isAllCapsWord reports whether s contains at least one letter and all
// letters are uppercase. Non-letter runes are ignored (so "U.S.A." or
// "WORD123" still classifies as all-caps).
func isAllCapsWord(s string) bool {
	any := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			any = true
		case r >= 'a' && r <= 'z':
			return false
		}
	}
	return any
}

// splitWordAtomByHyphenation tries Liang-style hyphenation on a word
// atom and returns a 2-element slice of atoms (head with trailing
// hyphen, tail) when a legal break fits within maxW. Returns nil when
// no valid hyphenation exists or none fits. Used by layoutLine before
// falling back to per-rune splitting.
func (r *renderer) splitWordAtomByHyphenation(a atom, maxW float64) []atom {
	if a.kind != atomWord || a.text == "" || maxW <= 0 {
		return nil
	}
	// Paragraph opt-out: w:wordWrap = false disables soft breaks entirely.
	if !r.paragraphWordWrap {
		return nil
	}
	// w:doNotHyphenateCaps — skip hyphenation when the source word is
	// entirely uppercase letters.
	if r.doc != nil && r.doc.Settings.DoNotHyphenateCaps && isAllCapsWord(a.text) {
		return nil
	}
	breaks := hyphenateForLang(a.text, a.props.Lang.Latin)
	if len(breaks) == 0 {
		return nil
	}
	_ = r.applyFontFamily(a.props, a.fontFamily)
	// Pick the LARGEST head that fits, so we keep maximal text on the
	// current line. Word's preference is identical.
	bestHead, bestTail := "", ""
	bestHeadW := 0.0
	for i := len(breaks) - 1; i >= 0; i-- {
		k := breaks[i]
		head := a.text[:k] + "-"
		hw, _ := r.pdf.MeasureTextWidth(head)
		if hw <= maxW {
			bestHead = head
			bestTail = a.text[k:]
			bestHeadW = hw
			break
		}
	}
	if bestHead == "" {
		return nil
	}
	tailW, _ := r.pdf.MeasureTextWidth(bestTail)
	return []atom{
		{kind: atomWord, text: bestHead, props: a.props, fontFamily: a.fontFamily, width: bestHeadW, linkRID: a.linkRID, linkAnchor: a.linkAnchor},
		{kind: atomWord, text: bestTail, props: a.props, fontFamily: a.fontFamily, width: tailW, linkRID: a.linkRID, linkAnchor: a.linkAnchor},
	}
}

// splitWordAtomByRune breaks a word atom into one atom per rune, each
// measured at the run's font. Used as the last-resort wrap mechanism
// when a word doesn't fit in the available width (most often in narrow
// table cells). Inherits all metadata — same fontFamily, props, link
// annotation — so each piece styles identically to the parent.
func (r *renderer) splitWordAtomByRune(a atom) []atom {
	_ = r.applyFontFamily(a.props, a.fontFamily)
	runes := []rune(a.text)
	out := make([]atom, 0, len(runes))
	for _, rn := range runes {
		s := string(rn)
		w, _ := r.pdf.MeasureTextWidth(s)
		out = append(out, atom{
			kind:       atomWord,
			text:       s,
			props:      a.props,
			fontFamily: a.fontFamily,
			width:      w,
			linkRID:    a.linkRID,
			linkAnchor: a.linkAnchor,
		})
	}
	return out
}

// drawFloatingShapeWithWrap paints a side-anchored image / shape and
// sets the active wrap band so subsequent text lines flow beside it.
// The shape is drawn at the current cursor y; the cursor is NOT
// advanced. When the band already exists from a prior shape, the new
// shape stacks below the previous one — preserves source document order.
func (r *renderer) drawFloatingShapeWithWrap(a *atom) {
	if a.width <= 0 || a.height <= 0 {
		return
	}
	side := "left"
	switch a.anchorAlignH {
	case "right", "outside":
		side = "right"
	}
	// If a band is already active, start this shape below the previous
	// one so they don't overlap visually.
	shapeTop := r.cursorY
	if r.floatBand != nil && r.cursorY < r.floatBand.bottomY {
		shapeTop = r.floatBand.bottomY
	}
	var imgX float64
	if a.anchorSimplePosUsed {
		// Legacy absolute placement. Override side selection too — the
		// band's side is decided by which half of the page the simplePos
		// falls into.
		imgX = a.anchorSimplePosXPt
		shapeTop = a.anchorSimplePosYPt
		if imgX+a.width/2 > r.marL+r.contentW/2 {
			side = "right"
		} else {
			side = "left"
		}
	} else {
		if side == "right" {
			imgX = r.marL + r.contentW - a.width
		} else {
			imgX = r.marL
		}
		// AnchorOffsetXPt is applied as a small lateral nudge from the
		// snapped left/right edge. Word writes this even when an alignment
		// is specified (especially in templates exported from web editors)
		// so honoring it keeps captions/portraits aligned with the source.
		imgX += a.anchorOffsetXPt
	}
	if a.kind == atomImage {
		var img image.Image
		if strings.Contains(a.imageID, ":crop") {
			img = r.croppedCache[a.imageID]
		} else {
			img = r.doc.Images[a.imageID]
		}
		if img != nil {
			_ = r.drawImage(img, imgX, shapeTop, a.width, a.height)
		}
	} else if a.shape != nil {
		drawVMLShape(r, a.shape, imgX, shapeTop, a.width, a.height)
	}
	band := &floatWrapBand{
		leftX:   imgX,
		rightX:  imgX + a.width,
		topY:    shapeTop,
		bottomY: shapeTop + a.height,
		side:    side,
		gapPt:   6,
	}
	if len(a.anchorWrapPolygon) >= 3 &&
		(a.anchorWrap == "tight" || a.anchorWrap == "through") {
		band.polygon, band.polyMinY, band.polyMaxY = buildWrapPolygon(a.anchorWrapPolygon, imgX, shapeTop, a.width, a.height)
	}
	r.floatBand = band
}

// buildWrapPolygon scales wp:wrapPolygon vertices from their 1/21600-of-
// shape-bbox coordinate space into renderer-space PDF points. Returns the
// translated polygon plus its vertical extent so the scanline check can
// skip rows above / below the contour. When any vertex coordinate exceeds
// 21600, the function rescales by the actual max so out-of-range polygons
// (a few Word builds emit raw EMU instead of normalized units) still land
// within the image bbox.
func buildWrapPolygon(pts []docx.WrapPathPoint, leftX, topY, w, h float64) ([]polyVertex, float64, float64) {
	if len(pts) < 3 || w <= 0 || h <= 0 {
		return nil, 0, 0
	}
	const polyUnit = 21600.0
	scaleX, scaleY := polyUnit, polyUnit
	for _, p := range pts {
		if float64(p.X) > scaleX {
			scaleX = float64(p.X)
		}
		if float64(p.Y) > scaleY {
			scaleY = float64(p.Y)
		}
	}
	out := make([]polyVertex, 0, len(pts))
	minY := math.Inf(1)
	maxY := math.Inf(-1)
	for _, p := range pts {
		px := leftX + (float64(p.X)/scaleX)*w
		py := topY + (float64(p.Y)/scaleY)*h
		if py < minY {
			minY = py
		}
		if py > maxY {
			maxY = py
		}
		out = append(out, polyVertex{x: px, y: py})
	}
	return out, minY, maxY
}

// drawFloatingShapeBehind paints a wp:anchor image whose wrap is
// none/behind/through — text doesn't yield to it. The image is placed
// at marL+anchorOffsetX (or aligned per anchorAlignH) and cursorY+
// anchorOffsetY. The inline cursor and lineMaxH are NOT advanced.
func (r *renderer) drawFloatingShapeBehind(a *atom) {
	if a.width <= 0 || a.height <= 0 {
		return
	}
	var imgX, imgY float64
	if a.anchorSimplePosUsed {
		// Legacy Word 2003 absolute positioning — coordinates are page-
		// relative, ignoring positionH/positionV alignment.
		imgX = a.anchorSimplePosXPt
		imgY = a.anchorSimplePosYPt
	} else {
		imgX = r.marL + a.anchorOffsetXPt
		switch a.anchorAlignH {
		case "right", "outside":
			imgX = r.marL + r.contentW - a.width
		case "center":
			imgX = r.marL + (r.contentW-a.width)/2
		case "left", "inside":
			imgX = r.marL
		}
		imgY = r.cursorY + a.anchorOffsetYPt
	}
	if a.kind == atomImage {
		var img image.Image
		if strings.Contains(a.imageID, ":crop") {
			img = r.croppedCache[a.imageID]
		} else {
			img = r.doc.Images[a.imageID]
		}
		if img != nil {
			_ = r.drawImage(img, imgX, imgY, a.width, a.height)
		}
	} else if a.shape != nil {
		drawVMLShape(r, a.shape, imgX, imgY, a.width, a.height)
	}
}

// lineBandAdjust returns the (x, width, true) constraints for a line
// whose top is at y. Returns (_, _, false) when no band is active or
// the line falls below the band. A side-effect-free read; expiration
// is detected and the field cleared on the next paragraph boundary
// (see drawParagraph) so we don't mutate state during line layout.
//
// When the band carries a wp:wrapPolygon contour, exclusion bounds are
// computed by scan-line intersection with the polygon rather than the
// bbox edges — text follows the silhouette rather than the rectangle.
func (r *renderer) lineBandAdjust(y, baseX, baseW float64) (float64, float64, bool) {
	if r.floatBand == nil {
		return 0, 0, false
	}
	if y >= r.floatBand.bottomY {
		return 0, 0, false
	}
	gap := r.floatBand.gapPt
	leftX, rightX := r.floatBand.leftX, r.floatBand.rightX
	if len(r.floatBand.polygon) >= 3 {
		// Sample the polygon at multiple scanlines spanning the line
		// height, then take the union of the resulting exclusion boxes.
		// More samples handle a polygon that bulges in or out across a
		// line height; the unioned bounds keep text from clipping into
		// the shape.
		lineH := r.opts.DefaultFontSize * 1.2
		const samples = 5
		minX, maxX := math.Inf(1), math.Inf(-1)
		hit := false
		for i := 0; i < samples; i++ {
			ty := y + lineH*float64(i)/float64(samples-1)
			l, rg, ok := polygonScanline(r.floatBand.polygon, r.floatBand.polyMinY, r.floatBand.polyMaxY, ty)
			if !ok {
				continue
			}
			if l < minX {
				minX = l
			}
			if rg > maxX {
				maxX = rg
			}
			hit = true
		}
		if !hit {
			// Line is above or below the polygon's vertical extent:
			// nothing to exclude. Fall through to baseX/baseW.
			return baseX, baseW, true
		}
		leftX, rightX = minX, maxX
	}
	if r.floatBand.side == "left" {
		newX := rightX + gap
		if newX > baseX {
			delta := newX - baseX
			if delta >= baseW {
				return baseX, 0, true
			}
			return newX, baseW - delta, true
		}
		return baseX, baseW, true
	}
	// right side
	limit := leftX - gap
	rightEdge := baseX + baseW
	if rightEdge > limit {
		newW := limit - baseX
		if newW < 0 {
			newW = 0
		}
		return baseX, newW, true
	}
	return baseX, baseW, true
}

// polygonScanline returns the polygon's horizontal span (leftX, rightX) at
// the given scanline y. Returns ok=false when y is outside the polygon's
// vertical extent or no edge crosses the line. Uses linear interpolation
// across each edge (p[i] → p[i+1]) and reports the minimum / maximum x of
// all crossings — adequate for the typical "single closed contour" wrap
// polygon Word emits.
func polygonScanline(poly []polyVertex, minY, maxY, y float64) (float64, float64, bool) {
	if y < minY || y > maxY {
		return 0, 0, false
	}
	lo := math.Inf(1)
	hi := math.Inf(-1)
	hit := false
	for i := range poly {
		p1 := poly[i]
		p2 := poly[(i+1)%len(poly)]
		// Edge straddles the scanline when the endpoints sit on opposite
		// sides; half-open [min,max) avoids double-counting vertices.
		y1, y2 := p1.y, p2.y
		if y1 == y2 {
			continue
		}
		var top, bot polyVertex
		if y1 < y2 {
			top, bot = p1, p2
		} else {
			top, bot = p2, p1
		}
		if y < top.y || y >= bot.y {
			continue
		}
		t := (y - top.y) / (bot.y - top.y)
		x := top.x + t*(bot.x-top.x)
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
		hit = true
	}
	if !hit {
		return 0, 0, false
	}
	return lo, hi, true
}

// clearExpiredFloatBand drops the active wrap band when the cursor has
// passed its bottomY. Called at safe boundaries (paragraph start, page
// break) so the field doesn't churn during line layout.
func (r *renderer) clearExpiredFloatBand() {
	if r.floatBand != nil && r.cursorY >= r.floatBand.bottomY {
		r.floatBand = nil
	}
}

// drawEmphasisMark paints a w:em emphasis mark above (or below for
// "underDot") each glyph of an atom. Values: "dot" (default), "comma",
// "circle", "underDot". This is the CJK convention for stressing words
// without changing typeface — appears as a small mark per character.
//
// We approximate the mark as a tiny filled disc / open ring / comma
// glyph, drawn per-rune so the visual density matches text density. For
// proportional Latin fonts this slightly over-marks since runes share
// glyph clusters, but the rendering remains legible.
func drawEmphasisMark(r *renderer, a atom, cx, baseline float64) {
	style := a.props.Em
	ascent := fontAscent(a.props, r.opts.DefaultFontSize)
	above := style != "underDot"
	markY := baseline - ascent - 0.5
	if !above {
		markY = baseline + 1.5
	}
	runeCount := 0
	for range a.text {
		runeCount++
	}
	if runeCount == 0 {
		return
	}
	step := a.width / float64(runeCount)
	dotRadius := ascent * 0.06
	if dotRadius < 0.4 {
		dotRadius = 0.4
	}
	r.pdf.SetLineWidth(0.4)
	for i := 0; i < runeCount; i++ {
		mx := cx + step*float64(i) + step/2
		switch style {
		case "comma":
			r.pdf.SetFontSize(ascent * 0.55)
			r.pdf.SetX(mx - dotRadius)
			r.pdf.SetY(markY - dotRadius)
			_ = r.pdf.Cell(nil, "'")
		case "circle":
			r.pdf.Oval(mx-dotRadius*1.4, markY-dotRadius*1.4, mx+dotRadius*1.4, markY+dotRadius*1.4)
		default:
			// "dot" and "underDot": small filled disc.
			r.pdf.SetFillColor(0, 0, 0)
			r.pdf.Oval(mx-dotRadius, markY-dotRadius, mx+dotRadius, markY+dotRadius)
		}
	}
	r.pdf.SetFontSize(r.opts.DefaultFontSize)
}

// drawUnderline paints an underline below an atom, honoring the OOXML
// w:u w:val variant the run requested. Most variants map to a dash
// pattern + line weight at a single Y offset; "double" doubles the line,
// "wave" approximates a sinusoid as a triangle wave at small amplitude.
// All other unknown variants fall through to a plain single line so we
// degrade gracefully on novel attributes.
func drawUnderline(r *renderer, a atom, cx, baseline float64) {
	style := a.props.UnderlineStyle
	if style == "" {
		style = "single"
	}
	ulY := baseline + 1
	switch style {
	case "double", "doubleHeavy":
		w := 0.5
		if style == "doubleHeavy" {
			w = 1.0
		}
		r.pdf.SetLineWidth(w)
		r.pdf.Line(cx, ulY-0.5, cx+a.width, ulY-0.5)
		r.pdf.Line(cx, ulY+0.5, cx+a.width, ulY+0.5)
	case "thick":
		r.pdf.SetLineWidth(1.5)
		r.pdf.Line(cx, ulY, cx+a.width, ulY)
	case "dotted":
		drawDashedHLine(r, cx, cx+a.width, ulY, 0.7, 1.2, 0.5)
	case "dottedHeavy":
		drawDashedHLine(r, cx, cx+a.width, ulY, 1.0, 1.5, 1.0)
	case "dash":
		drawDashedHLine(r, cx, cx+a.width, ulY, 2.5, 1.5, 0.5)
	case "dashedHeavy":
		drawDashedHLine(r, cx, cx+a.width, ulY, 3.0, 1.5, 1.0)
	case "dashLong":
		drawDashedHLine(r, cx, cx+a.width, ulY, 5.0, 2.0, 0.5)
	case "dashLongHeavy":
		drawDashedHLine(r, cx, cx+a.width, ulY, 5.0, 2.0, 1.0)
	case "dashDotHeavy":
		drawDashDotHLine(r, cx, cx+a.width, ulY, 1.0)
	case "dashDotDotHeavy":
		drawDashDotDotHLine(r, cx, cx+a.width, ulY, 1.0)
	case "wave", "wavyHeavy", "wavyDouble":
		w := 0.5
		if style == "wavyHeavy" {
			w = 1.0
		}
		r.pdf.SetLineWidth(w)
		drawWaveHLine(r, cx, cx+a.width, ulY, 1.2, 3.0)
		if style == "wavyDouble" {
			drawWaveHLine(r, cx, cx+a.width, ulY+1.5, 1.2, 3.0)
		}
	case "words":
		// Underline only word glyphs, not spaces. We're past the atom-
		// split boundary so this is implicitly the case anyway.
		r.pdf.SetLineWidth(0.5)
		r.pdf.Line(cx, ulY, cx+a.width, ulY)
	default:
		// "single", "" and anything else: solid line.
		r.pdf.SetLineWidth(0.5)
		r.pdf.Line(cx, ulY, cx+a.width, ulY)
	}
}

// drawDashedHLine renders a horizontal dashed line of the given dash/gap
// pattern. Caller is responsible for setting stroke color first.
func drawDashedHLine(r *renderer, x0, x1, y, dash, gap, width float64) {
	r.pdf.SetLineWidth(width)
	step := dash + gap
	for x := x0; x < x1; x += step {
		end := x + dash
		if end > x1 {
			end = x1
		}
		r.pdf.Line(x, y, end, y)
	}
}

// drawDashDotHLine: long-dash + dot pattern.
func drawDashDotHLine(r *renderer, x0, x1, y, width float64) {
	r.pdf.SetLineWidth(width)
	for x := x0; x < x1; {
		end := x + 3.0
		if end > x1 {
			end = x1
		}
		r.pdf.Line(x, y, end, y)
		x = end + 1.5
		if x >= x1 {
			break
		}
		dotEnd := x + 0.5
		if dotEnd > x1 {
			dotEnd = x1
		}
		r.pdf.Line(x, y, dotEnd, y)
		x = dotEnd + 1.5
	}
}

// drawDashDotDotHLine: long-dash + dot + dot pattern.
func drawDashDotDotHLine(r *renderer, x0, x1, y, width float64) {
	r.pdf.SetLineWidth(width)
	for x := x0; x < x1; {
		end := x + 3.0
		if end > x1 {
			end = x1
		}
		r.pdf.Line(x, y, end, y)
		x = end + 1.2
		for k := 0; k < 2; k++ {
			if x >= x1 {
				break
			}
			dotEnd := x + 0.5
			if dotEnd > x1 {
				dotEnd = x1
			}
			r.pdf.Line(x, y, dotEnd, y)
			x = dotEnd + 1.2
		}
	}
}

// drawWaveHLine approximates a sine wave as a triangle wave. Period and
// amplitude are in points; small values look like Word's red-spell-check
// squiggle without blowing up the PDF stream with thousands of segments.
func drawWaveHLine(r *renderer, x0, x1, y, amp, period float64) {
	if period <= 0 {
		period = 3.0
	}
	half := period / 2
	up := true
	for x := x0; x < x1; x += half {
		end := x + half
		if end > x1 {
			end = x1
		}
		if up {
			r.pdf.Line(x, y+amp, end, y-amp)
		} else {
			r.pdf.Line(x, y-amp, end, y+amp)
		}
		up = !up
	}
}

func atomHeight(a atom, defaultSize float64) float64 {
	switch a.kind {
	case atomImage, atomVMLShape, atomMath:
		return a.height
	case atomEACombine:
		// Two stacked halves: total height matches a single line at
		// the run's font size; each half occupies half that height.
		sz := a.props.FontSize
		if sz == 0 {
			sz = defaultSize
		}
		return sz * 1.2
	case atomWord, atomSpace, atomTab:
		sz := a.props.FontSize
		if sz == 0 {
			sz = defaultSize
		}
		return sz * 1.2
	}
	return defaultSize * 1.2
}

func fontAscent(p docx.RunProps, defaultSize float64) float64 {
	sz := p.FontSize
	if sz == 0 {
		sz = defaultSize
	}
	return sz * 0.8
}

// applyComplexScriptProps merges Word's complex-script alternatives into
// the regular bold/italic/size fields when the run is part of an RTL or
// CS-tagged run. Word stores Latin and complex-script formatting on
// separate attrs (w:b vs w:bCs, w:i vs w:iCs, w:sz vs w:szCs) so a doc
// can render the same characters differently depending on script
// resolution; mirroring that here lets the renderer pick the right
// glyph weight without leaking script-awareness deeper into the pipeline.
//
// We trigger on either p.CS (explicit complex-script tag) or paraBidi
// (paragraph-level RTL) so single-character RTL atoms inside a Bidi
// paragraph get the CS treatment even when the run lacks an explicit
// w:cs marker — matching docx4j's "Bidi promotes to CS" behavior.
func applyComplexScriptProps(p docx.RunProps, paraBidi bool) docx.RunProps {
	if !p.CS && !paraBidi {
		return p
	}
	if p.BCs {
		p.Bold = true
	}
	if p.ICs {
		p.Italic = true
	}
	if p.SzCs > 0 {
		p.FontSize = p.SzCs
	}
	return p
}

// allRTL reports whether every rune in s belongs to a right-to-left
// script. Empty string returns false. Used by runsToAtoms to decide
// whether an atom's text should be rune-reversed for RTL display.
func allRTL(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isRTL(r) {
			return false
		}
	}
	return true
}

// combineBracketChars maps w:combineBrackets values to the (opening,
// closing) glyph pair. None / empty pair returns "" / "" so the
// caller renders the bare text. Word's set: round, square, angle,
// curly. We accept those plus "none".
func combineBracketChars(kind string) (string, string) {
	switch kind {
	case "round":
		return "(", ")"
	case "square":
		return "[", "]"
	case "angle":
		return "〈", "〉"
	case "curly":
		return "{", "}"
	}
	return "", ""
}

// reverseRunes returns s with its runes in reverse order. Operates on
// runes (not bytes) so multi-byte characters survive intact.
func reverseRunes(s string) string {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return string(rs)
}

// drawRunTextBorder paints a w:bdr box around the atom rectangle. Style
// "none"/"nil"/"" suppress the box. Width falls back to 0.5pt when sz
// is zero; color defaults to a soft gray so the box reads as a "control"
// without competing with the glyph.
func drawRunTextBorder(r *renderer, e docx.BorderEdge, x, y, w, h float64) {
	if !e.Has() || e.Style == "none" || e.Style == "nil" {
		return
	}
	width := e.Sz
	if width <= 0 {
		width = 0.5
	}
	if e.Color != "" && e.Color != "auto" {
		rr, gg, bb := parseHexColor(e.Color)
		r.pdf.SetStrokeColor(rr, gg, bb)
	} else {
		r.pdf.SetStrokeColor(160, 160, 160)
	}
	r.pdf.SetLineWidth(width)
	// Add a 1pt breathing pad so glyphs don't kiss the frame.
	pad := 1.0
	r.pdf.Rectangle(x-pad, y-pad, x+w+pad, y+h+pad, "D", 0, 0)
	r.pdf.SetStrokeColor(0, 0, 0)
}
