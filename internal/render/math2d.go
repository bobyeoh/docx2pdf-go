package render

import (
	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// math2d lays out a MathNode tree on the PDF canvas as a 2D expression.
// We use a simple box model:
//
//   - Each box reports its width, ascent (above baseline), and descent
//     (below baseline). All values in PostScript points.
//   - Composite layouts (fractions, radicals, n-ary) stack their child
//     boxes around a baseline using these measurements.
//   - The atomic case is a textual leaf rendered at the active font size.
//
// Limitations: we paint fraction bars and radical vinculums as gopdf
// lines, but variable-height brackets are approximated by stretching the
// glyph baseline rather than drawing a custom path. Matrices use a
// uniform column gap. Good enough to make most algebra readable.

// mathBox represents one laid-out subtree.
type mathBox struct {
	w       float64
	ascent  float64
	descent float64
	draw    func(r *renderer, x, baseline float64)
}

// height returns ascent + descent.
func (b mathBox) height() float64 { return b.ascent + b.descent }

// buildMathBox recursively measures and lays out one MathNode at the
// given font size. Returns the resulting box; the caller invokes
// box.draw(x, baseline) to actually paint at a target location.
func (r *renderer) buildMathBox(n *docx.MathNode, fontSize float64) mathBox {
	if n == nil {
		return mathBox{}
	}
	// m:argPr/m:argSz: per-argument relative size hint in [-2, +2]. Each
	// step is ~0.85× scaling (mirrors Word's UI "smaller/larger" choices).
	// Applied here so every code path that recurses through buildMathBox
	// picks up the size shift uniformly.
	if n.ArgSz != 0 {
		switch n.ArgSz {
		case -2:
			fontSize *= 0.72
		case -1:
			fontSize *= 0.85
		case 1:
			fontSize *= 1.18
		case 2:
			fontSize *= 1.4
		}
	}
	switch n.Kind {
	case "t":
		return r.mathStyledTextBox(applyMathScript(n.Text, n.Script), fontSize, runPropsForMath(n))
	case "r":
		// w:r in OMML carries a w:t leaf — n.Text was populated by the
		// decoder via CharData. Render directly. Children, when present,
		// are non-text formatting wrappers we treat as a sequence.
		if n.Text != "" {
			return r.mathStyledTextBox(applyMathScript(n.Text, n.Script), fontSize, runPropsForMath(n))
		}
		return r.mathSequence(n.Children, fontSize)
	case "e", "num", "den", "deg", "sup", "sub", "lim", "fName", "oMath", "oMathPara":
		if n.Text != "" && len(n.Children) == 0 {
			return r.mathStyledTextBox(n.Text, fontSize, docx.RunProps{Italic: true})
		}
		return r.mathSequence(n.Children, fontSize)
	case "f":
		return r.mathFractionBox(n, fontSize)
	case "rad":
		return r.mathRadicalBox(n, fontSize)
	case "sSup":
		return r.mathSupBox(n, fontSize)
	case "sSub":
		return r.mathSubBox(n, fontSize)
	case "sSubSup":
		return r.mathSubSupBox(n, fontSize)
	case "sPre":
		return r.mathPreScriptBox(n, fontSize)
	case "nary":
		return r.mathNaryBox(n, fontSize)
	case "d":
		return r.mathDelimBox(n, fontSize)
	case "func":
		return r.mathFuncBox(n, fontSize)
	case "acc":
		return r.mathAccBox(n, fontSize)
	case "bar":
		return r.mathBarBox(n, fontSize)
	case "box":
		// m:box is a non-visible grouping — pass the base box through.
		// However, some writers attach m:strikeH/V/BLTR/TLBR to the box
		// itself rather than to m:borderBox. Apply any strikes on top.
		return applyMathStrikes(r.buildMathBox(n.Base, fontSize), n)
	case "borderBox":
		return applyMathStrikes(r.mathBorderBoxBox(n, fontSize), n)
	case "groupChr":
		return r.mathGroupChrBox(n, fontSize)
	case "eqArr":
		return r.mathEqArrBox(n, fontSize)
	case "limLow":
		return r.mathLimBox(n, n.LimLo, true, fontSize)
	case "limUpp":
		return r.mathLimBox(n, n.LimUp, false, fontSize)
	case "m", "matrix":
		return r.mathMatrixBox(n, fontSize)
	case "phant":
		// Phantom: reserve space without rendering.
		inner := r.buildMathBox(n.Base, fontSize)
		inner.draw = nil
		return inner
	}
	// Unknown kind: fall back to the textual approximation.
	if n.Text != "" {
		return r.mathTextBox(n.Text, fontSize)
	}
	return r.mathSequence(n.Children, fontSize)
}

// applyMathStrikes overlays cancellation lines on top of a mathBox per
// m:strikeH/V/BLTR/TLBR. Lines are drawn after the inner box so they
// sit visually atop the glyphs.
func applyMathStrikes(inner mathBox, n *docx.MathNode) mathBox {
	if !n.StrikeH && !n.StrikeV && !n.StrikeBLTR && !n.StrikeTLBR {
		return inner
	}
	origDraw := inner.draw
	w := inner.w
	asc := inner.ascent
	desc := inner.descent
	inner.draw = func(r *renderer, x, baseline float64) {
		if origDraw != nil {
			origDraw(r, x, baseline)
		}
		r.pdf.SetStrokeColor(60, 60, 60)
		r.pdf.SetLineWidth(0.6)
		top := baseline - asc
		bot := baseline + desc
		mid := (top + bot) / 2
		left := x
		right := x + w
		if n.StrikeH {
			r.pdf.Line(left, mid, right, mid)
		}
		if n.StrikeV {
			mx := (left + right) / 2
			r.pdf.Line(mx, top, mx, bot)
		}
		if n.StrikeBLTR {
			r.pdf.Line(left, bot, right, top)
		}
		if n.StrikeTLBR {
			r.pdf.Line(left, top, right, bot)
		}
	}
	return inner
}

// mathTextBox renders a single string at the active font size.
func (r *renderer) mathTextBox(s string, fontSize float64) mathBox {
	return r.mathStyledTextBox(s, fontSize, docx.RunProps{Italic: true})
}

// mathStyledTextBox renders a styled math glyph run. The default math
// styling is italic (variables); m:nor / m:sty p flips to upright,
// m:sty b / bi adds bold.
func (r *renderer) mathStyledTextBox(s string, fontSize float64, props docx.RunProps) mathBox {
	if s == "" {
		return mathBox{}
	}
	w := mustMeasureMath(r, s, fontSize)
	return mathBox{
		w:       w,
		ascent:  fontSize * 0.75,
		descent: fontSize * 0.25,
		draw: func(r *renderer, x, baseline float64) {
			fam := r.selectFont(props)
			style := ""
			if props.Bold && props.Italic {
				style = "BI"
			} else if props.Bold {
				style = "B"
			} else if props.Italic {
				style = "I"
			}
			_ = r.pdf.SetFont(fam, style, fontSize)
			r.pdf.SetFontSize(fontSize)
			r.pdf.SetX(x)
			r.pdf.SetY(baseline - fontSize*0.75)
			_ = r.pdf.Cell(nil, s)
		},
	}
}

// applyMathScript transforms ASCII letters via the Unicode "Mathematical
// Alphanumeric Symbols" block for the requested OMML script style. The
// substitution is lossy when the rendering font lacks the destination
// glyphs (most desktop fonts don't ship 1D400-1D7FF); callers should
// only enable this for documents that explicitly request it.
func applyMathScript(s, script string) string {
	if script == "" || script == "roman" {
		return s
	}
	// Each entry maps the starting code point for uppercase A / lowercase
	// a / digit 0 in the destination block. Empty offsets mean "no
	// transformation for that range".
	var aU, aL, d rune
	switch script {
	case "script":
		aU, aL = 0x1D49C, 0x1D4B6
	case "fraktur":
		aU, aL = 0x1D504, 0x1D51E
	case "doubleStruck":
		aU, aL, d = 0x1D538, 0x1D552, 0x1D7D8
	case "sansSerif":
		aU, aL, d = 0x1D5A0, 0x1D5BA, 0x1D7E2
	case "monospace":
		aU, aL, d = 0x1D670, 0x1D68A, 0x1D7F6
	default:
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z' && aU != 0:
			out = append(out, aU+(r-'A'))
		case r >= 'a' && r <= 'z' && aL != 0:
			out = append(out, aL+(r-'a'))
		case r >= '0' && r <= '9' && d != 0:
			out = append(out, d+(r-'0'))
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// runPropsForMath converts the styling flags on an m:r node into a
// RunProps the renderer's font picker can consume. Variables default to
// italic; m:nor / m:sty p reverts to upright.
func runPropsForMath(n *docx.MathNode) docx.RunProps {
	p := docx.RunProps{Italic: true}
	if n.Nor || n.StyleP {
		p.Italic = false
	}
	if n.StyleB {
		p.Bold = true
		p.Italic = false
	}
	if n.StyleI {
		p.Italic = true
	}
	if n.StyleBI {
		p.Bold = true
		p.Italic = true
	}
	return p
}

func mustMeasureMath(r *renderer, s string, fontSize float64) float64 {
	old := r.opts.DefaultFontSize
	// Ensure a font is selected — MeasureTextWidth panics without one. The
	// renderer's default family is reliably registered, so SetFont is a
	// safe no-op when an active face is already set.
	defer func() { _ = recover() }()
	_ = r.pdf.SetFont(defaultFamily, "", fontSize)
	r.pdf.SetFontSize(fontSize)
	w, _ := r.pdf.MeasureTextWidth(s)
	r.pdf.SetFontSize(old)
	return w
}

// mathSequence lays out a list of children in a single horizontal row.
func (r *renderer) mathSequence(kids []*docx.MathNode, fontSize float64) mathBox {
	if len(kids) == 0 {
		return mathBox{ascent: fontSize * 0.75, descent: fontSize * 0.25}
	}
	if len(kids) == 1 {
		return r.buildMathBox(kids[0], fontSize)
	}
	boxes := make([]mathBox, len(kids))
	totalW := 0.0
	maxA, maxD := 0.0, 0.0
	for i, c := range kids {
		boxes[i] = r.buildMathBox(c, fontSize)
		totalW += boxes[i].w
		if boxes[i].ascent > maxA {
			maxA = boxes[i].ascent
		}
		if boxes[i].descent > maxD {
			maxD = boxes[i].descent
		}
	}
	return mathBox{
		w:       totalW,
		ascent:  maxA,
		descent: maxD,
		draw: func(r *renderer, x, baseline float64) {
			cx := x
			for _, b := range boxes {
				if b.draw != nil {
					b.draw(r, cx, baseline)
				}
				cx += b.w
			}
		},
	}
}

// mathFractionBox stacks numerator over denominator with a horizontal bar.
func (r *renderer) mathFractionBox(n *docx.MathNode, fontSize float64) mathBox {
	// "lin" renders a/b inline — same shape as a regular sequence.
	if n.FracType == "lin" {
		num := r.buildMathBox(n.Num, fontSize)
		den := r.buildMathBox(n.Den, fontSize)
		slash := r.mathTextBox("/", fontSize)
		w := num.w + slash.w + den.w
		asc := num.ascent
		if slash.ascent > asc {
			asc = slash.ascent
		}
		if den.ascent > asc {
			asc = den.ascent
		}
		desc := num.descent
		if slash.descent > desc {
			desc = slash.descent
		}
		if den.descent > desc {
			desc = den.descent
		}
		return mathBox{
			w: w, ascent: asc, descent: desc,
			draw: func(r *renderer, x, baseline float64) {
				cx := x
				if num.draw != nil {
					num.draw(r, cx, baseline)
				}
				cx += num.w
				if slash.draw != nil {
					slash.draw(r, cx, baseline)
				}
				cx += slash.w
				if den.draw != nil {
					den.draw(r, cx, baseline)
				}
			},
		}
	}
	// "skw" renders a skewed fraction: num superscript-left of a slash,
	// den subscript-right. Approximate as a/b with raised num and lowered
	// den so the visual is at least suggestive.
	if n.FracType == "skw" {
		num := r.buildMathBox(n.Num, fontSize*0.85)
		den := r.buildMathBox(n.Den, fontSize*0.85)
		slash := r.mathTextBox("/", fontSize)
		w := num.w + slash.w*0.5 + den.w
		asc := num.ascent + fontSize*0.2
		desc := den.descent + fontSize*0.2
		return mathBox{
			w: w, ascent: asc, descent: desc,
			draw: func(r *renderer, x, baseline float64) {
				if num.draw != nil {
					num.draw(r, x, baseline-fontSize*0.2)
				}
				if slash.draw != nil {
					slash.draw(r, x+num.w, baseline)
				}
				if den.draw != nil {
					den.draw(r, x+num.w+slash.w*0.5, baseline+fontSize*0.2)
				}
			},
		}
	}
	num := r.buildMathBox(n.Num, fontSize*0.9)
	den := r.buildMathBox(n.Den, fontSize*0.9)
	w := num.w
	if den.w > w {
		w = den.w
	}
	const barGap = 1.5
	// The fraction's box ascent reaches the top of the numerator;
	// descent reaches the bottom of the denominator. Baseline sits on
	// the fraction bar.
	asc := num.height() + barGap
	desc := den.height() + barGap
	drawBar := n.FracType != "noBar"
	return mathBox{
		w:       w + 2, // a small margin on each side for the bar
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			barY := baseline
			if num.draw != nil {
				num.draw(r, x+(w-num.w)/2+1, barY-barGap-num.descent)
			}
			if den.draw != nil {
				den.draw(r, x+(w-den.w)/2+1, barY+barGap+den.ascent)
			}
			if drawBar {
				r.pdf.SetLineWidth(0.5)
				r.pdf.SetStrokeColor(0, 0, 0)
				r.pdf.Line(x+1, barY, x+1+w, barY)
			}
		},
	}
}

// mathRadicalBox draws a √ symbol with a horizontal vinculum over the
// base; an optional degree sits as a small superscript on the radical's
// upper-left.
func (r *renderer) mathRadicalBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	const symW = 0.4 // width of the radical sign as a fraction of fontSize
	rsW := fontSize * symW
	w := rsW + base.w + 2
	asc := base.ascent + 2
	desc := base.descent
	// m:radPr/m:degHide: suppress the degree even when present.
	var deg mathBox
	if !n.DegHide {
		deg = r.buildMathBox(n.Deg, fontSize*0.6)
	}
	if deg.w > 0 {
		w += deg.w * 0.7
	}
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			// Paint √ symbol as two strokes plus the vinculum.
			midY := baseline + fontSize*0.2
			topY := baseline - asc + 1
			leftX := x + rsW*0.2
			if deg.w > 0 {
				deg.draw(r, x, baseline-asc*0.7)
				leftX += deg.w * 0.7
			}
			r.pdf.SetLineWidth(0.8)
			r.pdf.SetStrokeColor(0, 0, 0)
			r.pdf.Line(leftX, midY, leftX+rsW*0.4, baseline+desc) // down-stroke
			r.pdf.Line(leftX+rsW*0.4, baseline+desc, leftX+rsW*0.8, topY)
			r.pdf.Line(leftX+rsW*0.8, topY, leftX+rsW+base.w+1, topY) // vinculum
			if base.draw != nil {
				base.draw(r, leftX+rsW, baseline)
			}
		},
	}
}

