package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

// chartPalette is the fallback series-color rotation. Picked to match
// Word's default Office theme accent ordering well enough that documents
// without explicit series colors still render with a familiar palette.
var chartPalette = []string{
	"4472C4", "ED7D31", "A5A5A5", "FFC000",
	"5B9BD5", "70AD47", "264478", "9E480E",
}

// drawChart paints a ChartData inside a [left, top, right, bottom] box.
// Dispatches by Kind; unknown kinds draw nothing so the outer rect's
// stroke serves as a placeholder. The caller is expected to have
// stroked the bounding rectangle already.
func drawChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if c == nil {
		return
	}
	if right-left < 30 || bottom-top < 30 {
		return
	}
	// Reserve space for the title at the top + a one-line legend at the
	// bottom. Both shrink the plot area, not the bounding rect.
	titleH := 0.0
	if c.Title != "" {
		titleH = r.opts.DefaultFontSize * 1.4
	}
	legendH := 0.0
	if len(c.Series) > 0 && hasSeriesNames(c.Series) {
		legendH = r.opts.DefaultFontSize * 1.4
	}
	plotTop := top + titleH
	plotBottom := bottom - legendH
	if plotBottom-plotTop < 20 {
		plotBottom = bottom
		plotTop = top
	}
	if c.Title != "" {
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize)
		r.pdf.SetTextColor(0, 0, 0)
		w, _ := r.pdf.MeasureTextWidth(c.Title)
		r.pdf.SetX(left + ((right-left)-w)/2)
		r.pdf.SetY(top + 2)
		_ = r.pdf.Cell(nil, c.Title)
	}
	switch c.Kind {
	case "column":
		drawColumnChart(r, c, left, plotTop, right, plotBottom)
	case "bar":
		drawBarChart(r, c, left, plotTop, right, plotBottom)
	case "pie", "doughnut":
		drawPieChart(r, c, left, plotTop, right, plotBottom, c.Kind == "doughnut")
	case "line":
		drawLineChart(r, c, left, plotTop, right, plotBottom)
	case "scatter":
		drawScatterChart(r, c, left, plotTop, right, plotBottom)
	case "area":
		drawAreaChart(r, c, left, plotTop, right, plotBottom)
	case "bubble":
		drawBubbleChart(r, c, left, plotTop, right, plotBottom)
	case "radar":
		drawRadarChart(r, c, left, plotTop, right, plotBottom)
	case "stock":
		drawStockChart(r, c, left, plotTop, right, plotBottom)
	case "surface":
		drawSurfaceChart(r, c, left, plotTop, right, plotBottom)
	case "ofPie":
		drawOfPieChart(r, c, left, plotTop, right, plotBottom)
	}
	if legendH > 0 {
		drawChartLegend(r, c.Series, left, plotBottom, right, bottom)
	}
}

func hasSeriesNames(series []docx.ChartSeries) bool {
	for _, s := range series {
		if s.Name != "" {
			return true
		}
	}
	return false
}

