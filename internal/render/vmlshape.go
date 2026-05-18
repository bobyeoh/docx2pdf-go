package render

import (
	"math"
	"strconv"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

func drawVMLShape(r *renderer, s *docx.VMLShape, x, y, w, h float64) {
	if w <= 0 || h <= 0 {
		return
	}
	// Apply rotation around the shape's center. gopdf.Rotate / RotateReset
	// bracket the body draws below so every primitive (rect, oval, text,
	// children) lands in the rotated frame. FlipH+FlipV collapse to a
	// 180° rotation; single-axis flips don't have a clean equivalent in
	// gopdf so we treat flipH-only as no-op and flipV-only as 180° (the
	// best practical approximation without a true mirror).
	rot := s.RotationDeg
	if s.FlipH && s.FlipV {
		rot += 180
	} else if s.FlipV {
		rot += 180
	}
	if rot != 0 {
		cx, cy := x+w/2, y+h/2
		r.pdf.Rotate(rot, cx, cy)
		defer r.pdf.RotateReset()
	}
	left, top := x, y
	right, bottom := x+w, y+h

	// Chart shapes draw the data graphic directly inside the bounding
	// rect. We still stroke the rect first so the chart sits in a
	// visible frame, matching Word's default chart appearance.
	if s.Chart != nil {
		r.pdf.SetStrokeColor(0xa0, 0xa0, 0xa0)
		r.pdf.SetLineWidth(0.5)
		r.pdf.Rectangle(left, top, right, bottom, "D", 0, 0)
		drawChart(r, s.Chart, left, top, right, bottom)
		return
	}
	// Glow: draw a faint halo behind the shape. We approximate by
	// stroking the shape bounding rect at 1.5x the glow radius with the
	// glow color at low opacity (alpha-blend not available in gopdf, so
	// we just lighten the stroke).
	if s.Glow != nil && s.Glow.RadiusPt > 0 {
		drawShapeGlow(r, s, left, top, right, bottom)
	}

	hasFill := s.FillColor != ""
	hasStroke := s.StrokeColor != ""
	if !hasFill && !hasStroke {
		hasStroke = true
	}

	// Shadow renders before the shape so it sits underneath.
	if s.Shadow != nil {
		drawShapeShadow(r, s, left, top, right, bottom)
	}

	// Gradient fill: rasterize as horizontal/vertical stripes (approximation).
	// We still set the solid fill state below so non-gradient code paths
	// keep working; gradient stripes paint over the rect afterwards.
	hasGradient := s.GradientKind != "" && len(s.GradientStops) >= 2

	stroke := s.StrokeColor
	if !hasStroke {
		stroke = ""
	}
	width := s.StrokeWeightPt
	if width <= 0 {
		width = 0.5
	}
	if stroke == "" {
		r.pdf.SetStrokeColor(0, 0, 0)
	} else {
		sr, sg, sb := parseHexColor(stroke)
		r.pdf.SetStrokeColor(sr, sg, sb)
	}
	r.pdf.SetLineWidth(width)
	if hasFill {
		fr, fg, fb := parseHexColor(s.FillColor)
		r.pdf.SetFillColor(fr, fg, fb)
	} else if hasGradient {
		// Seed fill with the first stop's color so the rect baseline
		// matches the gradient start when the stripe approximation
		// leaves seams.
		fr, fg, fb := parseHexColor(s.GradientStops[0].Color)
		r.pdf.SetFillColor(fr, fg, fb)
		hasFill = true
	}

	// Custom path takes precedence over preset Kind.
	if s.CustomPath != "" {
		drawCustomPath(r, s.CustomPath, left, top, right, bottom, hasFill, hasStroke)
		drawShapeBoxContent(r, s, left, top, right, bottom)
		drawVMLChildren(r, s, left, top, right, bottom)
		return
	}
	// Pattern fill: paint after the solid fill (which has provided a
	// background color via FillColor) but before the stroke. Only kicks
	// in for simple rectangular shapes; complex paths fall back to the
	// average color the parser already wrote into FillColor.
	if s.Pattern != nil && (s.Kind == "rect" || s.Kind == "roundrect" || s.Kind == "") {
		drawPatternTile(r, s.Pattern, left, top, right, bottom)
	}

	switch s.Kind {
	case "rect":
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
	case "roundrect":
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
	case "oval":
		drawShapeOval(r, left, top, right, bottom, hasFill, hasStroke)
	case "line":
		r.pdf.Line(left, top, right, bottom)
		// Arrow heads at start (a:headEnd) and end (a:tailEnd) of the
		// line. Both default to "none". The head sits at (left, top) and
		// the tail at (right, bottom) — matching the order shape.WidthPt /
		// HeightPt declared in the source.
		drawLineArrowHead(r, s.HeadEnd, right, bottom, left, top)
		drawLineArrowHead(r, s.TailEnd, left, top, right, bottom)
	case "polyline":
		drawShapePolyline(r, s.Points, left, top, hasStroke)
	case "group":
		// v:group has no visible body of its own — paint only its
		// children at their relative positions.
	case "triangle":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: (left + right) / 2, Y: top},
			{X: right, Y: bottom},
			{X: left, Y: bottom},
		})
	case "rtTriangle":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left, Y: top},
			{X: right, Y: bottom},
			{X: left, Y: bottom},
		})
	case "parallelogram":
		off := (right - left) * 0.25
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left + off, Y: top},
			{X: right, Y: top},
			{X: right - off, Y: bottom},
			{X: left, Y: bottom},
		})
	case "trapezoid":
		off := (right - left) * 0.2
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left + off, Y: top},
			{X: right - off, Y: top},
			{X: right, Y: bottom},
			{X: left, Y: bottom},
		})
	case "diamond":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: (left + right) / 2, Y: top},
			{X: right, Y: (top + bottom) / 2},
			{X: (left + right) / 2, Y: bottom},
			{X: left, Y: (top + bottom) / 2},
		})
	case "pentagon":
		drawShapeRegularPolygon(r, 5, left, top, right, bottom, hasFill, hasStroke)
	case "hexagon":
		drawShapeRegularPolygon(r, 6, left, top, right, bottom, hasFill, hasStroke)
	case "heptagon":
		drawShapeRegularPolygon(r, 7, left, top, right, bottom, hasFill, hasStroke)
	case "octagon":
		drawShapeRegularPolygon(r, 8, left, top, right, bottom, hasFill, hasStroke)
	case "star4":
		drawShapeStar(r, 4, left, top, right, bottom, hasFill, hasStroke)
	case "star5":
		drawShapeStar(r, 5, left, top, right, bottom, hasFill, hasStroke)
	case "star6":
		drawShapeStar(r, 6, left, top, right, bottom, hasFill, hasStroke)
	case "star7":
		drawShapeStar(r, 7, left, top, right, bottom, hasFill, hasStroke)
	case "star8":
		drawShapeStar(r, 8, left, top, right, bottom, hasFill, hasStroke)
	case "star10":
		drawShapeStar(r, 10, left, top, right, bottom, hasFill, hasStroke)
	case "star12":
		drawShapeStar(r, 12, left, top, right, bottom, hasFill, hasStroke)
	case "star16":
		drawShapeStar(r, 16, left, top, right, bottom, hasFill, hasStroke)
	case "star24":
		drawShapeStar(r, 24, left, top, right, bottom, hasFill, hasStroke)
	case "star32":
		drawShapeStar(r, 32, left, top, right, bottom, hasFill, hasStroke)
	case "rightArrow":
		drawArrow(r, "right", left, top, right, bottom, hasFill, hasStroke)
	case "leftArrow":
		drawArrow(r, "left", left, top, right, bottom, hasFill, hasStroke)
	case "upArrow":
		drawArrow(r, "up", left, top, right, bottom, hasFill, hasStroke)
	case "downArrow":
		drawArrow(r, "down", left, top, right, bottom, hasFill, hasStroke)
	case "leftRightArrow":
		drawArrow(r, "leftRight", left, top, right, bottom, hasFill, hasStroke)
	case "upDownArrow":
		drawArrow(r, "upDown", left, top, right, bottom, hasFill, hasStroke)
	case "chevron", "homePlate":
		off := (right - left) * 0.2
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left, Y: top},
			{X: right - off, Y: top},
			{X: right, Y: (top + bottom) / 2},
			{X: right - off, Y: bottom},
			{X: left, Y: bottom},
		})
	case "plus":
		drawShapePlus(r, left, top, right, bottom, hasFill, hasStroke)
	case "minus":
		// horizontal bar
		mid := (top + bottom) / 2
		barH := (bottom - top) * 0.3
		drawShapeRect(r, left, mid-barH/2, right, mid+barH/2, hasFill, hasStroke)
	case "heart":
		drawShapeHeart(r, left, top, right, bottom, hasFill, hasStroke)
	case "donut":
		drawShapeOval(r, left, top, right, bottom, hasFill, hasStroke)
		// Inner cutout — we don't actually subtract paths, just outline.
		ix := left + (right-left)*0.25
		iy := top + (bottom-top)*0.25
		ax := right - (right-left)*0.25
		ay := bottom - (bottom-top)*0.25
		drawShapeOval(r, ix, iy, ax, ay, false, hasStroke)
	case "callout", "calloutRect", "calloutRoundRect", "calloutEllipse":
		// Speech-bubble approximation: rect (or ellipse) with a small
		// pointer tail in the lower-left corner.
		if s.Kind == "calloutEllipse" {
			drawShapeOval(r, left, top, right, bottom, hasFill, hasStroke)
		} else {
			drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
		}
		tipX := left + (right-left)*0.2
		tailY := bottom + (bottom-top)*0.15
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left + (right-left)*0.1, Y: bottom},
			{X: left + (right-left)*0.3, Y: bottom},
			{X: tipX, Y: tailY},
		})
	case "cloud":
		drawShapeCloud(r, left, top, right, bottom, hasFill, hasStroke)
	case "smiley":
		drawShapeOval(r, left, top, right, bottom, hasFill, hasStroke)
		// Eyes + mouth
		eyeR := (right - left) * 0.05
		ey := top + (bottom-top)*0.35
		ex1 := left + (right-left)*0.35
		ex2 := left + (right-left)*0.65
		r.pdf.Oval(ex1-eyeR, ey-eyeR, ex1+eyeR, ey+eyeR)
		r.pdf.Oval(ex2-eyeR, ey-eyeR, ex2+eyeR, ey+eyeR)
		// Smile arc — approximate with a thin rect baseline.
		smileY := top + (bottom-top)*0.7
		r.pdf.Line(left+(right-left)*0.3, smileY, left+(right-left)*0.7, smileY)
	case "moon":
		drawShapePolygon(r, hasFill, hasStroke, crescentPoints(left, top, right, bottom))
	case "sun":
		drawShapeStar(r, 8, left, top, right, bottom, hasFill, hasStroke)
	case "lightning":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left + (right-left)*0.4, Y: top},
			{X: right, Y: top + (bottom-top)*0.45},
			{X: left + (right-left)*0.55, Y: top + (bottom-top)*0.5},
			{X: left + (right-left)*0.7, Y: bottom},
			{X: left, Y: top + (bottom-top)*0.55},
			{X: left + (right-left)*0.45, Y: top + (bottom-top)*0.5},
		})
	case "noEntry":
		drawShapeOval(r, left, top, right, bottom, false, true)
		r.pdf.Line(left+(right-left)*0.2, top+(bottom-top)*0.8,
			right-(right-left)*0.2, top+(bottom-top)*0.2)
	case "can":
		// Cylinder
		drawShapeOval(r, left, top, right, top+(bottom-top)*0.2, hasFill, hasStroke)
		drawShapeRect(r, left, top+(bottom-top)*0.1, right, bottom-(bottom-top)*0.1, hasFill, hasStroke)
		drawShapeOval(r, left, bottom-(bottom-top)*0.2, right, bottom, hasFill, hasStroke)
	case "cube":
		drawShapeCube(r, left, top, right, bottom, hasFill, hasStroke)
	case "bentArrow":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left, Y: bottom},
			{X: left, Y: top + (bottom-top)*0.4},
			{X: left + (right-left)*0.6, Y: top + (bottom-top)*0.4},
			{X: left + (right-left)*0.6, Y: top + (bottom-top)*0.2},
			{X: right, Y: top + (bottom-top)*0.5},
			{X: left + (right-left)*0.6, Y: top + (bottom-top)*0.8},
			{X: left + (right-left)*0.6, Y: top + (bottom-top)*0.6},
			{X: left + (right-left)*0.15, Y: top + (bottom-top)*0.6},
			{X: left + (right-left)*0.15, Y: bottom},
		})
	case "ribbon", "ribbon2":
		// Ribbon: horizontal banner with notched tails. ribbon2 is the
		// upside-down variant (curls down vs up).
		w, h := right-left, bottom-top
		inset := w * 0.08
		notchY := top + h*0.4
		tailY := top + h*0.7
		if s.Kind == "ribbon2" {
			notchY = top + h*0.6
			tailY = top + h*0.3
		}
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left, Y: tailY},
			{X: left + inset, Y: top + h*0.5},
			{X: left, Y: notchY},
			{X: left + inset*2, Y: notchY},
			{X: left + inset*2, Y: top},
			{X: right - inset*2, Y: top},
			{X: right - inset*2, Y: notchY},
			{X: right, Y: notchY},
			{X: right - inset, Y: top + h*0.5},
			{X: right, Y: tailY},
			{X: right - inset*2, Y: tailY},
			{X: right - inset*2, Y: bottom},
			{X: left + inset*2, Y: bottom},
			{X: left + inset*2, Y: tailY},
		})
	case "flowChartProcess":
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
	case "flowChartDecision":
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: (left + right) / 2, Y: top},
			{X: right, Y: (top + bottom) / 2},
			{X: (left + right) / 2, Y: bottom},
			{X: left, Y: (top + bottom) / 2},
		})
	case "flowChartTerminator":
		// Rounded rectangle with full-height end caps.
		drawShapeOval(r, left, top, left+(bottom-top), bottom, hasFill, hasStroke)
		drawShapeOval(r, right-(bottom-top), top, right, bottom, hasFill, hasStroke)
		drawShapeRect(r, left+(bottom-top)/2, top, right-(bottom-top)/2, bottom, hasFill, hasStroke)
	case "flowChartData", "flowChartInputOutput":
		// Parallelogram (data input/output).
		skew := (right - left) * 0.15
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left + skew, Y: top},
			{X: right, Y: top},
			{X: right - skew, Y: bottom},
			{X: left, Y: bottom},
		})
	case "flowChartDocument":
		// Page with wavy bottom.
		drawShapePolygon(r, hasFill, hasStroke, []gopdf.Point{
			{X: left, Y: top},
			{X: right, Y: top},
			{X: right, Y: bottom - (bottom-top)*0.18},
			{X: (2*left + right) / 3, Y: bottom},
			{X: (left + 2*right) / 3, Y: bottom - (bottom-top)*0.15},
			{X: left, Y: bottom - (bottom-top)*0.08},
		})
	case "flowChartPredefinedProcess":
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
		drawShapeRect(r, left+(right-left)*0.08, top, left+(right-left)*0.08, bottom, false, true)
		drawShapeRect(r, right-(right-left)*0.08, top, right-(right-left)*0.08, bottom, false, true)
	case "bracketPair":
		// Left+right square brackets framing the content area.
		w := (right - left) * 0.06
		drawShapePolygon(r, false, hasStroke, []gopdf.Point{
			{X: left + w, Y: top}, {X: left, Y: top}, {X: left, Y: bottom}, {X: left + w, Y: bottom},
		})
		drawShapePolygon(r, false, hasStroke, []gopdf.Point{
			{X: right - w, Y: top}, {X: right, Y: top}, {X: right, Y: bottom}, {X: right - w, Y: bottom},
		})
	case "leftBracket":
		w := (right - left) * 0.5
		drawShapePolygon(r, false, hasStroke, []gopdf.Point{
			{X: left + w, Y: top}, {X: left, Y: top}, {X: left, Y: bottom}, {X: left + w, Y: bottom},
		})
	case "rightBracket":
		w := (right - left) * 0.5
		drawShapePolygon(r, false, hasStroke, []gopdf.Point{
			{X: right - w, Y: top}, {X: right, Y: top}, {X: right, Y: bottom}, {X: right - w, Y: bottom},
		})
	case "bracePair", "leftBrace", "rightBrace":
		// Curly braces approximated as polylines — Word's TrueType glyph
		// curve isn't reproducible without bezier paths so we use 4-point
		// elbows that read as braces at typical sizes.
		mid := (top + bottom) / 2
		w := (right - left) * 0.3
		if s.Kind != "rightBrace" {
			drawShapePolygon(r, false, hasStroke, []gopdf.Point{
				{X: left + w, Y: top}, {X: left + w*0.4, Y: top + (bottom-top)*0.1},
				{X: left + w*0.4, Y: mid - (bottom-top)*0.05}, {X: left, Y: mid},
				{X: left + w*0.4, Y: mid + (bottom-top)*0.05},
				{X: left + w*0.4, Y: bottom - (bottom-top)*0.1}, {X: left + w, Y: bottom},
			})
		}
		if s.Kind != "leftBrace" {
			drawShapePolygon(r, false, hasStroke, []gopdf.Point{
				{X: right - w, Y: top}, {X: right - w*0.4, Y: top + (bottom-top)*0.1},
				{X: right - w*0.4, Y: mid - (bottom-top)*0.05}, {X: right, Y: mid},
				{X: right - w*0.4, Y: mid + (bottom-top)*0.05},
				{X: right - w*0.4, Y: bottom - (bottom-top)*0.1}, {X: right - w, Y: bottom},
			})
		}
	case "bentConnector2", "bentConnector3", "bentConnector4", "bentConnector5":
		// Right-angle (elbow) routing. We don't have control-handle data
		// so the routing is a simple horizontal-then-vertical at the
		// midpoint — matches the most common author-placed shape.
		mid := (left + right) / 2
		drawShapePolygon(r, false, hasStroke, []gopdf.Point{
			{X: left, Y: top}, {X: mid, Y: top},
			{X: mid, Y: bottom}, {X: right, Y: bottom},
		})
	case "curvedConnector2", "curvedConnector3", "curvedConnector4", "curvedConnector5":
		// Smooth S-curve between (left, top) and (right, bottom). We
		// approximate a single cubic Bézier with 8 line segments.
		p0 := gopdf.Point{X: left, Y: top}
		p1 := gopdf.Point{X: (left + right) / 2, Y: top}
		p2 := gopdf.Point{X: (left + right) / 2, Y: bottom}
		p3 := gopdf.Point{X: right, Y: bottom}
		pts := []gopdf.Point{p0}
		const segs = 8
		for i := 1; i <= segs; i++ {
			tt := float64(i) / segs
			u := 1 - tt
			x := u*u*u*p0.X + 3*u*u*tt*p1.X + 3*u*tt*tt*p2.X + tt*tt*tt*p3.X
			y := u*u*u*p0.Y + 3*u*u*tt*p1.Y + 3*u*tt*tt*p2.Y + tt*tt*tt*p3.Y
			pts = append(pts, gopdf.Point{X: x, Y: y})
		}
		drawShapePolygon(r, false, hasStroke, pts)
	case "explosion1", "explosion2", "irregularSeal1", "irregularSeal2":
		// Jagged-edged seal (16-point star with randomized radii).
		cx := (left + right) / 2
		cy := (top + bottom) / 2
		rx := (right - left) / 2
		ry := (bottom - top) / 2
		const points = 16
		pts := make([]gopdf.Point, 0, points)
		seed := 0.85
		for i := 0; i < points; i++ {
			theta := float64(i) * 2 * 3.14159265 / float64(points)
			rad := 1.0
			if i%2 == 1 {
				rad = seed
				seed = 0.55 + 0.45*((float64((i*7)%5)/4.0)+0.1)
				if seed > 1 {
					seed = 1
				}
			}
			pts = append(pts, gopdf.Point{
				X: cx + rx*rad*math.Cos(theta),
				Y: cy + ry*rad*math.Sin(theta),
			})
		}
		drawShapePolygon(r, hasFill, hasStroke, pts)
	case "arc", "pie", "blockArc":
		// Drawn as ellipse for simplicity — the partial-arc geometry
		// requires sweep+startAngle params we'd have to pull from avLst.
		drawShapeOval(r, left, top, right, bottom, hasFill, hasStroke)
	case "wedgeRectCallout", "wedgeRoundRectCallout":
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
	default:
		// Names that came in as "prst:<unknown>" — render outline only.
		drawShapeRect(r, left, top, right, bottom, hasFill, hasStroke)
	}

	// After the base shape is drawn (with its solid fill or outline),
	// overlay the gradient stripes. We do this AFTER the shape so the
	// stroked outline isn't covered by stripe edges.
	if hasGradient {
		overlayGradient(r, s, left, top, right, bottom)
		// Re-stroke the outline so it sits on top of the gradient.
		if hasStroke {
			drawShapeRect(r, left, top, right, bottom, false, true)
		}
	}
	// Inner shadow: drawn after the fill so it sits over the body but
	// before the textbox content so the text reads cleanly on top.
	if s.InnerShadow != nil {
		drawShapeInnerShadow(r, s, left, top, right, bottom)
	}

	drawShapeBoxContent(r, s, left, top, right, bottom)
	drawVMLChildren(r, s, left, top, right, bottom)
}

