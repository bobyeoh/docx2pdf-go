package render

import (
	"fmt"
	"math"
	"strconv"
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
	// Stash the current chart so colorpicking helpers (themedSeriesColor,
	// drawChartLegend, …) can consult the chart's own palette without
	// every callsite needing an extra parameter.
	prev := r.curChart
	r.curChart = c
	defer func() { r.curChart = prev }()
	// Reserve space for the title at the top + a strip for the legend
	// (orientation per c:legendPos). Both shrink the plot area, not the
	// bounding rect.
	titleH := 0.0
	if c.Title != "" {
		titleH = r.opts.DefaultFontSize * 1.4
	}
	wantLegend := len(c.Series) > 0 && hasSeriesNames(c.Series) && !c.LegendDeleted
	legendPos := c.LegendPos
	if !wantLegend {
		legendPos = ""
	}
	legendStripV := 0.0
	legendStripH := 0.0
	switch legendPos {
	case "t":
		legendStripV = r.opts.DefaultFontSize * 1.4
	case "l", "r", "tr":
		// Side legend: reserve ~25% width or measured width.
		legendStripH = math.Min((right-left)*0.25, measureLegendWidth(r, c.Series)+12)
	default:
		if wantLegend {
			legendStripV = r.opts.DefaultFontSize * 1.4
		}
	}
	plotTop := top + titleH
	plotBottom := bottom
	plotLeft := left
	plotRight := right
	switch legendPos {
	case "t":
		plotTop += legendStripV
	case "l":
		plotLeft += legendStripH
	case "r", "tr":
		plotRight -= legendStripH
	default:
		if wantLegend {
			plotBottom -= legendStripV
		}
	}
	if plotBottom-plotTop < 20 || plotRight-plotLeft < 20 {
		plotTop = top + titleH
		plotBottom = bottom
		plotLeft = left
		plotRight = right
	}
	if c.Title != "" {
		titleSize := r.opts.DefaultFontSize
		if c.StyleSummary.TitleFontSizePt > 0 {
			titleSize = c.StyleSummary.TitleFontSizePt
		}
		_ = r.pdf.SetFont(defaultFamily, "", titleSize)
		r.pdf.SetTextColor(0, 0, 0)
		w, _ := r.pdf.MeasureTextWidth(c.Title)
		r.pdf.SetX(left + ((right-left)-w)/2)
		r.pdf.SetY(top + 2)
		_ = r.pdf.Cell(nil, c.Title)
		_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize)
	}
	switch c.Kind {
	case "column":
		drawColumnChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "bar":
		drawBarChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "pie", "doughnut":
		drawPieChart(r, c, plotLeft, plotTop, plotRight, plotBottom, c.Kind == "doughnut")
	case "line":
		drawLineChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "scatter":
		drawScatterChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "area":
		drawAreaChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "bubble":
		drawBubbleChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "radar":
		drawRadarChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "stock":
		drawStockChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "surface":
		drawSurfaceChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "ofPie":
		drawOfPieChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "waterfall":
		drawWaterfallChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "treemap":
		drawTreemapChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "sunburst":
		drawSunburstChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "funnel":
		drawFunnelChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	case "histogram":
		// chartEx histogram presents binned data — fall back to the
		// column path which already understands category + value.
		drawColumnChart(r, c, plotLeft, plotTop, plotRight, plotBottom)
	}
	if wantLegend {
		switch legendPos {
		case "t":
			drawChartLegend(r, c.Series, left, top+titleH, right, top+titleH+legendStripV, false)
		case "l":
			drawChartLegend(r, c.Series, left, plotTop, left+legendStripH, plotBottom, true)
		case "r", "tr":
			drawChartLegend(r, c.Series, right-legendStripH, plotTop, right, plotBottom, true)
		default:
			drawChartLegend(r, c.Series, left, plotBottom, right, bottom, false)
		}
	}
}