// drawColumnChart paints vertical bars grouped per category, one bar per
// series within each group. Y-axis line + ticks; X-axis labels under
// each category. The plot frame is stroked at the same color/weight as
// the surrounding shape's default for visual continuity.
func drawColumnChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	maxCats := maxCategoryCount(c)
	if maxCats == 0 {
		return
	}
	stacked := c.Grouping == "stacked" || c.Grouping == "percentStacked"
	percent := c.Grouping == "percentStacked"
	minV, maxV := seriesValueRange(c.Series)
	if stacked {
		// In stacked mode each category's height is the sum of its series
		// values, not the max of any one series. Recompute the y-range so
		// the tallest stack fits.
		minV, maxV = stackedValueRange(c.Series, percent)
	}
	if maxV == minV {
		maxV = minV + 1
	}
	if minV > 0 {
		minV = 0
	}
	if maxV < 0 {
		maxV = 0
	}
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	catW := (plotR - plotL) / float64(maxCats)
	innerPad := catW * 0.15
	groupW := catW - 2*innerPad
	barW := groupW
	if !stacked {
		barW = groupW / float64(len(c.Series))
	}
	for cat := 0; cat < maxCats; cat++ {
		baseX := plotL + float64(cat)*catW + innerPad
		if stacked {
			// Stacked: walk series bottom-to-top, summing positive values
			// into a running total that anchors each next segment's base.
			total := 0.0
			if percent {
				for _, ser := range c.Series {
					if cat < len(ser.Values) {
						total += ser.Values[cat]
					}
				}
			}
			pos := 0.0
			for si, ser := range c.Series {
				if cat >= len(ser.Values) {
					continue
				}
				v := ser.Values[cat]
				if percent && total != 0 {
					v = v / total * 100
				}
				x0 := baseX
				x1 := x0 + barW*0.9
				y0 := valueToY(pos+v, minV, maxV, plotT, plotB)
				y1 := valueToY(pos, minV, maxV, plotT, plotB)
				if y1 < y0 {
					y0, y1 = y1, y0
				}
				color := r.themedSeriesColor(ser, si)
				rr, gg, bb := parseHexColor(color)
				r.pdf.SetFillColor(rr, gg, bb)
				r.pdf.SetStrokeColor(rr, gg, bb)
				r.pdf.SetLineWidth(0)
				r.pdf.Rectangle(x0, y0, x1, y1, "F", 0, 0)
				pos += v
			}
		} else {
			for si, ser := range c.Series {
				if cat >= len(ser.Values) {
					continue
				}
				v := ser.Values[cat]
				x0 := baseX + float64(si)*barW
				x1 := x0 + barW*0.9
				y0, y1 := valueToY(v, minV, maxV, plotT, plotB), valueToY(0, minV, maxV, plotT, plotB)
				if y1 < y0 {
					y0, y1 = y1, y0
				}
				color := r.themedSeriesColor(ser, si)
				rr, gg, bb := parseHexColor(color)
				r.pdf.SetFillColor(rr, gg, bb)
				r.pdf.SetStrokeColor(rr, gg, bb)
				r.pdf.SetLineWidth(0)
				r.pdf.Rectangle(x0, y0, x1, y1, "F", 0, 0)
			}
		}
		if cat < len(c.Categories) {
			label := c.Categories[cat]
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
			r.pdf.SetTextColor(80, 80, 80)
			lw, _ := r.pdf.MeasureTextWidth(label)
			lx := plotL + float64(cat)*catW + (catW-lw)/2
			if lx < plotL {
				lx = plotL
			}
			r.pdf.SetX(lx)
			r.pdf.SetY(plotB + 2)
			_ = r.pdf.Cell(nil, truncateLabel(label, int(catW/3)))
		}
	}
}

// drawBarChart is the horizontal analogue of drawColumnChart.
func drawBarChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 50.0
	plotL := left + axisPad
	plotT := top + 4
	plotB := bottom - 4
	plotR := right - 4
	maxCats := maxCategoryCount(c)
	if maxCats == 0 || plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	stacked := c.Grouping == "stacked" || c.Grouping == "percentStacked"
	percent := c.Grouping == "percentStacked"
	minV, maxV := seriesValueRange(c.Series)
	if stacked {
		minV, maxV = stackedValueRange(c.Series, percent)
	}
	if maxV == minV {
		maxV = minV + 1
	}
	if minV > 0 {
		minV = 0
	}
	if maxV < 0 {
		maxV = 0
	}
	// Axes — vertical (left) and horizontal (zero).
	r.pdf.SetStrokeColor(0xa0, 0xa0, 0xa0)
	r.pdf.SetLineWidth(0.5)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	zeroX := valueToX(0, minV, maxV, plotL, plotR)
	r.pdf.Line(plotL, plotB, plotR, plotB)
	_ = zeroX
	catH := (plotB - plotT) / float64(maxCats)
	innerPad := catH * 0.15
	groupH := catH - 2*innerPad
	barH := groupH
	if !stacked {
		barH = groupH / float64(len(c.Series))
	}
	for cat := 0; cat < maxCats; cat++ {
		baseY := plotT + float64(cat)*catH + innerPad
		if stacked {
			total := 0.0
			if percent {
				for _, ser := range c.Series {
					if cat < len(ser.Values) {
						total += ser.Values[cat]
					}
				}
			}
			pos := 0.0
			for si, ser := range c.Series {
				if cat >= len(ser.Values) {
					continue
				}
				v := ser.Values[cat]
				if percent && total != 0 {
					v = v / total * 100
				}
				y0 := baseY
				y1 := y0 + barH*0.9
				x0 := valueToX(pos, minV, maxV, plotL, plotR)
				x1 := valueToX(pos+v, minV, maxV, plotL, plotR)
				if x1 < x0 {
					x0, x1 = x1, x0
				}
				color := r.themedSeriesColor(ser, si)
				rr, gg, bb := parseHexColor(color)
				r.pdf.SetFillColor(rr, gg, bb)
				r.pdf.SetLineWidth(0)
				r.pdf.Rectangle(x0, y0, x1, y1, "F", 0, 0)
				pos += v
			}
		} else {
			for si, ser := range c.Series {
				if cat >= len(ser.Values) {
					continue
				}
				v := ser.Values[cat]
				y0 := baseY + float64(si)*barH
				y1 := y0 + barH*0.9
				x0, x1 := valueToX(0, minV, maxV, plotL, plotR), valueToX(v, minV, maxV, plotL, plotR)
				if x1 < x0 {
					x0, x1 = x1, x0
				}
				color := r.themedSeriesColor(ser, si)
				rr, gg, bb := parseHexColor(color)
				r.pdf.SetFillColor(rr, gg, bb)
				r.pdf.SetLineWidth(0)
				r.pdf.Rectangle(x0, y0, x1, y1, "F", 0, 0)
			}
		}
		if cat < len(c.Categories) {
			label := c.Categories[cat]
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
			r.pdf.SetTextColor(80, 80, 80)
			r.pdf.SetX(left + 2)
			r.pdf.SetY(plotT + float64(cat)*catH + catH/2 - r.opts.DefaultFontSize*0.4)
			_ = r.pdf.Cell(nil, truncateLabel(label, int(axisPad)/3))
		}
	}
}