// mathSupBox stacks a superscript on the base's upper-right.
func (r *renderer) mathSupBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	sup := r.buildMathBox(n.Sup, fontSize*0.75)
	supRise := fontSize * 0.45
	w := base.w + sup.w
	asc := base.ascent + supRise*0.5
	if base.ascent < supRise+sup.ascent {
		asc = supRise + sup.ascent
	}
	desc := base.descent
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			if sup.draw != nil {
				sup.draw(r, x+base.w, baseline-supRise)
			}
		},
	}
}

// mathSubBox stacks a subscript on the base's lower-right.
func (r *renderer) mathSubBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	sub := r.buildMathBox(n.Sub, fontSize*0.75)
	subDrop := fontSize * 0.25
	w := base.w + sub.w
	asc := base.ascent
	desc := base.descent + subDrop
	if base.descent < subDrop+sub.descent {
		desc = subDrop + sub.descent
	}
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			if sub.draw != nil {
				sub.draw(r, x+base.w, baseline+subDrop)
			}
		},
	}
}

// mathSubSupBox stacks both subscript and superscript on the base.
func (r *renderer) mathSubSupBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	sub := r.buildMathBox(n.Sub, fontSize*0.75)
	sup := r.buildMathBox(n.Sup, fontSize*0.75)
	supRise := fontSize * 0.45
	subDrop := fontSize * 0.25
	wExt := sub.w
	if sup.w > wExt {
		wExt = sup.w
	}
	w := base.w + wExt
	asc := supRise + sup.ascent
	if base.ascent > asc {
		asc = base.ascent
	}
	desc := subDrop + sub.descent
	if base.descent > desc {
		desc = base.descent
	}
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			if sup.draw != nil {
				sup.draw(r, x+base.w, baseline-supRise)
			}
			if sub.draw != nil {
				sub.draw(r, x+base.w, baseline+subDrop)
			}
		},
	}
}