// measureLegendWidth estimates the width required for a vertically
// stacked legend with one row per series. Used to reserve a side strip
// when c:legendPos is "l", "r", or "tr".
func measureLegendWidth(r *renderer, series []docx.ChartSeries) float64 {
	_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
	maxW := 0.0
	for _, s := range series {
		w, _ := r.pdf.MeasureTextWidth(s.Name)
		if w > maxW {
			maxW = w
		}
	}
	return maxW + r.opts.DefaultFontSize + 6
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
	minV, maxV := effectiveValueRange(c)
	if stacked {
		// In stacked mode each category's height is the sum of its series
		// values, not the max of any one series. Recompute the y-range so
		// the tallest stack fits.
		minV, maxV = stackedValueRange(c.Series, percent)
		if c.HasValMin {
			minV = c.ValMin
		}
		if c.HasValMax {
			maxV = c.ValMax
		}
	}
	if maxV == minV {
		maxV = minV + 1
	}
	if minV > 0 && !c.HasValMin {
		minV = 0
	}
	if maxV < 0 && !c.HasValMax {
		maxV = 0
	}
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	catW := (plotR - plotL) / float64(maxCats)
	// gapWidth (in % of bar width) drives the inter-category gap; in
	// the stacked layout we keep a wider gap so neighbors don't blur.
	gapPct := c.GapWidthPct
	if gapPct <= 0 {
		gapPct = 150
	}
	// Map gapPct to fractional padding inside the category cell.
	innerFrac := float64(gapPct) / (float64(gapPct) + 100)
	if innerFrac < 0.05 {
		innerFrac = 0.05
	}
	if innerFrac > 0.85 {
		innerFrac = 0.85
	}
	innerPad := catW * innerFrac / 2
	groupW := catW - 2*innerPad
	if groupW < 1 {
		groupW = 1
	}
	barW := groupW
	overlap := c.OverlapPct
	if !stacked && len(c.Series) > 1 {
		// Overlap default for clustered bars in Word: 0 → bars touch;
		// negative widens the gap between cluster siblings; positive
		// makes them overlap. We translate into a per-bar pitch.
		nseries := float64(len(c.Series))
		gap := -float64(overlap) / 100.0
		if gap < -0.95 {
			gap = -0.95
		}
		if gap > 0.95 {
			gap = 0.95
		}
		// pitch = barW / (n + gap*(n-1)) keeps overlapping bars sharing
		// pixels but never collapses to zero.
		denom := nseries + gap*(nseries-1)
		if denom < 0.5 {
			denom = 0.5
		}
		barW = groupW / denom
	}
	// Single-series + VaryColors picks one palette color per data point.
	varyColors := c.VaryColors && len(c.Series) == 1
	palette := r.themedPalette()
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
				lbl := composeDataLabel(effectiveDataLabels(c, ser), ser.Name, categoryAt(c, cat), v, 0)
				if lbl != "" {
					drawValueLabel(r, lbl, (x0+x1)/2, (y0+y1)/2+r.opts.DefaultFontSize*0.3, false)
				}
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
				if varyColors {
					color = palette[cat%len(palette)]
				}
				rr, gg, bb := parseHexColor(color)
				r.pdf.SetFillColor(rr, gg, bb)
				r.pdf.SetStrokeColor(rr, gg, bb)
				r.pdf.SetLineWidth(0)
				r.pdf.Rectangle(x0, y0, x1, y1, "F", 0, 0)
				lbl := composeDataLabel(effectiveDataLabels(c, ser), ser.Name, categoryAt(c, cat), v, 0)
				if lbl != "" {
					dirUp := v >= 0
					anchorY := y0
					if !dirUp {
						anchorY = y1
					}
					drawValueLabel(r, lbl, (x0+x1)/2, anchorY, dirUp)
				}
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
	// Series-level overlays — trendline + error bars. Positioned at the
	// category-center x; bars cover their cluster mid.
	if !stacked {
		for si, ser := range c.Series {
			xs := make([]float64, len(ser.Values))
			for i := range ser.Values {
				xs[i] = plotL + float64(i)*catW + innerPad + float64(si)*barW + barW*0.45
			}
			drawErrorBars(r, ser, xs, plotT, plotB, minV, maxV)
			drawTrendline(r, ser, plotL, plotT, plotR, plotB, minV, maxV)
		}
	}
}