// shapeBodyInsets returns the effective text-frame insets in points,
// honoring a:bodyPr/@lIns / tIns / rIns / bIns when set or falling back
// to Word's defaults: 0.1" horizontal, 0.05" vertical.
func shapeBodyInsets(s *docx.VMLShape) (l, t, r, b float64) {
	defL, defT, defR, defB := 7.2, 3.6, 7.2, 3.6
	l, t, r, bb := s.TextLeftInsetPt, s.TextTopInsetPt, s.TextRightInsetPt, s.TextBottomInsetPt
	if l == 0 {
		l = defL
	}
	if t == 0 {
		t = defT
	}
	if r == 0 {
		r = defR
	}
	if bb == 0 {
		bb = defB
	}
	return l, t, r, bb
}

// drawShapeBoxContent prefers the rich block tree (paragraphs, runs with
// formatting) when present, falling back to the flat string for shapes
// whose textbox content didn't survive structured parsing.
func drawShapeBoxContent(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	// Apply a:bodyPr insets: tighten the drawable box per shape config.
	lIns, tIns, rIns, bIns := shapeBodyInsets(s)
	innerLeft := left + lIns
	innerTop := top + tIns
	innerRight := right - rIns
	innerBottom := bottom - bIns
	if innerRight <= innerLeft || innerBottom <= innerTop {
		return
	}
	// Vertical anchor: predict total content height and shift the origin
	// for "ctr" or "b" anchors.
	if s.TextAnchor == "ctr" || s.TextAnchor == "b" {
		h := predictShapeContentHeight(r, s, innerRight-innerLeft)
		boxH := innerBottom - innerTop
		if h < boxH {
			switch s.TextAnchor {
			case "ctr":
				innerTop += (boxH - h) / 2
			case "b":
				innerTop = innerBottom - h
			}
		}
	}
	if len(s.TextBoxBlocks) > 0 {
		stampShapeBlocks(r, s.TextBoxBlocks, innerLeft, innerTop, innerRight, innerBottom)
		return
	}
	if s.TextBox != "" {
		stampShapeText(r, s.TextBox, innerLeft, innerTop, innerRight, innerBottom)
	}
}