// mathPreScriptBox renders m:sPre — a prescript: sub/sup placed BEFORE
// the base rather than after. Common in chemistry / tensor notation
// (₆¹⁴C, the 6 sits as a pre-subscript and 14 as a pre-superscript on
// the carbon glyph). Layout mirrors mathSubSupBox but on the left.
func (r *renderer) mathPreScriptBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	sup := r.buildMathBox(n.Sup, fontSize*0.75)
	sub := r.buildMathBox(n.Sub, fontSize*0.75)
	maxScriptW := sup.w
	if sub.w > maxScriptW {
		maxScriptW = sub.w
	}
	w := maxScriptW + base.w
	supRise := fontSize * 0.45
	subDrop := fontSize * 0.25
	asc := base.ascent
	if supRise+sup.ascent > asc {
		asc = supRise + sup.ascent
	}
	desc := base.descent
	if subDrop+sub.descent > desc {
		desc = subDrop + sub.descent
	}
	return mathBox{
		w: w, ascent: asc, descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			if sup.draw != nil {
				sup.draw(r, x+(maxScriptW-sup.w), baseline-supRise)
			}
			if sub.draw != nil {
				sub.draw(r, x+(maxScriptW-sub.w), baseline+subDrop)
			}
			if base.draw != nil {
				base.draw(r, x+maxScriptW, baseline)
			}
		},
	}
}