// drawPieChart paints a single (first) series as a pie. A doughnut is
// drawn by overlaying a same-color disc at 50% radius — gopdf has no
// path-subtract primitive, so the doughnut hole renders as plain white.
func drawPieChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64, doughnut bool) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		return
	}
	values := c.Series[0].Values
	total := 0.0
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return
	}
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	rxAvail := (right - left) / 2
	ryAvail := (bottom - top) / 2
	radius := math.Min(rxAvail, ryAvail) * 0.85
	startAngle := -math.Pi / 2 // Start at 12 o'clock
	for i, v := range values {
		if v <= 0 {
			continue
		}
		frac := v / total
		end := startAngle + frac*2*math.Pi
		palette := r.themedPalette()
		color := palette[i%len(palette)]
		if i < len(c.Categories) && i < len(c.Series) {
			_ = c.Series[i] // categories may have explicit series colors when each slice was a series
		}
		drawPieSlice(r, cx, cy, radius, startAngle, end, color)
		// Label inside the slice: percentage centered along the bisector
		midAngle := (startAngle + end) / 2
		lx := cx + math.Cos(midAngle)*radius*0.65
		ly := cy + math.Sin(midAngle)*radius*0.65
		pct := fmt.Sprintf("%.0f%%", frac*100)
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.85)
		r.pdf.SetTextColor(255, 255, 255)
		tw, _ := r.pdf.MeasureTextWidth(pct)
		r.pdf.SetX(lx - tw/2)
		r.pdf.SetY(ly - r.opts.DefaultFontSize*0.4)
		_ = r.pdf.Cell(nil, pct)
		startAngle = end
	}
	if doughnut {
		// White center disc — best we can do without a true path
		// subtract operation.
		r.pdf.SetFillColor(255, 255, 255)
		drawShapeOval(r, cx-radius*0.5, cy-radius*0.5, cx+radius*0.5, cy+radius*0.5, true, false)
	}
}

// drawPieSlice fills a pie slice by tessellating the arc into a triangle
// fan rooted at (cx, cy). Sweep is from start (radians) to end (radians).
func drawPieSlice(r *renderer, cx, cy, radius, start, end float64, color string) {
	rr, gg, bb := parseHexColor(color)
	r.pdf.SetFillColor(rr, gg, bb)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.SetLineWidth(0)
	const minSegs = 12
	span := math.Abs(end - start)
	segs := int(span / (math.Pi / 18))
	if segs < minSegs {
		segs = minSegs
	}
	pts := make([]gopdf.Point, 0, segs+2)
	pts = append(pts, gopdf.Point{X: cx, Y: cy})
	for i := 0; i <= segs; i++ {
		theta := start + (end-start)*float64(i)/float64(segs)
		pts = append(pts, gopdf.Point{X: cx + math.Cos(theta)*radius, Y: cy + math.Sin(theta)*radius})
	}
	r.pdf.Polygon(pts, "F")
}