// predictShapeContentHeight measures the total y-advance of a shape's
// text content at width w. Used to position the content vertically when
// the shape has a bodyPr anchor of "ctr" or "b" — we need to know how
// much space the content will consume before laying it out.
//
// For plain-string textboxes we approximate using line count × line
// height. For rich blocks we sum paragraph predictions; nested tables
// and unusual blocks fall back to a constant per-block estimate.
func predictShapeContentHeight(r *renderer, s *docx.VMLShape, w float64) float64 {
	lineH := r.opts.DefaultFontSize * 1.2
	if len(s.TextBoxBlocks) > 0 {
		h := 0.0
		for _, b := range s.TextBoxBlocks {
			switch v := b.(type) {
			case docx.Paragraph:
				h += predictShapeParaHeight(r, v, w)
			default:
				_ = v
				h += lineH
			}
		}
		return h
	}
	if s.TextBox == "" {
		return 0
	}
	// Plain string: split on newlines, rough estimate.
	lines := 1
	for _, c := range s.TextBox {
		if c == '\n' {
			lines++
		}
	}
	return float64(lines) * lineH
}

// predictShapeParaHeight is a rough height estimate matching what
// drawShapeParagraph would emit; accurate to ~1 line at typical sizes.
func predictShapeParaHeight(r *renderer, p docx.Paragraph, w float64) float64 {
	lineH := r.opts.DefaultFontSize * 1.2
	// Count rendered text width vs available width to estimate line count.
	total := 0.0
	for _, run := range p.Runs {
		size := r.opts.DefaultFontSize
		if run.Props.FontSize > 0 {
			size = run.Props.FontSize
		}
		total += float64(len(run.Text)) * size * 0.45
	}
	if w <= 0 {
		w = 100
	}
	lines := int(total/w) + 1
	return float64(lines) * lineH
}