// mathNaryBox renders an n-ary operator (∑ / ∫ / ∏) with stacked
// limits when present. The operator glyph defaults to ∑.
func (r *renderer) mathNaryBox(n *docx.MathNode, fontSize float64) mathBox {
	glyph := n.NaryChar
	if glyph == "" {
		glyph = "∑"
	}
	op := r.mathTextBox(glyph, fontSize*1.4)
	base := r.buildMathBox(n.Base, fontSize)
	// Honor m:supHide / m:subHide by zeroing the limit boxes — they then
	// contribute no width or vertical reservation.
	var lo, hi mathBox
	if !n.NarySubHide {
		lo = r.buildMathBox(n.LimLo, fontSize*0.7)
	}
	if !n.NarySupHide {
		hi = r.buildMathBox(n.LimUp, fontSize*0.7)
	}
	// limLoc=subSup → render limits as sub/super on the right of the
	// operator (rather than stacked above/below). The default is
	// limLoc=undOvr for sum-like operators and subSup for integrals;
	// when the spec says subSup explicitly we shift positions.
	limLoc := n.NaryLimLoc
	if limLoc == "" && r.doc != nil {
		// Fall back to document-level m:mathPr defaults: m:intLim for
		// integral-like operators (∫ ∬ ∭ ∮ ∯ ∰), m:naryLim for ∑/∏/⋃/⋂ etc.
		if isIntegralNaryChar(glyph) {
			limLoc = r.doc.Settings.MathProps.IntLim
		} else {
			limLoc = r.doc.Settings.MathProps.NaryLim
		}
	}
	subSupStyle := limLoc == "subSup"
	// When no document or element preference is recorded, integrals default
	// to subSup (Word's display behavior) and other n-ary operators default
	// to undOvr.
	if limLoc == "" && isIntegralNaryChar(glyph) {
		subSupStyle = true
	}
	if subSupStyle {
		w := op.w + lo.w + base.w + 4
		if hi.w > lo.w {
			w = op.w + hi.w + base.w + 4
		}
		asc := op.ascent
		if hi.ascent > 0 && hi.ascent+fontSize*0.4 > asc {
			asc = hi.ascent + fontSize*0.4
		}
		desc := op.descent
		if lo.descent > 0 {
			desc = op.descent + lo.descent*0.3
		}
		return mathBox{
			w:       w,
			ascent:  asc,
			descent: desc,
			draw: func(r *renderer, x, baseline float64) {
				if op.draw != nil {
					op.draw(r, x, baseline)
				}
				if hi.draw != nil {
					hi.draw(r, x+op.w, baseline-fontSize*0.4)
				}
				if lo.draw != nil {
					lo.draw(r, x+op.w, baseline+fontSize*0.2)
				}
				if base.draw != nil {
					rightW := hi.w
					if lo.w > rightW {
						rightW = lo.w
					}
					base.draw(r, x+op.w+rightW+2, baseline)
				}
			},
		}
	}
	limW := lo.w
	if hi.w > limW {
		limW = hi.w
	}
	opSpan := op.w
	if limW > opSpan {
		opSpan = limW
	}
	w := opSpan + base.w + 2
	asc := op.ascent
	if hi.height() > 0 {
		asc += hi.height() + 1
	}
	desc := op.descent
	if lo.height() > 0 {
		desc += lo.height() + 1
	}
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			cx := x + (opSpan-op.w)/2
			if hi.draw != nil {
				hi.draw(r, x+(opSpan-hi.w)/2, baseline-op.ascent-1-hi.descent)
			}
			if op.draw != nil {
				op.draw(r, cx, baseline)
			}
			if lo.draw != nil {
				lo.draw(r, x+(opSpan-lo.w)/2, baseline+op.descent+1+lo.ascent)
			}
			if base.draw != nil {
				base.draw(r, x+opSpan+2, baseline)
			}
		},
	}
}

