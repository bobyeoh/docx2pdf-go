package render

import (
	"image"
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
}

type atomKind int

const (
	atomWord atomKind = iota
	atomSpace
	atomBreak     // soft line break inside paragraph (w:br with no type)
	atomPageBreak // hard page break (w:br w:type="page")
	atomImage
	atomTab
	atomBookmark // zero-width marker; registers a named PDF anchor at this position
)

// nextTabAfterWithAlign returns the next tab stop strictly past relX
// (measured from line left margin), along with its leader, alignment, and
// a found flag. When no explicit w:tabs apply, falls back to a uniform
// grid — using the doc's w:defaultTabStop when present, else the
// half-inch (720 twips) Word default.
func (r *renderer) nextTabAfterWithAlign(relX float64, p docx.RunProps) (float64, string, string, bool) {
	for _, ts := range r.activeTabs {
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

// drawTabLeader fills the gap between fromX..toX with the leader pattern.
func drawTabLeader(r *renderer, leader string, fromX, toX, baseline float64, props docx.RunProps, defSize float64) {
	if toX-fromX < 4 {
		return
	}
	var ch string
	switch leader {
	case "dot", "middleDot":
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
	var out []atom
	for _, run := range runs {
		if run.Props.Vanish {
			continue
		}
		if run.FootnoteID != "" && !r.drawingFootnotes {
			r.pendingFootnotes = append(r.pendingFootnotes, pendingNote{
				id: run.FootnoteID, endnote: run.IsEndnote,
			})
		}
		if run.Bookmark != "" {
			out = append(out, atom{kind: atomBookmark, text: run.Bookmark})
			continue
		}
		if run.ImageID != "" {
			img, ok := r.doc.Images[run.ImageID]
			if !ok {
				continue
			}
			cropped := run.CropTopPct > 0 || run.CropBottomPct > 0 || run.CropLeftPct > 0 || run.CropRightPct > 0
			imgID := run.ImageID
			if cropped {
				img = cropImage(img, run.CropTopPct, run.CropBottomPct, run.CropLeftPct, run.CropRightPct)
				if r.croppedCache == nil {
					r.croppedCache = map[string]image.Image{}
				}
				imgID = run.ImageID + ":crop"
				r.croppedCache[imgID] = img
			}
			var w, h float64
			if run.ImageWidthPt > 0 && run.ImageHeightPt > 0 {
				w, h = run.ImageWidthPt, run.ImageHeightPt
				if w > r.contentW {
					scale := r.contentW / w
					w *= scale
					h *= scale
				}
			} else {
				w, h = r.fitImage(img)
			}
			out = append(out, atom{kind: atomImage, imageID: imgID, width: w, height: h, props: run.Props, linkRID: run.LinkURL, linkAnchor: run.LinkAnchor})
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
			// reversing its rune sequence here so the glyph stream we
			// hand to gopdf draws in visual (right-to-left) order. Mixed
			// or all-LTR words pass through unchanged — proper UAX#9
			// resolution for embedded LTR runs is out of scope.
			if r.paragraphRTL && allRTL(text) {
				text = reverseRunes(text)
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
				out = append(out, atom{kind: atomTab, props: run.Props, width: w})
			case rn == ' ':
				flushBuf()
				_ = r.applyFontFamily(run.Props, r.selectFont(run.Props))
				w, _ := r.pdf.MeasureTextWidth(" ")
				out = append(out, atom{kind: atomSpace, text: " ", props: run.Props, width: w})
			case isCJK(rn):
				flushBuf()
				fam := r.chooseFamily(rn, run.Props)
				_ = r.applyFontFamily(run.Props, fam)
				s := string(rn)
				w, _ := r.pdf.MeasureTextWidth(s)
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

		if r.pendingMarker != nil {
			pm := r.pendingMarker
			if pm.image != nil {
				em := r.opts.DefaultFontSize
				_ = r.drawImage(pm.image, pm.x, baseline-em, em, em)
			} else {
				_ = r.applyRunFont(pm.props)
				r.pdf.SetX(pm.x)
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
				if err := r.applyFontFamily(a.props, a.fontFamily); err != nil {
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
				if err := r.pdf.Cell(nil, a.text); err != nil {
					return err
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
				if a.props.Underline || a.props.Strike {
					r.pdf.SetLineWidth(0.5)
					r.pdf.SetStrokeColor(0, 0, 0)
					if a.props.Color != "" {
						rr, gg, bb := parseHexColor(a.props.Color)
						r.pdf.SetStrokeColor(rr, gg, bb)
					}
					if a.props.Underline {
						ulY := baseline + 1
						r.pdf.Line(cx, ulY, cx+a.width, ulY)
					}
					if a.props.Strike {
						strikeY := baseline - fontAscent(a.props, r.opts.DefaultFontSize)*0.35
						r.pdf.Line(cx, strikeY, cx+a.width, strikeY)
					}
				}
				if url := r.resolveURL(a.linkRID); url != "" {
					h := fontAscent(a.props, r.opts.DefaultFontSize) * 1.1
					r.pdf.AddExternalLink(url, cx, topY, a.width, h)
				} else if a.linkAnchor != "" {
					h := fontAscent(a.props, r.opts.DefaultFontSize) * 1.1
					r.pdf.AddInternalLink(a.linkAnchor, cx, topY, a.width, h)
				}
				cx += a.width
			case atomSpace:
				cx += a.width + extraSpace
			case atomTab:
				stopX, leader, tabAlign, ok := r.nextTabAfterWithAlign(cx-x, a.props)
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
				if err := r.drawImage(img, cx, r.cursorY, a.width, a.height); err != nil {
					return err
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
		h := atomHeight(a, r.opts.DefaultFontSize)
		// First line gets hang extra width; subsequent lines use r.contentW.
		// hang is zeroed inside flush() so this naturally tightens after the
		// first wrap.
		effW := r.contentW + hang
		// Over-wide word: a single word atom wider than the line's
		// effective width can't be wrapped by the normal "atom-vs-atom"
		// break logic. Split it into per-rune sub-atoms so the breaker
		// can place as many characters as fit per line, then continue.
		// Common in narrow table cells where identifiers like
		// "submission_timestamp" exceed the column width.
		if a.kind == atomWord && effW > 0 && a.width > effW && a.text != "" {
			subs := r.splitWordAtomByRune(a)
			// Replay the per-rune sequence through the same loop logic.
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
		if lineW+a.width > effW && len(line) > 0 {
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

func atomHeight(a atom, defaultSize float64) float64 {
	switch a.kind {
	case atomImage:
		return a.height
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

// reverseRunes returns s with its runes in reverse order. Operates on
// runes (not bytes) so multi-byte characters survive intact.
func reverseRunes(s string) string {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return string(rs)
}