// drawShapeShadow paints an offset, dimmer copy of the shape behind the
// real one. We approximate alpha by lightening the shadow color (the
// gopdf backend doesn't expose alpha compositing). Blur is approximated
// by stacking three offset rectangles with progressively wider offsets.
func drawShapeShadow(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	if s.Shadow == nil {
		return
	}
	col := s.Shadow.Color
	if col == "" {
		col = "000000"
	}
	alpha := s.Shadow.Alpha
	if alpha <= 0 {
		alpha = 0.4
	}
	srgb, sg, sb := parseHexColor(col)
	// Lighten by (1-alpha) toward white so the visual "weight" of the
	// shadow matches the opacity request.
	mix := func(v uint8) uint8 {
		f := float64(v)*alpha + 255*(1-alpha)
		if f < 0 {
			return 0
		}
		if f > 255 {
			return 255
		}
		return uint8(f)
	}
	sr, sgg, sbb := mix(srgb), mix(sg), mix(sb)
	r.pdf.SetFillColor(sr, sgg, sbb)
	r.pdf.SetStrokeColor(sr, sgg, sbb)
	r.pdf.SetLineWidth(0)
	dx := s.Shadow.OffsetXPt
	dy := s.Shadow.OffsetYPt
	// Three soft passes for a blurry feel when blur is set.
	passes := 1
	if s.Shadow.BlurPt > 0 {
		passes = 3
	}
	for i := 0; i < passes; i++ {
		shift := float64(i) * s.Shadow.BlurPt * 0.3
		r.pdf.Rectangle(left+dx-shift, top+dy-shift, right+dx+shift, bottom+dy+shift, "F", 0, 0)
	}
}

// drawShapeGlow paints a faint halo around the shape's bounding rect.
// Without alpha-compositing we approximate the glow as a wide, washed-out
// stroke that runs OUTSIDE the rect. Radius scales with the requested
// glow radius; color desaturates toward white per the alpha setting.
func drawShapeGlow(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	g := s.Glow
	if g == nil || g.RadiusPt <= 0 {
		return
	}
	col := g.Color
	if col == "" {
		col = "FFFF00"
	}
	alpha := g.Alpha
	if alpha <= 0 {
		alpha = 0.35
	}
	srgb, sg, sb := parseHexColor(col)
	mix := func(v uint8) uint8 {
		f := float64(v)*alpha + 255*(1-alpha)
		if f < 0 {
			return 0
		}
		if f > 255 {
			return 255
		}
		return uint8(f)
	}
	r.pdf.SetStrokeColor(mix(srgb), mix(sg), mix(sb))
	for i := 1; i <= 3; i++ {
		offset := g.RadiusPt * float64(i) / 3
		r.pdf.SetLineWidth(g.RadiusPt / 2)
		r.pdf.Rectangle(left-offset, top-offset, right+offset, bottom+offset, "D", 0, 0)
	}
}

// drawShapeInnerShadow stripes the inside of the bounding rect with a
// faint darkening band along the top + left edges so the shape looks
// "pressed". Approximates a:innerShdw without alpha compositing.
func drawShapeInnerShadow(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	sh := s.InnerShadow
	if sh == nil {
		return
	}
	col := sh.Color
	if col == "" {
		col = "000000"
	}
	alpha := sh.Alpha
	if alpha <= 0 {
		alpha = 0.5
	}
	srgb, sg, sb := parseHexColor(col)
	mix := func(v uint8) uint8 {
		f := float64(v)*alpha + 255*(1-alpha)
		if f < 0 {
			return 0
		}
		if f > 255 {
			return 255
		}
		return uint8(f)
	}
	r.pdf.SetStrokeColor(mix(srgb), mix(sg), mix(sb))
	r.pdf.SetLineWidth(sh.BlurPt + 0.5)
	// Top and left edges only — the classic "light from upper-left"
	// convention. Offset inward by half the line width.
	off := (sh.BlurPt + 0.5) / 2
	r.pdf.Line(left+off, top+off, right-off, top+off)
	r.pdf.Line(left+off, top+off, left+off, bottom-off)
}