// isIntegralNaryChar reports whether the n-ary operator glyph belongs to
// the integral family — used to pick the right document-level limLoc
// fallback (m:intLim vs m:naryLim).
func isIntegralNaryChar(g string) bool {
	switch g {
	case "∫", "∬", "∭", "∮", "∯", "∰", "∱", "∲", "∳":
		return true
	}
	return false
}

// mathDelimBox surrounds a body with paired delimiters (paren / bracket /
// brace / pipe). Delimiters stretch by simply scaling their font size to
// match the body height.
func (r *renderer) mathDelimBox(n *docx.MathNode, fontSize float64) mathBox {
	beg := n.BegChar
	if beg == "" {
		beg = "("
	}
	end := n.EndChar
	if end == "" {
		end = ")"
	}
	sep := n.SepChar
	if sep == "" {
		sep = ","
	}
	// Body: all child slots joined by sep.
	parts := []mathBox{}
	if n.Base != nil {
		parts = append(parts, r.buildMathBox(n.Base, fontSize))
	}
	for _, c := range n.Children {
		if c.Kind == "e" {
			parts = append(parts, r.buildMathBox(c, fontSize))
		}
	}
	if len(parts) == 0 {
		parts = []mathBox{r.mathSequence(n.Children, fontSize)}
	}
	bodyW := 0.0
	maxA, maxD := 0.0, 0.0
	for i, p := range parts {
		bodyW += p.w
		if i > 0 {
			bodyW += r.mathTextBox(sep, fontSize).w + 2
		}
		if p.ascent > maxA {
			maxA = p.ascent
		}
		if p.descent > maxD {
			maxD = p.descent
		}
	}
	delimScale := 1.0
	bodyH := maxA + maxD
	if bodyH > fontSize*1.4 {
		delimScale = bodyH / (fontSize * 1.1)
	}
	// m:dPr/m:grow="1" forces vertical scaling to match the body height
	// even when the body is small enough that the heuristic skipped it.
	// In practice this is how Word's "auto-resize delimiters" toggle is
	// surfaced — without honoring grow, fixed-glyph parens around a tall
	// fraction look stunted.
	if n.DGrow && bodyH > fontSize {
		want := bodyH / (fontSize * 1.0)
		if want > delimScale {
			delimScale = want
		}
	}
	// m:dPr/m:shp val="match" — delimiters must stretch to the full body
	// height instead of using Word's "centered" fixed-glyph aesthetic.
	// "centered" (the default) keeps the brace centered on the body
	// midline; with "match" we force the delimiter to fully envelop the
	// body, bumping the scale beyond the bodyH/1.1 heuristic when needed.
	if n.DShape == "match" && bodyH > 0 {
		want := bodyH / fontSize
		if want > delimScale {
			delimScale = want
		}
	}
	begBox := r.mathTextBox(beg, fontSize*delimScale)
	endBox := r.mathTextBox(end, fontSize*delimScale)
	asc := begBox.ascent
	if maxA > asc {
		asc = maxA
	}
	desc := begBox.descent
	if maxD > desc {
		desc = maxD
	}
	return mathBox{
		w:       begBox.w + bodyW + endBox.w + 2,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			cx := x
			begBox.draw(r, cx, baseline)
			cx += begBox.w
			for i, p := range parts {
				if i > 0 {
					sepBox := r.mathTextBox(sep, fontSize)
					sepBox.draw(r, cx, baseline)
					cx += sepBox.w + 2
				}
				if p.draw != nil {
					p.draw(r, cx, baseline)
				}
				cx += p.w
			}
			endBox.draw(r, cx, baseline)
		},
	}
}