// categoryAt returns c.Categories[i] or empty.
func categoryAt(c *docx.ChartData, i int) string {
	if i >= 0 && i < len(c.Categories) {
		return c.Categories[i]
	}
	return ""
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
	palette := r.themedPalette()
	opts := c.DataLabels
	if c.Series[0].DataLabels != nil {
		opts = *c.Series[0].DataLabels
	}
	for i, v := range values {
		if v <= 0 {
			continue
		}
		frac := v / total
		end := startAngle + frac*2*math.Pi
		color := palette[i%len(palette)]
		drawPieSlice(r, cx, cy, radius, startAngle, end, color)
		// Label inside the slice: prefer composed label if any Show*
		// flag is set; fall back to bare percentage.
		midAngle := (startAngle + end) / 2
		lx := cx + math.Cos(midAngle)*radius*0.65
		ly := cy + math.Sin(midAngle)*radius*0.65
		var label string
		if opts.ShowVal || opts.ShowCatName || opts.ShowSerName || opts.ShowPercent {
			label = composeDataLabel(opts, c.Series[0].Name, categoryAt(c, i), v, frac)
		} else {
			label = fmt.Sprintf("%.0f%%", frac*100)
		}
		if label != "" {
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.85)
			r.pdf.SetTextColor(255, 255, 255)
			tw, _ := r.pdf.MeasureTextWidth(label)
			r.pdf.SetX(lx - tw/2)
			r.pdf.SetY(ly - r.opts.DefaultFontSize*0.4)
			_ = r.pdf.Cell(nil, label)
		}
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
	minV, maxV := effectiveValueRange(c)
	drawAxes(r, plotL, plotT, plotR, plotB, minV, maxV)
	step := (plotR - plotL) / float64(maxN-1)
	drawChartHiLowDrops(r, c, plotL, plotT, plotB, step, minV, maxV)
	for si, ser := range c.Series {
		color := r.themedSeriesColor(ser, si)
		rr, gg, bb := parseHexColor(color)
		r.pdf.SetStrokeColor(rr, gg, bb)
		r.pdf.SetFillColor(rr, gg, bb)
		r.pdf.SetLineWidth(1.2)
		// Secondary-axis series get a dashed stroke so the reader can
		// disambiguate them from primary-axis lines. The simplified
		// renderer reuses the primary value range, so the visual cue is
		// our only signal that "this series doesn't share scale".
		if ser.Secondary {
			r.pdf.SetLineWidth(1.0)
		}
		// Build (x, y) points up front so we can interpolate, draw
		// markers, and apply DispBlanksAs uniformly.
		xs := make([]float64, len(ser.Values))
		ys := make([]float64, len(ser.Values))
		valid := make([]bool, len(ser.Values))
		for i, v := range ser.Values {
			xs[i] = plotL + float64(i)*step
			if math.IsNaN(v) {
				switch c.DispBlanksAs {
				case "zero":
					ys[i] = valueToY(0, minV, maxV, plotT, plotB)
					valid[i] = true
				case "span":
					// Skip: caller will connect across the gap.
					valid[i] = false
				default: // gap (default)
					valid[i] = false
				}
				continue
			}
			ys[i] = valueToY(v, minV, maxV, plotT, plotB)
			valid[i] = true
		}
		smooth := ser.Smooth
		if smooth {
			drawSmoothPolyline(r, xs, ys, valid, c.DispBlanksAs == "span")
		} else {
			drawSegmentedPolyline(r, xs, ys, valid, c.DispBlanksAs == "span")
		}
		// Markers (unless explicitly suppressed by c:symbol val="none").
		if ser.MarkerSymbol != "none" {
			for i := range ser.Values {
				if !valid[i] {
					continue
				}
				drawMarker(r, ser.MarkerSymbol, xs[i], ys[i], color)
			}
		}
		// Per-point data labels.
		opts := effectiveDataLabels(c, ser)
		if opts.ShowVal || opts.ShowCatName || opts.ShowSerName {
			for i, v := range ser.Values {
				if !valid[i] {
					continue
				}
				lbl := composeDataLabel(opts, ser.Name, categoryAt(c, i), v, 0)
				drawValueLabel(r, lbl, xs[i], ys[i], true)
			}
		}
		drawErrorBars(r, ser, xs, plotT, plotB, minV, maxV)
		drawTrendline(r, ser, plotL, plotT, plotR, plotB, minV, maxV)
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

// drawSegmentedPolyline strokes between consecutive valid points. When
// span is true, gaps (invalid entries) are bridged directly from the
// last-valid point to the next-valid one.
func drawSegmentedPolyline(r *renderer, xs, ys []float64, valid []bool, span bool) {
	lastIdx := -1
	for i := range xs {
		if !valid[i] {
			continue
		}
		if lastIdx >= 0 {
			if span || lastIdx == i-1 {
				r.pdf.Line(xs[lastIdx], ys[lastIdx], xs[i], ys[i])
			}
		}
		lastIdx = i
	}
}

// drawSmoothPolyline approximates a Catmull-Rom spline by subdividing
// each segment into 8 micro-steps and drawing chords. Endpoints are
// reflected so the curve passes through every valid point.
func drawSmoothPolyline(r *renderer, xs, ys []float64, valid []bool, span bool) {
	pts := make([][2]float64, 0, len(xs))
	for i := range xs {
		if !valid[i] {
			if !span {
				// Flush current segment, render, then reset.
				renderCatmullRom(r, pts)
				pts = pts[:0]
				continue
			}
			continue
		}
		pts = append(pts, [2]float64{xs[i], ys[i]})
	}
	renderCatmullRom(r, pts)
}

func renderCatmullRom(r *renderer, pts [][2]float64) {
	if len(pts) < 2 {
		return
	}
	if len(pts) == 2 {
		r.pdf.Line(pts[0][0], pts[0][1], pts[1][0], pts[1][1])
		return
	}
	get := func(i int) [2]float64 {
		if i < 0 {
			return pts[0]
		}
		if i >= len(pts) {
			return pts[len(pts)-1]
		}
		return pts[i]
	}
	const steps = 8
	for i := 0; i < len(pts)-1; i++ {
		p0 := get(i - 1)
		p1 := get(i)
		p2 := get(i + 1)
		p3 := get(i + 2)
		prevX, prevY := p1[0], p1[1]
		for s := 1; s <= steps; s++ {
			t := float64(s) / steps
			t2 := t * t
			t3 := t2 * t
			x := 0.5 * ((2 * p1[0]) +
				(-p0[0]+p2[0])*t +
				(2*p0[0]-5*p1[0]+4*p2[0]-p3[0])*t2 +
				(-p0[0]+3*p1[0]-3*p2[0]+p3[0])*t3)
			y := 0.5 * ((2 * p1[1]) +
				(-p0[1]+p2[1])*t +
				(2*p0[1]-5*p1[1]+4*p2[1]-p3[1])*t2 +
				(-p0[1]+3*p1[1]-3*p2[1]+p3[1])*t3)
			r.pdf.Line(prevX, prevY, x, y)
			prevX, prevY = x, y
		}
	}
}

// drawMarker renders one of the chart marker glyphs at (x, y).
func drawMarker(r *renderer, kind string, x, y float64, color string) {
	rr, gg, bb := parseHexColor(color)
	r.pdf.SetFillColor(rr, gg, bb)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.SetLineWidth(0.6)
	const s = 1.6
	switch kind {
	case "circle", "auto", "":
		drawShapeOval(r, x-s, y-s, x+s, y+s, true, false)
	case "square":
		r.pdf.Rectangle(x-s, y-s, x+s, y+s, "F", 0, 0)
	case "diamond":
		pts := []gopdf.Point{{X: x, Y: y - s}, {X: x + s, Y: y}, {X: x, Y: y + s}, {X: x - s, Y: y}}
		r.pdf.Polygon(pts, "F")
	case "triangle":
		pts := []gopdf.Point{{X: x, Y: y - s}, {X: x + s, Y: y + s}, {X: x - s, Y: y + s}}
		r.pdf.Polygon(pts, "F")
	case "x":
		r.pdf.Line(x-s, y-s, x+s, y+s)
		r.pdf.Line(x-s, y+s, x+s, y-s)
	case "plus":
		r.pdf.Line(x-s, y, x+s, y)
		r.pdf.Line(x, y-s, x, y+s)
	case "star":
		r.pdf.Line(x-s, y-s, x+s, y+s)
		r.pdf.Line(x-s, y+s, x+s, y-s)
		r.pdf.Line(x-s, y, x+s, y)
		r.pdf.Line(x, y-s, x, y+s)
	case "dash":
		r.pdf.Rectangle(x-s, y-0.4, x+s, y+0.4, "F", 0, 0)
	case "dot":
		drawShapeOval(r, x-0.8, y-0.8, x+0.8, y+0.8, true, false)
	default:
		drawShapeOval(r, x-s, y-s, x+s, y+s, true, false)
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
	// Secondary axis: draw a parallel right-side rule with the same
	// 3-step tick labels (sharing the primary value range, since the
	// simplified renderer doesn't track a second scale).
	if r != nil && r.curChart != nil && r.curChart.HasSecondaryAxis {
		r.pdf.SetStrokeColor(0xa0, 0xa0, 0xa0)
		r.pdf.SetLineWidth(0.5)
		r.pdf.Line(plotR, plotT, plotR, plotB)
		for i := 0; i <= 3; i++ {
			v := minV + (maxV-minV)*float64(i)/3
			y := valueToY(v, minV, maxV, plotT, plotB)
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.7)
			r.pdf.SetTextColor(120, 120, 120)
			lbl := fmt.Sprintf("%.0f", v)
			r.pdf.SetX(plotR + 2)
			r.pdf.SetY(y - r.opts.DefaultFontSize*0.35)
			_ = r.pdf.Cell(nil, lbl)
		}
	}
}

func drawChartLegend(r *renderer, series []docx.ChartSeries, left, top, right, bottom float64, vertical bool) {
	if len(series) == 0 {
		return
	}
	_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
	if vertical {
		lineH := r.opts.DefaultFontSize + 4
		totalH := lineH * float64(len(series))
		y := top + ((bottom-top)-totalH)/2
		if y < top {
			y = top
		}
		for i, s := range series {
			color := r.themedSeriesColor(s, i)
			rr, gg, bb := parseHexColor(color)
			r.pdf.SetFillColor(rr, gg, bb)
			r.pdf.Rectangle(left, y+1, left+r.opts.DefaultFontSize*0.6, y+r.opts.DefaultFontSize*0.6+1, "F", 0, 0)
			r.pdf.SetTextColor(60, 60, 60)
			r.pdf.SetX(left + r.opts.DefaultFontSize*0.6 + 4)
			r.pdf.SetY(y)
			_ = r.pdf.Cell(nil, s.Name)
			y += lineH
		}
		return
	}
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

// effectiveValueRange wraps seriesValueRange but honors c:scaling
// overrides (ValMin / ValMax) when present.
func effectiveValueRange(c *docx.ChartData) (float64, float64) {
	minV, maxV := seriesValueRange(c.Series)
	if c.HasValMin {
		minV = c.ValMin
	}
	if c.HasValMax {
		maxV = c.ValMax
	}
	if maxV == minV {
		maxV = minV + 1
	}
	return minV, maxV
}

// drawValueLabel renders a small numeric / category / series label at
// (x, y), anchored centered horizontally. dirUp lifts the baseline up
// for above-bar placement; otherwise the label sits below y.
func drawValueLabel(r *renderer, label string, x, y float64, dirUp bool) {
	if label == "" {
		return
	}
	_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.7)
	r.pdf.SetTextColor(60, 60, 60)
	w, _ := r.pdf.MeasureTextWidth(label)
	tx := x - w/2
	ty := y
	if dirUp {
		ty = y - r.opts.DefaultFontSize*0.9
	} else {
		ty = y + 1
	}
	r.pdf.SetX(tx)
	r.pdf.SetY(ty)
	_ = r.pdf.Cell(nil, label)
}

// composeDataLabel concatenates the pieces of a c:dLbls record into a
// single short string. order: ser • cat • val (• pct).
func composeDataLabel(opts docx.DataLabelOptions, serName, catName string, val float64, pctOfTotal float64) string {
	if !opts.ShowVal && !opts.ShowCatName && !opts.ShowSerName && !opts.ShowPercent {
		return ""
	}
	parts := make([]string, 0, 4)
	if opts.ShowSerName && serName != "" {
		parts = append(parts, serName)
	}
	if opts.ShowCatName && catName != "" {
		parts = append(parts, catName)
	}
	if opts.ShowVal {
		parts = append(parts, formatChartValue(val))
	}
	if opts.ShowPercent {
		parts = append(parts, fmt.Sprintf("%.0f%%", pctOfTotal*100))
	}
	return strings.Join(parts, " ")
}

// formatChartValue picks a compact representation for a chart value.
// Whole-number values print without a decimal; fractional values get
// up to two decimals; very large numbers use scientific notation.
func formatChartValue(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := math.Abs(v)
	if abs >= 1e6 {
		return fmt.Sprintf("%.2e", v)
	}
	if v == math.Trunc(v) && abs < 1e6 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// effectiveDataLabels returns the labels applied to a single series:
// the per-series override wins; otherwise the chart-level default
// applies.
func effectiveDataLabels(c *docx.ChartData, ser docx.ChartSeries) docx.DataLabelOptions {
	if ser.DataLabels != nil {
		return *ser.DataLabels
	}
	return c.DataLabels
}

// drawChartHiLowDrops paints the chart-level c:hiLowLines, c:dropLines,
// and c:upDownBars decorations onto a line/area/stock chart at category
// step `step`. hiLowLines connect max/min across all series per category;
// dropLines drop a vertical rule from each point to the value-axis
// baseline; upDownBars draw a thin bar between the first and last
// series' values per category (green when up, red when down).
func drawChartHiLowDrops(r *renderer, c *docx.ChartData, plotL, plotT, plotB, step, minV, maxV float64) {
	if !c.HasHiLowLines && !c.HasDropLines && !c.HasUpDownBars {
		return
	}
	maxN := 0
	for _, s := range c.Series {
		if len(s.Values) > maxN {
			maxN = len(s.Values)
		}
	}
	if maxN == 0 {
		return
	}
	baselineY := valueToY(0, minV, maxV, plotT, plotB)
	for i := 0; i < maxN; i++ {
		x := plotL + float64(i)*step
		var hi, lo float64
		hasAny := false
		for _, ser := range c.Series {
			if i >= len(ser.Values) {
				continue
			}
			v := ser.Values[i]
			if math.IsNaN(v) {
				continue
			}
			if !hasAny {
				hi, lo = v, v
				hasAny = true
				continue
			}
			if v > hi {
				hi = v
			}
			if v < lo {
				lo = v
			}
		}
		if !hasAny {
			continue
		}
		hiY := valueToY(hi, minV, maxV, plotT, plotB)
		loY := valueToY(lo, minV, maxV, plotT, plotB)
		if c.HasHiLowLines {
			r.pdf.SetStrokeColor(80, 80, 80)
			r.pdf.SetLineWidth(0.6)
			r.pdf.Line(x, hiY, x, loY)
		}
		if c.HasDropLines {
			r.pdf.SetStrokeColor(150, 150, 150)
			r.pdf.SetLineWidth(0.4)
			r.pdf.Line(x, loY, x, baselineY)
		}
		if c.HasUpDownBars && len(c.Series) >= 2 {
			first := c.Series[0]
			last := c.Series[len(c.Series)-1]
			if i < len(first.Values) && i < len(last.Values) &&
				!math.IsNaN(first.Values[i]) && !math.IsNaN(last.Values[i]) {
				fy := valueToY(first.Values[i], minV, maxV, plotT, plotB)
				ly := valueToY(last.Values[i], minV, maxV, plotT, plotB)
				up := last.Values[i] >= first.Values[i]
				if up {
					r.pdf.SetFillColor(70, 160, 90)
				} else {
					r.pdf.SetFillColor(190, 70, 70)
				}
				bw := step * 0.3
				if bw > 8 {
					bw = 8
				}
				y0, y1 := fy, ly
				if y0 > y1 {
					y0, y1 = y1, y0
				}
				r.pdf.Rectangle(x-bw/2, y0, x+bw/2, y1, "F", 0, 0)
			}
		}
	}
}

// drawTrendline overlays a fitted curve on top of a column / line /
// scatter series. minV/maxV are the y-extent already used to plot
// series values. For simplicity all kinds collapse to linear LSRL
// except movingAvg which renders a running mean.
func drawTrendline(r *renderer, ser docx.ChartSeries, plotL, plotT, plotR, plotB, minV, maxV float64) {
	if ser.Trendline == nil || len(ser.Values) < 2 {
		return
	}
	tl := ser.Trendline
	n := len(ser.Values)
	step := (plotR - plotL) / float64(n-1)
	color := tl.Color
	if color == "" {
		color = "606060"
	}
	rr, gg, bb := parseHexColor(color)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.SetLineWidth(0.8)
	if tl.Kind == "movingAvg" {
		period := tl.Order
		if period < 2 {
			period = 2
		}
		var prevX, prevY float64
		drew := false
		for i := period - 1; i < n; i++ {
			s := 0.0
			for k := 0; k < period; k++ {
				s += ser.Values[i-k]
			}
			avg := s / float64(period)
			x := plotL + float64(i)*step
			y := valueToY(avg, minV, maxV, plotT, plotB)
			if drew {
				r.pdf.Line(prevX, prevY, x, y)
			}
			prevX, prevY = x, y
			drew = true
		}
		return
	}
	// Linear LSRL fit: y = m*x + b across index/value pairs.
	var sumX, sumY, sumXY, sumX2 float64
	for i, v := range ser.Values {
		x := float64(i)
		sumX += x
		sumY += v
		sumXY += x * v
		sumX2 += x * x
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if denom == 0 {
		return
	}
	m := (float64(n)*sumXY - sumX*sumY) / denom
	b := (sumY - m*sumX) / float64(n)
	y0 := valueToY(b, minV, maxV, plotT, plotB)
	y1 := valueToY(m*float64(n-1)+b, minV, maxV, plotT, plotB)
	r.pdf.Line(plotL, y0, plotL+float64(n-1)*step, y1)
}

// drawErrorBars draws Y-direction whiskers on a series. For percent /
// stat types we approximate the magnitude as a fraction of the value
// range so the bars stay visible even without explicit data.
func drawErrorBars(r *renderer, ser docx.ChartSeries, xs []float64, plotT, plotB, minV, maxV float64) {
	if ser.ErrBars == nil || ser.ErrBars.Direction == "x" {
		return
	}
	eb := ser.ErrBars
	r.pdf.SetStrokeColor(80, 80, 80)
	r.pdf.SetLineWidth(0.5)
	for i, v := range ser.Values {
		if i >= len(xs) {
			break
		}
		mag := eb.Value
		switch eb.ErrValType {
		case "percentage":
			mag = math.Abs(v) * eb.Value / 100
		case "stdDev", "stdErr", "cust":
			if mag == 0 {
				mag = (maxV - minV) * 0.05
			}
		}
		if mag <= 0 {
			continue
		}
		yHi := valueToY(v+mag, minV, maxV, plotT, plotB)
		yLo := valueToY(v-mag, minV, maxV, plotT, plotB)
		r.pdf.Line(xs[i], yHi, xs[i], yLo)
		const cap = 3
		r.pdf.Line(xs[i]-cap, yHi, xs[i]+cap, yHi)
		r.pdf.Line(xs[i]-cap, yLo, xs[i]+cap, yLo)
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
// per-series color first, then the current chart's cs:colorStyle
// palette, then falls back to the document theme's accent palette, and
// finally the static palette.
func (r *renderer) themedSeriesColor(s docx.ChartSeries, idx int) string {
	if s.Color != "" {
		return s.Color
	}
	var pal []string
	if r != nil && r.curChart != nil {
		pal = r.chartPaletteFor(*r.curChart)
	} else {
		pal = r.themedPalette()
	}
	if len(pal) == 0 {
		return paletteColor(idx)
	}
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

// chartPaletteFor honors the chart's own cs:colorStyle palette when
// present, then theme accents, then the static fallback. "scheme:accentN"
// placeholders are resolved through the document theme; anything still
// unresolved is dropped so the caller doesn't paint a string.
func (r *renderer) chartPaletteFor(c docx.ChartData) []string {
	if len(c.Palette) == 0 {
		return r.themedPalette()
	}
	var resolved []string
	for _, p := range c.Palette {
		if strings.HasPrefix(p, "scheme:") {
			name := strings.TrimPrefix(p, "scheme:")
			if r != nil && r.doc != nil {
				if v, ok := r.doc.Theme.Colors[name]; ok && v != "" {
					resolved = append(resolved, v)
					continue
				}
			}
			continue
		}
		resolved = append(resolved, p)
	}
	if len(resolved) >= 2 {
		return resolved
	}
	return r.themedPalette()
}

// chartSeriesColor picks the right palette entry for one series taking
// the chart's optional cs:colorStyle palette into account.
func (r *renderer) chartSeriesColor(c docx.ChartData, s docx.ChartSeries, idx int) string {
	if s.Color != "" {
		return s.Color
	}
	pal := r.chartPaletteFor(c)
	if len(pal) == 0 {
		return paletteColor(idx)
	}
	if idx < 0 {
		idx = 0
	}
	return pal[idx%len(pal)]
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