// overlayGradient paints a gradient on top of the shape's bounding box by
// stepping along the angle direction and drawing a slim rectangle for
// each color interpolation. The result clips to the bounding rectangle.
// Radial gradients draw concentric filled ellipses from outside in.
func overlayGradient(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	const steps = 64
	w := right - left
	h := bottom - top
	if w <= 0 || h <= 0 {
		return
	}
	if s.GradientKind == "radial" {
		cx := (left + right) / 2
		cy := (top + bottom) / 2
		for i := steps; i >= 1; i-- {
			frac := float64(i) / float64(steps)
			col := interpolateGradient(s.GradientStops, frac)
			cr, cg, cb := parseHexColor(col)
			r.pdf.SetFillColor(cr, cg, cb)
			rx := (w / 2) * frac
			ry := (h / 2) * frac
			drawShapeOval(r, cx-rx, cy-ry, cx+rx, cy+ry, true, false)
		}
		return
	}
	// Linear: project bounding box onto the gradient axis.
	// Angle in DEGREES, OOXML convention: 0 = left→right, 90 = top→bottom.
	angle := s.GradientAngle
	cosA := cosFloat(angle * pi180)
	sinA := sinFloat(angle * pi180)
	for i := 0; i < steps; i++ {
		frac := float64(i) / float64(steps-1)
		col := interpolateGradient(s.GradientStops, frac)
		cr, cg, cb := parseHexColor(col)
		r.pdf.SetFillColor(cr, cg, cb)
		// Compute a stripe along the perpendicular axis.
		if absF(cosA) >= absF(sinA) {
			// Mostly horizontal gradient — paint vertical stripes.
			x1 := left + w*frac
			x2 := left + w*(frac+1.0/float64(steps-1))
			if cosA < 0 {
				x1 = right - w*frac
				x2 = right - w*(frac+1.0/float64(steps-1))
			}
			if x2 < x1 {
				x1, x2 = x2, x1
			}
			if x2 > right {
				x2 = right
			}
			if x1 < left {
				x1 = left
			}
			r.pdf.Rectangle(x1, top, x2, bottom, "F", 0, 0)
		} else {
			y1 := top + h*frac
			y2 := top + h*(frac+1.0/float64(steps-1))
			if sinA < 0 {
				y1 = bottom - h*frac
				y2 = bottom - h*(frac+1.0/float64(steps-1))
			}
			if y2 < y1 {
				y1, y2 = y2, y1
			}
			if y2 > bottom {
				y2 = bottom
			}
			if y1 < top {
				y1 = top
			}
			r.pdf.Rectangle(left, y1, right, y2, "F", 0, 0)
		}
	}
}

// interpolateGradient linearly interpolates between the two stops that
// bracket `t` in [0,1]. Stops are assumed sorted by Pos.
func interpolateGradient(stops []docx.GradientStop, t float64) string {
	if len(stops) == 0 {
		return "000000"
	}
	if t <= stops[0].Pos {
		return stops[0].Color
	}
	if t >= stops[len(stops)-1].Pos {
		return stops[len(stops)-1].Color
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].Pos {
			a := stops[i-1]
			b := stops[i]
			span := b.Pos - a.Pos
			if span <= 0 {
				return b.Color
			}
			frac := (t - a.Pos) / span
			ar, ag, ab := parseHexColor(a.Color)
			br, bg, bb := parseHexColor(b.Color)
			r := uint8(float64(ar) + (float64(br)-float64(ar))*frac)
			g := uint8(float64(ag) + (float64(bg)-float64(ag))*frac)
			bb2 := uint8(float64(ab) + (float64(bb)-float64(ab))*frac)
			return hexFromRGB(r, g, bb2)
		}
	}
	return stops[len(stops)-1].Color
}

func hexFromRGB(r, g, b uint8) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{
		hex[r>>4], hex[r&0x0f],
		hex[g>>4], hex[g&0x0f],
		hex[b>>4], hex[b&0x0f],
	})
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func cosFloat(rad float64) float64 { return math.Cos(rad) }
func sinFloat(rad float64) float64 { return math.Sin(rad) }

const pi180 = math.Pi / 180.0

// drawVMLChildren recursively paints group children. When CoordSizeW/H
// are set on the parent, children are positioned by their explicit
// OffsetXPt/OffsetYPt within the group's coordinate space — used by
// SmartArt and other shape tree imports. Without coord sizing, children
// are centered and scaled to fit individually (the legacy v:group
// behavior).
func drawVMLChildren(r *renderer, s *docx.VMLShape, left, top, right, bottom float64) {
	if len(s.Children) == 0 {
		return
	}
	parentW := right - left
	parentH := bottom - top
	if parentW <= 0 || parentH <= 0 {
		return
	}
	if s.CoordSizeW > 0 && s.CoordSizeH > 0 {
		sx := parentW / s.CoordSizeW
		sy := parentH / s.CoordSizeH
		for i := range s.Children {
			child := s.Children[i]
			cx := left + child.OffsetXPt*sx
			cy := top + child.OffsetYPt*sy
			cw := child.WidthPt * sx
			ch := child.HeightPt * sy
			if cw <= 0 {
				cw = 1
			}
			if ch <= 0 {
				ch = 1
			}
			drawVMLShape(r, &child, cx, cy, cw, ch)
		}
		return
	}
	for i := range s.Children {
		child := s.Children[i]
		cw := child.WidthPt
		ch := child.HeightPt
		// When the child carries its own size, treat it as a relative
		// fraction of the parent if it looks small; otherwise scale to
		// fit. We default to a centered same-size child when no info.
		if cw <= 0 || ch <= 0 {
			cw = parentW
			ch = parentH
		} else if cw > parentW || ch > parentH {
			scale := math.Min(parentW/cw, parentH/ch)
			cw *= scale
			ch *= scale
		}
		cx := left + (parentW-cw)/2
		cy := top + (parentH-ch)/2
		drawVMLShape(r, &child, cx, cy, cw, ch)
	}
}

func drawShapePolygon(r *renderer, fill, stroke bool, points []gopdf.Point) {
	if len(points) < 3 {
		return
	}
	mode := "D"
	switch {
	case fill && stroke:
		mode = "FD"
	case fill:
		mode = "F"
	}
	r.pdf.Polygon(points, mode)
}

func drawShapeRegularPolygon(r *renderer, n int, left, top, right, bottom float64, fill, stroke bool) {
	if n < 3 {
		return
	}
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	rx := (right - left) / 2
	ry := (bottom - top) / 2
	pts := make([]gopdf.Point, n)
	for i := 0; i < n; i++ {
		theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		pts[i] = gopdf.Point{X: cx + rx*math.Cos(theta), Y: cy + ry*math.Sin(theta)}
	}
	drawShapePolygon(r, fill, stroke, pts)
}

func drawShapeStar(r *renderer, points int, left, top, right, bottom float64, fill, stroke bool) {
	if points < 3 {
		return
	}
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	outerX := (right - left) / 2
	outerY := (bottom - top) / 2
	innerX := outerX * 0.4
	innerY := outerY * 0.4
	pts := make([]gopdf.Point, points*2)
	for i := 0; i < points*2; i++ {
		theta := -math.Pi/2 + math.Pi*float64(i)/float64(points)
		if i%2 == 0 {
			pts[i] = gopdf.Point{X: cx + outerX*math.Cos(theta), Y: cy + outerY*math.Sin(theta)}
		} else {
			pts[i] = gopdf.Point{X: cx + innerX*math.Cos(theta), Y: cy + innerY*math.Sin(theta)}
		}
	}
	drawShapePolygon(r, fill, stroke, pts)
}