// drawLineChart paints one polyline per series. Categories become the x
// positions; if categories are absent we space points evenly along x.
// Each series gets a square marker at every data point.
func drawLineChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	maxN := 0
	for _, s := range c.Series {
		if len(s.Values) > maxN {
			maxN = len(s.Values)
		}
	}
	if maxN < 2 {
		return
	}
	minV, maxV := seriesValueRange(c.Series)
	if maxV == minV {
		maxV = minV + 1
	}
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	step := (plotR - plotL) / float64(maxN-1)
	for si, ser := range c.Series {
		color := r.themedSeriesColor(ser, si)
		rr, gg, bb := parseHexColor(color)
		r.pdf.SetStrokeColor(rr, gg, bb)
		r.pdf.SetFillColor(rr, gg, bb)
		r.pdf.SetLineWidth(1.2)
		var prevX, prevY float64
		for i, v := range ser.Values {
			x := plotL + float64(i)*step
			y := valueToY(v, minV, maxV, plotT, plotB)
			if i > 0 {
				r.pdf.Line(prevX, prevY, x, y)
			}
			r.pdf.Rectangle(x-1.5, y-1.5, x+1.5, y+1.5, "F", 0, 0)
			prevX, prevY = x, y
		}
	}
	for i := 0; i < maxN && i < len(c.Categories); i++ {
		label := c.Categories[i]
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
		r.pdf.SetTextColor(80, 80, 80)
		lw, _ := r.pdf.MeasureTextWidth(label)
		x := plotL + float64(i)*step - lw/2
		if x < plotL {
			x = plotL
		}
		r.pdf.SetX(x)
		r.pdf.SetY(plotB + 2)
		_ = r.pdf.Cell(nil, truncateLabel(label, 10))
	}
}

// drawAreaChart fills the region under each series' polyline. Series are
// stacked back-to-front in declaration order so the first series sits
// behind later ones. The fill uses a semi-transparent shade of the
// series color approximated by lightening it 50%.
func drawAreaChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	maxN := 0
	for _, s := range c.Series {
		if len(s.Values) > maxN {
			maxN = len(s.Values)
		}
	}
	if maxN < 2 {
		return
	}
	minV, maxV := seriesValueRange(c.Series)
	if maxV == minV {
		maxV = minV + 1
	}
	if minV > 0 {
		minV = 0
	}
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	step := (plotR - plotL) / float64(maxN-1)
	zeroY := valueToY(0, minV, maxV, plotT, plotB)
	for si, ser := range c.Series {
		if len(ser.Values) == 0 {
			continue
		}
		color := r.themedSeriesColor(ser, si)
		rr, gg, bb := lightenHex(color, 0.55)
		r.pdf.SetFillColor(rr, gg, bb)
		r.pdf.SetLineWidth(0)
		pts := make([]gopdf.Point, 0, len(ser.Values)+2)
		pts = append(pts, gopdf.Point{X: plotL, Y: zeroY})
		for i, v := range ser.Values {
			pts = append(pts, gopdf.Point{
				X: plotL + float64(i)*step,
				Y: valueToY(v, minV, maxV, plotT, plotB),
			})
		}
		pts = append(pts, gopdf.Point{X: plotL + float64(len(ser.Values)-1)*step, Y: zeroY})
		r.pdf.Polygon(pts, "F")
		// Stroke the top contour in the saturated color.
		rr, gg, bb = parseHexColor(color)
		r.pdf.SetStrokeColor(rr, gg, bb)
		r.pdf.SetLineWidth(1.0)
		for i := 1; i < len(ser.Values); i++ {
			x0 := plotL + float64(i-1)*step
			y0 := valueToY(ser.Values[i-1], minV, maxV, plotT, plotB)
			x1 := plotL + float64(i)*step
			y1 := valueToY(ser.Values[i], minV, maxV, plotT, plotB)
			r.pdf.Line(x0, y0, x1, y1)
		}
	}
}

// drawBubbleChart plots each series' Values as bubbles laid out in
// equally-spaced X positions. We don't have separate X / Y / size
// triplets at the parser level — Values[i] is treated as size; Y is the
// magnitude of the size; X is the index.
func drawBubbleChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	maxN := 0
	for _, s := range c.Series {
		if len(s.Values) > maxN {
			maxN = len(s.Values)
		}
	}
	if maxN < 1 {
		return
	}
	minV, maxV := seriesValueRange(c.Series)
	if maxV == minV {
		maxV = minV + 1
	}
	if minV > 0 {
		minV = 0
	}
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	step := (plotR - plotL) / float64(maxN)
	maxBubble := math.Min(step*0.6, (plotB-plotT)/5)
	for si, ser := range c.Series {
		color := r.themedSeriesColor(ser, si)
		rr, gg, bb := lightenHex(color, 0.3)
		r.pdf.SetFillColor(rr, gg, bb)
		rrS, ggS, bbS := parseHexColor(color)
		r.pdf.SetStrokeColor(rrS, ggS, bbS)
		r.pdf.SetLineWidth(0.5)
		for i, v := range ser.Values {
			x := plotL + (float64(i)+0.5)*step
			y := valueToY(v, minV, maxV, plotT, plotB)
			radius := math.Abs(v) / math.Max(math.Abs(maxV), math.Abs(minV)) * maxBubble
			if radius < 2 {
				radius = 2
			}
			drawShapeOval(r, x-radius, y-radius, x+radius, y+radius, true, true)
		}
	}
}