// mathFuncBox renders fName(arg) — e.g. sin(x).
func (r *renderer) mathFuncBox(n *docx.MathNode, fontSize float64) mathBox {
	name := r.buildMathBox(n.Arg, fontSize)
	if name.w == 0 {
		// Empty name field — children carry the function name.
		name = r.mathSequence(n.Children, fontSize)
	}
	body := r.buildMathBox(n.Base, fontSize)
	gap := fontSize * 0.15
	w := name.w + gap + body.w
	asc := name.ascent
	if body.ascent > asc {
		asc = body.ascent
	}
	desc := name.descent
	if body.descent > desc {
		desc = body.descent
	}
	return mathBox{
		w:       w,
		ascent:  asc,
		descent: desc,
		draw: func(r *renderer, x, baseline float64) {
			if name.draw != nil {
				name.draw(r, x, baseline)
			}
			if body.draw != nil {
				body.draw(r, x+name.w+gap, baseline)
			}
		},
	}
}

// mathAccBox layers an accent character above the base.
func (r *renderer) mathAccBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	accChar := n.AccChar
	if accChar == "" {
		accChar = "̂"
	}
	acc := r.mathTextBox(accChar, fontSize*0.6)
	asc := base.ascent + acc.height()
	return mathBox{
		w:       base.w,
		ascent:  asc,
		descent: base.descent,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			if acc.draw != nil {
				acc.draw(r, x+(base.w-acc.w)/2, baseline-base.ascent)
			}
		},
	}
}