func drawArrow(r *renderer, dir string, left, top, right, bottom float64, fill, stroke bool) {
	w := right - left
	h := bottom - top
	if w <= 0 || h <= 0 {
		return
	}
	switch dir {
	case "right":
		bodyH := h * 0.4
		tipW := w * 0.3
		midY := (top + bottom) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: left, Y: midY - bodyH/2},
			{X: right - tipW, Y: midY - bodyH/2},
			{X: right - tipW, Y: top},
			{X: right, Y: midY},
			{X: right - tipW, Y: bottom},
			{X: right - tipW, Y: midY + bodyH/2},
			{X: left, Y: midY + bodyH/2},
		})
	case "left":
		bodyH := h * 0.4
		tipW := w * 0.3
		midY := (top + bottom) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: right, Y: midY - bodyH/2},
			{X: left + tipW, Y: midY - bodyH/2},
			{X: left + tipW, Y: top},
			{X: left, Y: midY},
			{X: left + tipW, Y: bottom},
			{X: left + tipW, Y: midY + bodyH/2},
			{X: right, Y: midY + bodyH/2},
		})
	case "up":
		bodyW := w * 0.4
		tipH := h * 0.3
		midX := (left + right) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: midX - bodyW/2, Y: bottom},
			{X: midX - bodyW/2, Y: top + tipH},
			{X: left, Y: top + tipH},
			{X: midX, Y: top},
			{X: right, Y: top + tipH},
			{X: midX + bodyW/2, Y: top + tipH},
			{X: midX + bodyW/2, Y: bottom},
		})
	case "down":
		bodyW := w * 0.4
		tipH := h * 0.3
		midX := (left + right) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: midX - bodyW/2, Y: top},
			{X: midX - bodyW/2, Y: bottom - tipH},
			{X: left, Y: bottom - tipH},
			{X: midX, Y: bottom},
			{X: right, Y: bottom - tipH},
			{X: midX + bodyW/2, Y: bottom - tipH},
			{X: midX + bodyW/2, Y: top},
		})
	case "leftRight":
		bodyH := h * 0.4
		tipW := w * 0.2
		midY := (top + bottom) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: left, Y: midY},
			{X: left + tipW, Y: top},
			{X: left + tipW, Y: midY - bodyH/2},
			{X: right - tipW, Y: midY - bodyH/2},
			{X: right - tipW, Y: top},
			{X: right, Y: midY},
			{X: right - tipW, Y: bottom},
			{X: right - tipW, Y: midY + bodyH/2},
			{X: left + tipW, Y: midY + bodyH/2},
			{X: left + tipW, Y: bottom},
		})
	case "upDown":
		bodyW := w * 0.4
		tipH := h * 0.2
		midX := (left + right) / 2
		drawShapePolygon(r, fill, stroke, []gopdf.Point{
			{X: midX, Y: top},
			{X: left, Y: top + tipH},
			{X: midX - bodyW/2, Y: top + tipH},
			{X: midX - bodyW/2, Y: bottom - tipH},
			{X: left, Y: bottom - tipH},
			{X: midX, Y: bottom},
			{X: right, Y: bottom - tipH},
			{X: midX + bodyW/2, Y: bottom - tipH},
			{X: midX + bodyW/2, Y: top + tipH},
			{X: right, Y: top + tipH},
		})
	}
}

func drawShapePlus(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	w := right - left
	h := bottom - top
	t := math.Min(w, h) * 0.3
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	drawShapePolygon(r, fill, stroke, []gopdf.Point{
		{X: cx - t/2, Y: top},
		{X: cx + t/2, Y: top},
		{X: cx + t/2, Y: cy - t/2},
		{X: right, Y: cy - t/2},
		{X: right, Y: cy + t/2},
		{X: cx + t/2, Y: cy + t/2},
		{X: cx + t/2, Y: bottom},
		{X: cx - t/2, Y: bottom},
		{X: cx - t/2, Y: cy + t/2},
		{X: left, Y: cy + t/2},
		{X: left, Y: cy - t/2},
		{X: cx - t/2, Y: cy - t/2},
	})
}

func drawShapeHeart(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	w := right - left
	h := bottom - top
	cx := (left + right) / 2
	// Approximate a heart with two semi-circular bumps and a triangle tip.
	bumpR := w * 0.25
	bumpY := top + h*0.3
	r.pdf.Oval(left, top, left+w*0.5, bumpY+bumpR)
	r.pdf.Oval(left+w*0.5, top, right, bumpY+bumpR)
	drawShapePolygon(r, fill, stroke, []gopdf.Point{
		{X: left, Y: bumpY},
		{X: right, Y: bumpY},
		{X: cx, Y: bottom},
	})
}

func drawShapeCloud(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	// Three overlapping ellipses approximating a cloud silhouette.
	w := right - left
	h := bottom - top
	r.pdf.Oval(left, top+h*0.3, left+w*0.6, bottom)
	r.pdf.Oval(left+w*0.3, top, right-w*0.05, bottom-h*0.1)
	r.pdf.Oval(left+w*0.45, top+h*0.2, right, bottom-h*0.05)
	if !fill {
		_ = stroke
	}
}

func drawShapeCube(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	w := right - left
	h := bottom - top
	off := math.Min(w, h) * 0.25
	// Front face
	drawShapeRect(r, left, top+off, right-off, bottom, fill, stroke)
	// Top face
	drawShapePolygon(r, fill, stroke, []gopdf.Point{
		{X: left, Y: top + off},
		{X: left + off, Y: top},
		{X: right, Y: top},
		{X: right - off, Y: top + off},
	})
	// Right face
	drawShapePolygon(r, fill, stroke, []gopdf.Point{
		{X: right - off, Y: top + off},
		{X: right, Y: top},
		{X: right, Y: bottom - off},
		{X: right - off, Y: bottom},
	})
}

func crescentPoints(left, top, right, bottom float64) []gopdf.Point {
	const segs = 18
	w := right - left
	h := bottom - top
	cx := left + w*0.6
	cy := (top + bottom) / 2
	rx := w * 0.45
	ry := h * 0.45
	pts := make([]gopdf.Point, 0, segs*2)
	for i := 0; i <= segs; i++ {
		theta := math.Pi/2 + math.Pi*float64(i)/float64(segs)
		pts = append(pts, gopdf.Point{X: cx + rx*math.Cos(theta), Y: cy + ry*math.Sin(theta)})
	}
	cx2 := left + w*0.75
	rx2 := w * 0.35
	ry2 := h * 0.35
	for i := segs; i >= 0; i-- {
		theta := math.Pi/2 + math.Pi*float64(i)/float64(segs)
		pts = append(pts, gopdf.Point{X: cx2 + rx2*math.Cos(theta), Y: cy + ry2*math.Sin(theta)})
	}
	return pts
}