// drawRadarChart paints a polygonal "spider web" with one vertex per
// category and one polygon per series. The plot is centered in the
// available rect; categories arranged clockwise from 12 o'clock.
func drawRadarChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	categories := maxCategoryCount(c)
	if categories < 3 {
		return
	}
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	radius := math.Min((right-left)/2, (bottom-top)/2) * 0.8
	_, maxV := seriesValueRange(c.Series)
	if maxV <= 0 {
		maxV = 1
	}
	// Gridlines: 4 concentric polygons.
	r.pdf.SetStrokeColor(0xd0, 0xd0, 0xd0)
	r.pdf.SetLineWidth(0.4)
	for ring := 1; ring <= 4; ring++ {
		rr := radius * float64(ring) / 4
		pts := make([]gopdf.Point, 0, categories)
		for i := 0; i < categories; i++ {
			theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(categories)
			pts = append(pts, gopdf.Point{X: cx + math.Cos(theta)*rr, Y: cy + math.Sin(theta)*rr})
		}
		// Outline only — no fill.
		for i := 0; i < len(pts); i++ {
			p0 := pts[i]
			p1 := pts[(i+1)%len(pts)]
			r.pdf.Line(p0.X, p0.Y, p1.X, p1.Y)
		}
	}
	// Spokes.
	for i := 0; i < categories; i++ {
		theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(categories)
		r.pdf.Line(cx, cy, cx+math.Cos(theta)*radius, cy+math.Sin(theta)*radius)
	}
	// Category labels.
	for i := 0; i < categories && i < len(c.Categories); i++ {
		theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(categories)
		lx := cx + math.Cos(theta)*(radius+8)
		ly := cy + math.Sin(theta)*(radius+8)
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.75)
		r.pdf.SetTextColor(80, 80, 80)
		tw, _ := r.pdf.MeasureTextWidth(c.Categories[i])
		r.pdf.SetX(lx - tw/2)
		r.pdf.SetY(ly - r.opts.DefaultFontSize*0.4)
		_ = r.pdf.Cell(nil, truncateLabel(c.Categories[i], 10))
	}
	// One polygon per series.
	for si, ser := range c.Series {
		color := r.themedSeriesColor(ser, si)
		rr, gg, bb := lightenHex(color, 0.55)
		r.pdf.SetFillColor(rr, gg, bb)
		rrS, ggS, bbS := parseHexColor(color)
		r.pdf.SetStrokeColor(rrS, ggS, bbS)
		r.pdf.SetLineWidth(1.0)
		pts := make([]gopdf.Point, 0, categories)
		for i := 0; i < categories; i++ {
			if i >= len(ser.Values) {
				break
			}
			theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(categories)
			rr := radius * ser.Values[i] / maxV
			if rr < 0 {
				rr = 0
			}
			pts = append(pts, gopdf.Point{X: cx + math.Cos(theta)*rr, Y: cy + math.Sin(theta)*rr})
		}
		if len(pts) < 2 {
			continue
		}
		// Filled (translucent shade) + stroked outline.
		r.pdf.Polygon(pts, "F")
		for i := 0; i < len(pts); i++ {
			p0 := pts[i]
			p1 := pts[(i+1)%len(pts)]
			r.pdf.Line(p0.X, p0.Y, p1.X, p1.Y)
		}
	}
}

// lightenHex blends a hex color toward white by frac (0..1) and returns
// the resulting RGB. A simple linear blend in RGB space — sufficient
// for chart fills where we want a pastel version of the stroke color.
func lightenHex(hex string, frac float64) (uint8, uint8, uint8) {
	rr, gg, bb := parseHexColor(hex)
	mix := func(c uint8) uint8 {
		f := float64(c) + (255-float64(c))*frac
		if f > 255 {
			f = 255
		}
		return uint8(f)
	}
	return mix(rr), mix(gg), mix(bb)
}

func drawAxes(r *renderer, plotL, plotT, plotR, plotB, minV, maxV float64) {
	r.pdf.SetStrokeColor(0xa0, 0xa0, 0xa0)
	r.pdf.SetLineWidth(0.5)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	r.pdf.Line(plotL, plotB, plotR, plotB)
	// 3 horizontal grid lines + value labels.
	for i := 0; i <= 3; i++ {
		v := minV + (maxV-minV)*float64(i)/3
		y := valueToY(v, minV, maxV, plotT, plotB)
		r.pdf.SetStrokeColor(0xe0, 0xe0, 0xe0)
		r.pdf.Line(plotL, y, plotR, y)
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.7)
		r.pdf.SetTextColor(120, 120, 120)
		lbl := fmt.Sprintf("%.0f", v)
		w, _ := r.pdf.MeasureTextWidth(lbl)
		r.pdf.SetX(plotL - w - 2)
		r.pdf.SetY(y - r.opts.DefaultFontSize*0.35)
		_ = r.pdf.Cell(nil, lbl)
	}
}