// mathBarBox draws a horizontal overline over the base.
func (r *renderer) mathBarBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	asc := base.ascent + 2
	return mathBox{
		w:       base.w,
		ascent:  asc,
		descent: base.descent,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			y := baseline - base.ascent - 1
			r.pdf.SetLineWidth(0.5)
			r.pdf.SetStrokeColor(0, 0, 0)
			r.pdf.Line(x, y, x+base.w, y)
		},
	}
}

// mathLimBox renders limLow / limUpp: a base with a low (or high) limit
// underneath.
func (r *renderer) mathLimBox(n *docx.MathNode, lim *docx.MathNode, low bool, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	limBox := r.buildMathBox(lim, fontSize*0.7)
	w := base.w
	if limBox.w > w {
		w = limBox.w
	}
	if low {
		return mathBox{
			w:       w,
			ascent:  base.ascent,
			descent: base.descent + limBox.height() + 1,
			draw: func(r *renderer, x, baseline float64) {
				if base.draw != nil {
					base.draw(r, x+(w-base.w)/2, baseline)
				}
				if limBox.draw != nil {
					limBox.draw(r, x+(w-limBox.w)/2, baseline+base.descent+1+limBox.ascent)
				}
			},
		}
	}
	return mathBox{
		w:       w,
		ascent:  base.ascent + limBox.height() + 1,
		descent: base.descent,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x+(w-base.w)/2, baseline)
			}
			if limBox.draw != nil {
				limBox.draw(r, x+(w-limBox.w)/2, baseline-base.ascent-1-limBox.descent)
			}
		},
	}
}

// mathBorderBoxBox draws a rectangle around its base. Padding is a small
// fraction of fontSize on every side.
func (r *renderer) mathBorderBoxBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	pad := fontSize * 0.2
	hideTop := n.HideTop
	hideBot := n.HideBot
	hideLeft := n.HideLeft
	hideRight := n.HideRight
	return mathBox{
		w:       base.w + pad*2,
		ascent:  base.ascent + pad,
		descent: base.descent + pad,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x+pad, baseline)
			}
			top := baseline - base.ascent - pad
			h := base.height() + pad*2
			right := x + base.w + pad*2
			bot := top + h
			r.pdf.SetLineWidth(0.5)
			r.pdf.SetStrokeColor(0, 0, 0)
			// m:borderBoxPr lets the author hide individual sides — used
			// for "show only the top stroke" notation. We paint each side
			// separately so the hide flags select which lines to draw.
			if !hideTop {
				r.pdf.Line(x, top, right, top)
			}
			if !hideBot {
				r.pdf.Line(x, bot, right, bot)
			}
			if !hideLeft {
				r.pdf.Line(x, top, x, bot)
			}
			if !hideRight {
				r.pdf.Line(right, top, right, bot)
			}
		},
	}
}

// mathGroupChrBox draws a stretchy character (overbrace ⏞ / underbrace ⏟
// by default) above or below the base, sized to span the base width.
func (r *renderer) mathGroupChrBox(n *docx.MathNode, fontSize float64) mathBox {
	base := r.buildMathBox(n.Base, fontSize)
	ch := n.AccChar
	if ch == "" {
		if n.AccUnder {
			ch = "⏟"
		} else {
			ch = "⏞"
		}
	}
	// Scale the group char so its visual width approximates the base
	// width. We use the natural glyph width at fontSize as a starting
	// reference; the renderer paints it as a single glyph (no path
	// stretching) so the result is decorative more than metric-perfect.
	natural := r.mathTextBox(ch, fontSize)
	scale := 1.0
	if natural.w > 0 && base.w > natural.w {
		scale = base.w / natural.w
		if scale > 3.0 {
			scale = 3.0
		}
	}
	chBox := r.mathTextBox(ch, fontSize*scale)
	gap := fontSize * 0.1
	if n.AccUnder {
		return mathBox{
			w:       base.w,
			ascent:  base.ascent,
			descent: base.descent + chBox.height() + gap,
			draw: func(r *renderer, x, baseline float64) {
				if base.draw != nil {
					base.draw(r, x, baseline)
				}
				if chBox.draw != nil {
					chBox.draw(r, x+(base.w-chBox.w)/2, baseline+base.descent+gap+chBox.ascent)
				}
			},
		}
	}
	return mathBox{
		w:       base.w,
		ascent:  base.ascent + chBox.height() + gap,
		descent: base.descent,
		draw: func(r *renderer, x, baseline float64) {
			if base.draw != nil {
				base.draw(r, x, baseline)
			}
			if chBox.draw != nil {
				chBox.draw(r, x+(base.w-chBox.w)/2, baseline-base.ascent-gap-chBox.descent)
			}
		},
	}
}