func drawCustomPath(r *renderer, path string, left, top, right, bottom float64, fill, stroke bool) {
	// Each command in `path` references coordinates in the 0..1 local
	// space — convert to absolute. We don't have access to a true PDF
	// path-building API in gopdf, so we approximate by drawing line
	// segments between consecutive points; curves degrade to straight
	// chords.
	w := right - left
	h := bottom - top
	toks := strings.Fields(path)
	if len(toks) == 0 {
		return
	}
	var pts []gopdf.Point
	var cx, cy float64
	hasCur := false
	i := 0
	flush := func() {
		if len(pts) >= 3 {
			drawShapePolygon(r, fill, stroke, pts)
		} else if len(pts) == 2 && stroke {
			r.pdf.Line(pts[0].X, pts[0].Y, pts[1].X, pts[1].Y)
		}
		pts = pts[:0]
	}
	parsePoint := func(xs, ys string) (float64, float64, bool) {
		x, errx := strconv.ParseFloat(xs, 64)
		y, erry := strconv.ParseFloat(ys, 64)
		if errx != nil || erry != nil {
			return 0, 0, false
		}
		return left + x*w, top + y*h, true
	}
	for i < len(toks) {
		op := toks[i]
		i++
		switch op {
		case "M", "m":
			flush()
			if i+1 < len(toks) {
				if x, y, ok := parsePoint(toks[i], toks[i+1]); ok {
					pts = append(pts, gopdf.Point{X: x, Y: y})
					cx, cy = x, y
					hasCur = true
				}
				i += 2
			}
		case "L", "l":
			if i+1 < len(toks) {
				if x, y, ok := parsePoint(toks[i], toks[i+1]); ok {
					pts = append(pts, gopdf.Point{X: x, Y: y})
					cx, cy = x, y
					hasCur = true
				}
				i += 2
			}
		case "C", "c":
			if i+5 < len(toks) && hasCur {
				// Sample the cubic with 8 line segments between cur and
				// the end point (toks[i+4..i+5]).
				x1, y1, _ := parsePoint(toks[i], toks[i+1])
				x2, y2, _ := parsePoint(toks[i+2], toks[i+3])
				x3, y3, _ := parsePoint(toks[i+4], toks[i+5])
				const subdiv = 8
				for s := 1; s <= subdiv; s++ {
					tt := float64(s) / subdiv
					mt := 1 - tt
					bx := mt*mt*mt*cx + 3*mt*mt*tt*x1 + 3*mt*tt*tt*x2 + tt*tt*tt*x3
					by := mt*mt*mt*cy + 3*mt*mt*tt*y1 + 3*mt*tt*tt*y2 + tt*tt*tt*y3
					pts = append(pts, gopdf.Point{X: bx, Y: by})
				}
				cx, cy = x3, y3
				i += 6
			}
		case "Q", "q":
			if i+3 < len(toks) && hasCur {
				x1, y1, _ := parsePoint(toks[i], toks[i+1])
				x2, y2, _ := parsePoint(toks[i+2], toks[i+3])
				const subdiv = 6
				for s := 1; s <= subdiv; s++ {
					tt := float64(s) / subdiv
					mt := 1 - tt
					bx := mt*mt*cx + 2*mt*tt*x1 + tt*tt*x2
					by := mt*mt*cy + 2*mt*tt*y1 + tt*tt*y2
					pts = append(pts, gopdf.Point{X: bx, Y: by})
				}
				cx, cy = x2, y2
				i += 4
			}
		case "Z", "z":
			if len(pts) > 0 {
				pts = append(pts, pts[0])
			}
			flush()
		}
	}
	flush()
}

func drawShapeRect(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	mode := ""
	switch {
	case fill && stroke:
		mode = "FD"
	case fill:
		mode = "F"
	default:
		mode = "D"
	}
	r.pdf.Rectangle(left, top, right, bottom, mode, 0, 0)
}

func drawShapeOval(r *renderer, left, top, right, bottom float64, fill, stroke bool) {
	if fill {
		const segs = 24
		cx := (left + right) / 2
		cy := (top + bottom) / 2
		rx := (right - left) / 2
		ry := (bottom - top) / 2
		points := make([]gopdf.Point, 0, segs)
		for i := 0; i < segs; i++ {
			theta := 2.0 * math.Pi * float64(i) / float64(segs)
			points = append(points, gopdf.Point{
				X: cx + rx*math.Cos(theta),
				Y: cy + ry*math.Sin(theta),
			})
		}
		r.pdf.Polygon(points, "F")
	}
	if stroke {
		r.pdf.Oval(left, top, right, bottom)
	}
}

func drawShapePolyline(r *renderer, raw string, originX, originY float64, stroke bool) {
	if !stroke {
		return
	}
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	pts := make([]float64, 0, len(tokens))
	for _, tk := range tokens {
		if v, err := strconv.ParseFloat(tk, 64); err == nil {
			pts = append(pts, v)
		}
	}
	if len(pts) < 4 {
		return
	}
	for i := 0; i+3 < len(pts); i += 2 {
		x1 := originX + pts[i]
		y1 := originY + pts[i+1]
		x2 := originX + pts[i+2]
		y2 := originY + pts[i+3]
		r.pdf.Line(x1, y1, x2, y2)
	}
}

func stampShapeText(r *renderer, text string, left, top, right, bottom float64) {
	if err := r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize); err != nil {
		return
	}
	r.pdf.SetTextColor(0, 0, 0)
	w, _ := r.pdf.MeasureTextWidth(text)
	tx := left + ((right-left)-w)/2
	if tx < left+2 {
		tx = left + 2
	}
	ty := top + ((bottom-top)-r.opts.DefaultFontSize)/2
	if ty < top+2 {
		ty = top + 2
	}
	r.pdf.SetX(tx)
	r.pdf.SetY(ty)
	_ = r.pdf.Cell(nil, text)
}

// stampShapeBlocks paints a rich txbxContent block tree inside the shape
// rect. It implements a deliberately small layout pass:
//   - Paragraphs flow top-to-bottom; lines wrap at the inner edge.
//   - Run formatting (bold/italic/size/color) is honored.
//   - Bullet/number list markers render as a leading "• " for any
//     w:numId paragraph (we don't have access to the doc's full
//     numbering here, so the visual stays a generic bullet).
//   - Tables and other non-paragraph blocks fall back to text.
//
// Content that overflows the shape bottom is clipped silently — sizing
// the box is the author's responsibility in Word; growing the shape on
// overflow would push surrounding flow content out of place.
func stampShapeBlocks(r *renderer, blocks []docx.Block, left, top, right, bottom float64) {
	const pad = 2.0
	innerLeft := left + pad
	innerRight := right - pad
	innerTop := top + pad
	innerBottom := bottom - pad
	if innerRight <= innerLeft || innerBottom <= innerTop {
		return
	}
	y := innerTop
	for _, b := range blocks {
		if y >= innerBottom {
			return
		}
		switch v := b.(type) {
		case docx.Paragraph:
			y = drawShapeParagraph(r, v, innerLeft, y, innerRight, innerBottom)
		case docx.Table:
			y = drawShapeTableSummary(r, v, innerLeft, y, innerRight, innerBottom)
		}
	}
}