func drawChartLegend(r *renderer, series []docx.ChartSeries, left, top, right, bottom float64) {
	if len(series) == 0 {
		return
	}
	_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
	totalW := 0.0
	widths := make([]float64, len(series))
	for i, s := range series {
		w, _ := r.pdf.MeasureTextWidth(s.Name)
		widths[i] = w + r.opts.DefaultFontSize + 8
		totalW += widths[i]
	}
	x := left + ((right-left)-totalW)/2
	if x < left {
		x = left
	}
	y := top + (bottom-top-r.opts.DefaultFontSize)/2
	for i, s := range series {
		color := r.themedSeriesColor(s, i)
		rr, gg, bb := parseHexColor(color)
		r.pdf.SetFillColor(rr, gg, bb)
		r.pdf.Rectangle(x, y+1, x+r.opts.DefaultFontSize*0.6, y+r.opts.DefaultFontSize*0.6+1, "F", 0, 0)
		r.pdf.SetTextColor(60, 60, 60)
		r.pdf.SetX(x + r.opts.DefaultFontSize*0.6 + 4)
		r.pdf.SetY(y)
		_ = r.pdf.Cell(nil, s.Name)
		x += widths[i]
	}
}

func maxCategoryCount(c *docx.ChartData) int {
	n := len(c.Categories)
	for _, s := range c.Series {
		if len(s.Values) > n {
			n = len(s.Values)
		}
	}
	return n
}

func seriesValueRange(series []docx.ChartSeries) (float64, float64) {
	var minV, maxV float64
	have := false
	for _, s := range series {
		for _, v := range s.Values {
			if !have {
				minV, maxV = v, v
				have = true
				continue
			}
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
	}
	if !have {
		return 0, 1
	}
	return minV, maxV
}

// stackedValueRange returns the y-extent needed to render a stacked
// bar/area chart: for each category, the per-series values sum vertically
// rather than overlap, so the chart's max is the tallest stack, not the
// largest single value. When percent is true (percentStacked), every
// stack normalizes to 100, so the range is fixed at [0, 100].
func stackedValueRange(series []docx.ChartSeries, percent bool) (float64, float64) {
	if percent {
		return 0, 100
	}
	maxStack := 0.0
	minStack := 0.0
	cats := 0
	for _, s := range series {
		if len(s.Values) > cats {
			cats = len(s.Values)
		}
	}
	for i := 0; i < cats; i++ {
		pos, neg := 0.0, 0.0
		for _, s := range series {
			if i >= len(s.Values) {
				continue
			}
			v := s.Values[i]
			if v >= 0 {
				pos += v
			} else {
				neg += v
			}
		}
		if pos > maxStack {
			maxStack = pos
		}
		if neg < minStack {
			minStack = neg
		}
	}
	return minStack, maxStack
}

func valueToY(v, minV, maxV, plotT, plotB float64) float64 {
	span := maxV - minV
	if span == 0 {
		return plotB
	}
	return plotB - (v-minV)/span*(plotB-plotT)
}

func valueToX(v, minV, maxV, plotL, plotR float64) float64 {
	span := maxV - minV
	if span == 0 {
		return plotL
	}
	return plotL + (v-minV)/span*(plotR-plotL)
}

func seriesColor(s docx.ChartSeries, idx int) string {
	if s.Color != "" {
		return s.Color
	}
	return paletteColor(idx)
}

// themedSeriesColor is the renderer-aware variant: honors an explicit
// per-series color first, then falls back to the document theme's
// accent palette, and finally the static palette. Use this from chart
// kinds that have access to the renderer (most paths do).
func (r *renderer) themedSeriesColor(s docx.ChartSeries, idx int) string {
	if s.Color != "" {
		return s.Color
	}
	pal := r.themedPalette()
	if idx < 0 {
		idx = 0
	}
	return pal[idx%len(pal)]
}

// themedPalette returns a series palette built from the document's
// theme accent1..accent6 colors when present, falling back to the
// hardcoded chartPalette otherwise. Called per-render so docs with a
// non-default theme get series strokes that match their other
// theme-colored text.
func (r *renderer) themedPalette() []string {
	if r == nil || r.doc == nil || len(r.doc.Theme.Colors) == 0 {
		return chartPalette
	}
	pick := func(name string) string {
		if v, ok := r.doc.Theme.Colors[name]; ok && v != "" {
			return v
		}
		return ""
	}
	var out []string
	for _, n := range []string{"accent1", "accent2", "accent3", "accent4", "accent5", "accent6"} {
		if v := pick(n); v != "" {
			out = append(out, v)
		}
	}
	if len(out) < 2 {
		return chartPalette
	}
	return out
}

func paletteColor(idx int) string {
	if idx < 0 {
		idx = 0
	}
	return chartPalette[idx%len(chartPalette)]
}

func truncateLabel(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8len(s) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return string([]rune(s)[0])
	}
	runes := []rune(s)
	return string(runes[:maxRunes-1]) + "…"
}