// mathEqArrBox renders m:eqArr — a vertical stack of equations. Honors
// EqMaxDist (extra row padding, "Maximize Distance" toggle), EqRowSpRule
// (custom row spacing rule, 1..4), and falls back to centered alignment
// when none of those drive a more specific layout.
func (r *renderer) mathEqArrBox(n *docx.MathNode, fontSize float64) mathBox {
	rows := []mathBox{}
	maxW := 0.0
	for _, c := range n.Children {
		if c.Kind == "e" {
			b := r.buildMathBox(c, fontSize)
			rows = append(rows, b)
			if b.w > maxW {
				maxW = b.w
			}
		}
	}
	if len(rows) == 0 {
		return mathBox{}
	}
	rowGap := 2.0
	// EqMaxDist asks for the maximum vertical separation between rows so
	// stacked subscripts/superscripts don't visually touch. Bumping the
	// gap by ~30% of the font matches Word's spacing in practice.
	if n.EqMaxDist {
		rowGap = fontSize * 0.35
	}
	// EqRowSpRule: 1=single, 2=1.5x, 3=double, 4=at-least. We approximate
	// 1.5/double by multiplying the gap; "at-least" keeps the default
	// since callers can't supply the minimum from here.
	switch n.EqRowSpRule {
	case 2:
		rowGap *= 1.5
	case 3:
		rowGap *= 2
	}
	totalH := 0.0
	for _, b := range rows {
		totalH += b.height() + rowGap
	}
	if totalH > 0 {
		totalH -= rowGap
	}
	return mathBox{
		w:       maxW,
		ascent:  totalH / 2,
		descent: totalH / 2,
		draw: func(r *renderer, x, baseline float64) {
			y := baseline - totalH/2
			for _, b := range rows {
				if b.draw != nil {
					// Center each row in the column.
					b.draw(r, x+(maxW-b.w)/2, y+b.ascent)
				}
				y += b.height() + rowGap
			}
		},
	}
}

// mathMatrixBox lays the rows out in a grid with uniform column spacing.
func (r *renderer) mathMatrixBox(n *docx.MathNode, fontSize float64) mathBox {
	if len(n.Rows) == 0 {
		return mathBox{}
	}
	cols := 0
	for _, r := range n.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	cellBoxes := make([][]mathBox, len(n.Rows))
	colW := make([]float64, cols)
	rowH := make([]float64, len(n.Rows))
	for i, row := range n.Rows {
		cellBoxes[i] = make([]mathBox, cols)
		for j := 0; j < cols; j++ {
			if j < len(row) {
				cellBoxes[i][j] = r.buildMathBox(row[j], fontSize)
			}
			if cellBoxes[i][j].w > colW[j] {
				colW[j] = cellBoxes[i][j].w
			}
			h := cellBoxes[i][j].height()
			if h > rowH[i] {
				rowH[i] = h
			}
		}
	}
	const colGap = 6.0
	const rowGap = 2.0
	totalW := 0.0
	for _, w := range colW {
		totalW += w + colGap
	}
	if totalW > 0 {
		totalW -= colGap
	}
	totalH := 0.0
	for _, h := range rowH {
		totalH += h + rowGap
	}
	if totalH > 0 {
		totalH -= rowGap
	}
	// Resolve per-column alignment ("l"/"c"/"r"); default center.
	colJc := make([]string, cols)
	for j := range colJc {
		colJc[j] = "c"
	}
	for j, jc := range n.MatrixColJc {
		if j >= cols {
			break
		}
		switch jc {
		case "l", "c", "r":
			colJc[j] = jc
		}
	}
	return mathBox{
		w:       totalW + 4,
		ascent:  totalH / 2,
		descent: totalH / 2,
		draw: func(r *renderer, x, baseline float64) {
			y := baseline - totalH/2
			for i, row := range cellBoxes {
				cx := x + 2
				for j := 0; j < cols; j++ {
					if row[j].draw != nil {
						cellAsc := row[j].ascent
						var ox float64
						switch colJc[j] {
						case "l":
							ox = 0
						case "r":
							ox = colW[j] - row[j].w
						default:
							ox = (colW[j] - row[j].w) / 2
						}
						row[j].draw(r, cx+ox, y+cellAsc)
					}
					cx += colW[j] + colGap
				}
				y += rowH[i] + rowGap
			}
		},
	}
}