// drawShapeParagraph greedily wraps the paragraph's runs into lines that
// fit within [left, right] and paints them starting at y, returning the
// y after the last line + the paragraph's after-spacing. Each line's
// baseline alignment honors p.Alignment.
func drawShapeParagraph(r *renderer, p docx.Paragraph, left, y, right, bottom float64) float64 {
	if y >= bottom {
		return y
	}
	maxWidth := right - left
	if maxWidth <= 0 {
		return y
	}
	// Tokenize runs into word atoms (space-separated) so we can wrap.
	type word struct {
		text  string
		props docx.RunProps
	}
	var words []word
	bulletPrefix := ""
	if p.List != nil {
		bulletPrefix = "• "
	}
	for _, run := range p.Runs {
		if run.Text == "" || run.FieldBegin || run.FieldSep || run.FieldEnd || run.InstrText != "" {
			continue
		}
		// Split on whitespace but keep spaces as part of the previous
		// atom so wrap math stays simple. Empty pieces (consecutive
		// spaces) are skipped.
		raw := run.Text
		for len(raw) > 0 {
			// Pull the next "word + trailing space" chunk.
			next := raw
			if idx := strings.IndexAny(raw, " \t"); idx >= 0 {
				next = raw[:idx+1]
				raw = raw[idx+1:]
			} else {
				raw = ""
			}
			if next == "" {
				continue
			}
			words = append(words, word{text: next, props: run.Props})
		}
	}
	if bulletPrefix != "" {
		// Prepend a single bullet word with the first run's props (or
		// defaults when none).
		var bp docx.RunProps
		if len(words) > 0 {
			bp = words[0].props
		}
		words = append([]word{{text: bulletPrefix, props: bp}}, words...)
	}
	if len(words) == 0 {
		// Empty paragraph still advances by a blank line.
		lh := lineHeightFor(r, docx.RunProps{})
		return y + lh
	}
	// Build lines.
	type lineRun struct {
		text  string
		props docx.RunProps
		w     float64
	}
	var lines [][]lineRun
	var cur []lineRun
	curW := 0.0
	for _, w := range words {
		measureW := measureRunText(r, w.text, w.props)
		if curW+measureW > maxWidth && len(cur) > 0 {
			lines = append(lines, cur)
			cur = nil
			curW = 0
		}
		cur = append(cur, lineRun{text: w.text, props: w.props, w: measureW})
		curW += measureW
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	// Paint.
	for _, line := range lines {
		// Compute line height as the max run size on the line.
		lh := 0.0
		totalW := 0.0
		for _, lr := range line {
			h := lineHeightFor(r, lr.props)
			if h > lh {
				lh = h
			}
			totalW += lr.w
		}
		if y+lh > bottom {
			return y
		}
		// Horizontal offset for alignment.
		x := left
		switch p.Alignment {
		case docx.AlignCenter:
			x = left + (maxWidth-totalW)/2
		case docx.AlignRight:
			x = right - totalW
		}
		if x < left {
			x = left
		}
		for _, lr := range line {
			if err := r.applyFontFamily(lr.props, ""); err != nil {
				continue
			}
			r.pdf.SetX(x)
			r.pdf.SetY(y)
			_ = r.pdf.Cell(nil, lr.text)
			x += lr.w
		}
		y += lh
	}
	if p.SpacingAfter > 0 {
		y += p.SpacingAfter
	}
	return y
}

// drawShapeTableSummary renders a textbox-embedded table as a thin
// outlined grid with each cell's collapsed paragraph text — a usable
// fallback when authors put data tables inside a shape. Full table
// layout (merged cells, row heights, conditional formatting) is not
// reproduced here; that lives on the body table renderer.
func drawShapeTableSummary(r *renderer, t docx.Table, left, y, right, bottom float64) float64 {
	if len(t.Rows) == 0 || y >= bottom {
		return y
	}
	maxCols := 0
	for _, row := range t.Rows {
		if len(row.Cells) > maxCols {
			maxCols = len(row.Cells)
		}
	}
	if maxCols == 0 {
		return y
	}
	colW := (right - left) / float64(maxCols)
	rowH := r.opts.DefaultFontSize * 1.4
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	r.pdf.SetLineWidth(0.4)
	for _, row := range t.Rows {
		if y+rowH > bottom {
			return y
		}
		x := left
		for i, cell := range row.Cells {
			cx := left + float64(i)*colW
			r.pdf.Rectangle(cx, y, cx+colW, y+rowH, "D", 0, 0)
			// Cell text: flatten paragraphs to a single string.
			var b strings.Builder
			for _, blk := range cell.Blocks {
				if p, ok := blk.(docx.Paragraph); ok {
					for _, run := range p.Runs {
						b.WriteString(run.Text)
					}
					b.WriteByte(' ')
				}
			}
			txt := strings.TrimSpace(b.String())
			if txt != "" {
				_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.85)
				r.pdf.SetTextColor(0, 0, 0)
				r.pdf.SetX(cx + 2)
				r.pdf.SetY(y + 2)
				_ = r.pdf.Cell(nil, truncateTextRender(txt, int((colW-4)/4)))
			}
			x = cx + colW
		}
		_ = x
		y += rowH
	}
	return y
}

// measureRunText sets the run's font + size, then asks gopdf for the
// width. Restores nothing — callers should reset state as needed.
func measureRunText(r *renderer, text string, p docx.RunProps) float64 {
	if err := r.applyFontFamily(p, ""); err != nil {
		return 0
	}
	w, _ := r.pdf.MeasureTextWidth(text)
	return w
}

func lineHeightFor(r *renderer, p docx.RunProps) float64 {
	sz := p.FontSize
	if sz == 0 {
		sz = r.opts.DefaultFontSize
	}
	return sz * 1.2
}

func truncateTextRender(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return string(s[0])
	}
	return s[:max-1] + "…"
}

// drawLineArrowHead paints an arrow decoration of the given DrawingML type
// at (tipX, tipY), pointing away from (fromX, fromY). Supported types:
// "triangle", "stealth", "arrow" — filled triangles; "oval" — small circle;
// "diamond" — filled rhombus; "none"/"" — no-op. Unknown types degrade to
// a plain triangle so unfamiliar arrow styles still surface the intent.
func drawLineArrowHead(r *renderer, kind string, fromX, fromY, tipX, tipY float64) {
	if kind == "" || kind == "none" {
		return
	}
	dx := tipX - fromX
	dy := tipY - fromY
	dist := math.Hypot(dx, dy)
	if dist <= 0 {
		return
	}
	// Size scales with stroke length but caps so a long line doesn't get
	// an oversized arrow. 8pt is the typical Word default.
	const arrowLen = 8.0
	const arrowWid = 4.0
	ux := dx / dist
	uy := dy / dist
	// Base point sits arrowLen back from tip along the line.
	baseX := tipX - ux*arrowLen
	baseY := tipY - uy*arrowLen
	// Perpendicular for triangle wings.
	px := -uy * arrowWid
	py := ux * arrowWid

	switch kind {
	case "oval":
		r.pdf.Oval(tipX-arrowWid, tipY-arrowWid, tipX+arrowWid, tipY+arrowWid)
	case "diamond":
		mx, my := (tipX+baseX)/2, (tipY+baseY)/2
		drawShapePolygon(r, true, true, []gopdf.Point{
			{X: tipX, Y: tipY},
			{X: mx + px, Y: my + py},
			{X: baseX, Y: baseY},
			{X: mx - px, Y: my - py},
		})
	default:
		// "triangle", "stealth", "arrow", any unknown → filled triangle.
		drawShapePolygon(r, true, true, []gopdf.Point{
			{X: tipX, Y: tipY},
			{X: baseX + px, Y: baseY + py},
			{X: baseX - px, Y: baseY - py},
		})
	}
}