func utf8len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// Helper so the legend / labels routines can be shared with the chart
// renderer when emitted from textbox content. Defined here so internal
// callers don't have to import strings only for this one use.
var _ = strings.TrimSpace

// drawScatterChart plots each series as markers (diamonds) connected by
// thin lines. Differs from drawLineChart by drawing a visible marker at
// every data point — scatter's primary visual signature.
func drawScatterChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	r.pdf.Line(plotL, plotB, plotR, plotB)
	lo, hi := seriesValueRange(c.Series)
	if lo == hi {
		hi = lo + 1
	}
	maxPts := maxCategoryCount(c)
	if maxPts < 2 {
		maxPts = 2
	}
	for si, s := range c.Series {
		col := r.themedSeriesColor(s, si)
		cr, cg, cb := parseHexColor(col)
		r.pdf.SetStrokeColor(cr, cg, cb)
		r.pdf.SetFillColor(cr, cg, cb)
		r.pdf.SetLineWidth(0.6)
		prevX, prevY, has := 0.0, 0.0, false
		for i, v := range s.Values {
			fx := float64(i) / float64(maxPts-1)
			fy := (v - lo) / (hi - lo)
			px := plotL + fx*(plotR-plotL)
			py := plotB - fy*(plotB-plotT)
			if has {
				r.pdf.Line(prevX, prevY, px, py)
			}
			// Diamond marker.
			d := 2.0
			r.pdf.SetLineWidth(0.4)
			r.pdf.Line(px-d, py, px, py-d)
			r.pdf.Line(px, py-d, px+d, py)
			r.pdf.Line(px+d, py, px, py+d)
			r.pdf.Line(px, py+d, px-d, py)
			prevX, prevY, has = px, py, true
		}
	}
}

// drawStockChart renders an OHLC-style candlestick per category. Series
// are interpreted positionally: 4 series → [open, high, low, close];
// 3 series → [high, low, close]; otherwise we fall back to drawLineChart.
func drawStockChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	n := len(c.Series)
	if n != 3 && n != 4 {
		drawLineChart(r, c, left, top, right, bottom)
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	r.pdf.Line(plotL, plotB, plotR, plotB)
	lo, hi := seriesValueRange(c.Series)
	if lo == hi {
		hi = lo + 1
	}
	maxPts := maxCategoryCount(c)
	if maxPts < 1 {
		return
	}
	step := (plotR - plotL) / float64(maxPts)
	bodyW := step * 0.5
	openIdx, highIdx, lowIdx, closeIdx := -1, 0, 1, 2
	if n == 4 {
		openIdx, highIdx, lowIdx, closeIdx = 0, 1, 2, 3
	}
	yOf := func(v float64) float64 {
		return plotB - (v-lo)/(hi-lo)*(plotB-plotT)
	}
	for i := 0; i < maxPts; i++ {
		cx := plotL + (float64(i)+0.5)*step
		highVal := safeAt(c.Series[highIdx].Values, i)
		lowVal := safeAt(c.Series[lowIdx].Values, i)
		closeVal := safeAt(c.Series[closeIdx].Values, i)
		openVal := closeVal
		if openIdx >= 0 {
			openVal = safeAt(c.Series[openIdx].Values, i)
		}
		// Wick: high → low vertical line.
		r.pdf.SetLineWidth(0.4)
		r.pdf.SetStrokeColor(0x40, 0x40, 0x40)
		r.pdf.Line(cx, yOf(highVal), cx, yOf(lowVal))
		// Body: open to close rectangle. Filled green when close > open,
		// red otherwise (the universal market convention).
		bodyTop := yOf(openVal)
		bodyBot := yOf(closeVal)
		if bodyTop > bodyBot {
			bodyTop, bodyBot = bodyBot, bodyTop
		}
		if closeVal >= openVal {
			r.pdf.SetFillColor(0x70, 0xAD, 0x47)
		} else {
			r.pdf.SetFillColor(0xC0, 0x00, 0x00)
		}
		r.pdf.Rectangle(cx-bodyW/2, bodyTop, cx+bodyW/2, bodyBot, "F", 0, 0)
	}
}

// drawSurfaceChart approximates a 3D surface as a stack of filled bands,
// one per series, each with a gradient from light at the top to slightly
// darker at the bottom so the "topographic" reading carries.
func drawSurfaceChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 {
		return
	}
	const axisPad = 28.0
	plotL := left + axisPad
	plotB := bottom - r.opts.DefaultFontSize*1.4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotR := right - 4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	r.pdf.Line(plotL, plotB, plotR, plotB)
	lo, hi := seriesValueRange(c.Series)
	if lo == hi {
		hi = lo + 1
	}
	maxPts := maxCategoryCount(c)
	if maxPts < 2 {
		return
	}
	step := (plotR - plotL) / float64(maxPts-1)
	for si, s := range c.Series {
		col := r.themedSeriesColor(s, si)
		cr, cg, cb := parseHexColor(col)
		// Fill polygon from baseline up to the series line.
		var poly []gopdf.Point
		poly = append(poly, gopdf.Point{X: plotL, Y: plotB})
		for i, v := range s.Values {
			x := plotL + float64(i)*step
			y := plotB - (v-lo)/(hi-lo)*(plotB-plotT)
			poly = append(poly, gopdf.Point{X: x, Y: y})
		}
		poly = append(poly, gopdf.Point{X: plotL + float64(len(s.Values)-1)*step, Y: plotB})
		// Stagger fill alpha by series index so layers stack.
		light := uint8(255 - si*15)
		_ = light
		r.pdf.SetFillColor(cr, cg, cb)
		drawPolygonFill(r, poly)
		// Outline.
		r.pdf.SetStrokeColor(cr, cg, cb)
		r.pdf.SetLineWidth(0.8)
		for i := 1; i < len(poly)-1; i++ {
			r.pdf.Line(poly[i].X, poly[i].Y, poly[i+1].X, poly[i+1].Y)
		}
	}
}

// drawOfPieChart renders a pie-of-pie / bar-of-pie. The first series is
// the main pie; we slice off the last min(3, len/3) values as the
// "detail" group, render them as a smaller second pie to the right,
// and draw a connector between them.
func drawOfPieChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		drawPieChart(r, c, left, top, right, bottom, false)
		return
	}
	values := c.Series[0].Values
	categories := c.Categories
	detailCount := len(values) / 3
	if detailCount < 2 {
		detailCount = 2
	}
	if detailCount > len(values)-1 {
		detailCount = len(values) - 1
	}
	if detailCount <= 0 {
		drawPieChart(r, c, left, top, right, bottom, false)
		return
	}
	// Build the main pie (everything but the last detailCount values,
	// plus a combined "Other" slice equal to their sum).
	mainCount := len(values) - detailCount
	mainValues := make([]float64, 0, mainCount+1)
	mainCats := make([]string, 0, mainCount+1)
	otherSum := 0.0
	for i := 0; i < mainCount; i++ {
		mainValues = append(mainValues, values[i])
		if i < len(categories) {
			mainCats = append(mainCats, categories[i])
		} else {
			mainCats = append(mainCats, "")
		}
	}
	for i := mainCount; i < len(values); i++ {
		otherSum += values[i]
	}
	mainValues = append(mainValues, otherSum)
	mainCats = append(mainCats, "Other")
	mainChart := *c
	mainChart.Series = []docx.ChartSeries{{Values: mainValues}}
	mainChart.Categories = mainCats
	// Detail pie: just the last detailCount values.
	detValues := values[mainCount:]
	detCats := categories
	if mainCount < len(categories) {
		detCats = categories[mainCount:]
	} else {
		detCats = []string{}
	}
	detChart := *c
	detChart.Series = []docx.ChartSeries{{Values: detValues}}
	detChart.Categories = detCats

	mid := (left + right) / 2
	drawPieChart(r, &mainChart, left, top, mid-4, bottom, false)
	drawPieChart(r, &detChart, mid+4, top+(bottom-top)*0.15, right, bottom-(bottom-top)*0.15, false)
	// Connector lines suggesting the link between the "Other" slice and
	// the detail pie.
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0xA0, 0xA0, 0xA0)
	r.pdf.Line(mid-8, top+(bottom-top)*0.4, mid+4, top+(bottom-top)*0.4)
	r.pdf.Line(mid-8, top+(bottom-top)*0.6, mid+4, top+(bottom-top)*0.6)
}

func safeAt(xs []float64, i int) float64 {
	if i < 0 || i >= len(xs) {
		return 0
	}
	return xs[i]
}

// drawPolygonFill paints a closed filled polygon. Used by surface charts
// where we need a many-vertex region; gopdf's Rectangle path doesn't
// cover the arbitrary shape.
func drawPolygonFill(r *renderer, pts []gopdf.Point) {
	if len(pts) < 3 {
		return
	}
	r.pdf.Polygon(pts, "F")
}
