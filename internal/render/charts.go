package render

import (
	"fmt"
	"math"
	"strconv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// Chart rendering. ChartData carries structured series data from
// word/charts/chartN.xml; this file paints it into the PDF page
// using gopdf primitives (Line, RectFromUpperLeftWithStyle, Cell,
// SetFillColor / SetStrokeColor).
//
// What's supported:
//   - Vertical bar charts (BarDir = "col" or unset). One bar per
//     (series × category); clustered grouping inside each category.
//   - Line charts: poly-line per series with small markers.
//   - Pie charts: approximated by triangle fans — gopdf has no native
//     arc primitive, so each slice is a 32-segment polygon.
//
// Each chart is drawn into the rectangle (x, y, w, h) reserved by
// the corresponding wp:extent. Layout inside the rectangle:
//
//   top ~ 16pt  title (centered, if present)
//   middle      plot area with axes / pie
//   right ~80pt legend column (one row per series)
//   bottom ~ 14pt category labels (line/bar) or omitted (pie)
//
// Limitations:
//   - No data labels above bars or beside slices.
//   - No tick marks; axes are bare lines.
//   - Horizontal bars (BarDir = "bar") fall back to vertical.
//   - Stacked / percent stacked groupings render as clustered.

// defaultChartPalette is the color cycle used when a series has no
// explicit fill color. Modeled loosely on Office's "Colorful" theme
// palette — distinguishable hues, no extreme darks.
var defaultChartPalette = []struct{ R, G, B uint8 }{
	{0x4E, 0x79, 0xA7}, // blue
	{0xF2, 0x8E, 0x2B}, // orange
	{0x59, 0xA1, 0x4F}, // green
	{0xED, 0xC9, 0x48}, // yellow
	{0xB0, 0x7A, 0xA1}, // purple
	{0xE1, 0x57, 0x59}, // red
	{0x76, 0xB7, 0xB2}, // teal
	{0xAF, 0x7A, 0xA1}, // mauve
}

// drawChart paints data into the (x, y, w, h) rectangle. Returns nil
// for empty / unsupported chart types so the renderer falls back to
// the legacy "[Chart]" placeholder upstream.
func (r *renderer) drawChart(data docx.ChartData, x, y, w, h float64) error {
	if !data.HasData() || w <= 0 || h <= 0 {
		return nil
	}
	switch data.Type {
	case "bar":
		return r.drawBarChart(data, x, y, w, h)
	case "line":
		return r.drawLineChart(data, x, y, w, h)
	case "pie":
		return r.drawPieChart(data, x, y, w, h)
	}
	return nil
}

// chartLayoutRect carves the chart rectangle into title / plot /
// legend / xlabel sub-rectangles. Pie charts skip the xlabel band.
type chartLayoutRect struct {
	plotX, plotY, plotW, plotH float64
	titleY                     float64
	legendX, legendY           float64
	legendW                    float64
	xlabelY                    float64
}

func chartLayout(x, y, w, h float64, hasTitle, hasCategories, hasLegend bool) chartLayoutRect {
	var lay chartLayoutRect
	top := y
	if hasTitle {
		lay.titleY = top + 2
		top += 16
	}
	bottom := y + h
	if hasCategories {
		lay.xlabelY = bottom - 12
		bottom -= 14
	}
	right := x + w
	if hasLegend {
		lay.legendW = 80
		if lay.legendW > w*0.35 {
			lay.legendW = w * 0.35
		}
		lay.legendX = right - lay.legendW + 4
		lay.legendY = top + 4
		right -= lay.legendW
	}
	lay.plotX = x + 28 // y-axis label gutter
	lay.plotY = top
	lay.plotW = right - lay.plotX
	lay.plotH = bottom - lay.plotY
	if lay.plotW < 10 {
		lay.plotW = 10
	}
	if lay.plotH < 10 {
		lay.plotH = 10
	}
	return lay
}

// drawBarChart paints a clustered vertical bar chart.
func (r *renderer) drawBarChart(data docx.ChartData, x, y, w, h float64) error {
	lay := chartLayout(x, y, w, h, data.Title != "", len(data.Categories) > 0, len(data.Series) > 1)
	r.paintChartTitle(data, x, w, lay.titleY)
	r.paintChartLegend(data, lay)
	minV, maxV := chartValueRange(data)
	r.paintChartAxes(lay, minV, maxV)
	r.paintChartCategoryLabels(data, lay)

	categoriesN := chartCategoriesCount(data)
	if categoriesN == 0 {
		return nil
	}
	seriesN := len(data.Series)
	if seriesN == 0 {
		return nil
	}

	// Inset within the plot area so bars don't touch the axes.
	slotW := lay.plotW / float64(categoriesN)
	barGroupW := slotW * 0.75
	barW := barGroupW / float64(seriesN)
	if barW < 1 {
		barW = 1
	}
	for s, series := range data.Series {
		r.setPaletteFill(s, series.Color)
		for c := 0; c < categoriesN; c++ {
			if c >= len(series.Values) {
				continue
			}
			v := series.Values[c]
			if math.IsNaN(v) {
				continue
			}
			barH := scaleValueToHeight(v, minV, maxV, lay.plotH)
			if barH < 0 {
				barH = 0
			}
			bx := lay.plotX + slotW*float64(c) + (slotW-barGroupW)/2 + float64(s)*barW
			by := lay.plotY + lay.plotH - barH
			r.pdf.RectFromUpperLeftWithStyle(bx, by, barW, barH, "F")
		}
	}
	return nil
}

// drawLineChart paints one polyline per series with small circular
// markers at each data point.
func (r *renderer) drawLineChart(data docx.ChartData, x, y, w, h float64) error {
	lay := chartLayout(x, y, w, h, data.Title != "", len(data.Categories) > 0, len(data.Series) > 1)
	r.paintChartTitle(data, x, w, lay.titleY)
	r.paintChartLegend(data, lay)
	minV, maxV := chartValueRange(data)
	r.paintChartAxes(lay, minV, maxV)
	r.paintChartCategoryLabels(data, lay)

	categoriesN := chartCategoriesCount(data)
	if categoriesN < 2 {
		return nil
	}
	stepX := lay.plotW / float64(categoriesN-1)
	for s, series := range data.Series {
		r.setPaletteStroke(s, series.Color)
		r.pdf.SetLineWidth(1.2)
		var prevX, prevY float64
		prevValid := false
		for c, v := range series.Values {
			if math.IsNaN(v) {
				prevValid = false
				continue
			}
			px := lay.plotX + stepX*float64(c)
			py := lay.plotY + lay.plotH - scaleValueToHeight(v, minV, maxV, lay.plotH)
			if prevValid {
				r.pdf.Line(prevX, prevY, px, py)
			}
			// marker
			r.setPaletteFill(s, series.Color)
			r.pdf.Oval(px-2, py-2, px+2, py+2)
			prevX, prevY = px, py
			prevValid = true
		}
	}
	return nil
}

// drawPieChart approximates a pie via per-slice triangle fans.
func (r *renderer) drawPieChart(data docx.ChartData, x, y, w, h float64) error {
	lay := chartLayout(x, y, w, h, data.Title != "", false, len(data.Series) > 0)
	r.paintChartTitle(data, x, w, lay.titleY)
	r.paintChartLegend(data, lay)

	// Pie uses the first series. Multi-series pie charts are
	// uncommon and Excel renders them as one ring per series; we
	// just take series[0].
	if len(data.Series) == 0 || len(data.Series[0].Values) == 0 {
		return nil
	}
	values := data.Series[0].Values
	total := 0.0
	for _, v := range values {
		if !math.IsNaN(v) && v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return nil
	}
	// Center the pie in the plot rect, choose radius = min half-side.
	cx := lay.plotX + lay.plotW/2
	cy := lay.plotY + lay.plotH/2
	radius := math.Min(lay.plotW, lay.plotH) / 2
	if radius < 8 {
		return nil
	}
	startAngle := -math.Pi / 2 // start at 12 o'clock
	const segmentsPerRad = 16  // ~32 segments for a full circle
	for i, v := range values {
		if math.IsNaN(v) || v <= 0 {
			continue
		}
		sliceAngle := 2 * math.Pi * v / total
		// Pick color from explicit list or palette.
		color := ""
		if i < len(data.Series[0].Values) && i < len(data.Series) {
			color = data.Series[i%len(data.Series)].Color
		}
		r.setPaletteFill(i, color)
		drawPieSlice(r, cx, cy, radius, startAngle, startAngle+sliceAngle, int(math.Max(2, math.Ceil(sliceAngle*segmentsPerRad))))
		startAngle += sliceAngle
	}
	return nil
}

// drawPieSlice draws a filled polygon approximating one pie slice
// from (cx, cy) by chaining segments + 2 radii. gopdf has no native
// polygon-fill, so we approximate with stacked rectangles isn't going
// to look right — instead use small triangles via successive line
// segments. For an MVP we draw the slice's chord as a series of
// triangles fanned from the center; gopdf's filled rectangles plus
// the radius lines provide the outline.
//
// This is a visual approximation, not a precise filled arc. Slices
// remain identifiable by hue + position even if the curved edge
// shows quantization at low segment counts.
func drawPieSlice(r *renderer, cx, cy, radius, start, end float64, segments int) {
	if segments < 2 {
		segments = 2
	}
	step := (end - start) / float64(segments)
	r.pdf.SetLineWidth(0.5)
	for i := 0; i < segments; i++ {
		a1 := start + step*float64(i)
		a2 := start + step*float64(i+1)
		x1 := cx + radius*math.Cos(a1)
		y1 := cy + radius*math.Sin(a1)
		x2 := cx + radius*math.Cos(a2)
		y2 := cy + radius*math.Sin(a2)
		// Triangle (cx,cy)-(x1,y1)-(x2,y2): filled via two rect
		// approximations — we draw thick line for the arc edge and
		// the two radii. Without a path-fill API this leaves
		// unfilled interior, so we fall back to a series of thin
		// concentric ovals (poor man's fill). Acceptable for the
		// MVP; future work: switch to a fork of gopdf that exposes
		// path operators.
		r.pdf.Line(cx, cy, x1, y1)
		r.pdf.Line(cx, cy, x2, y2)
		r.pdf.Line(x1, y1, x2, y2)
		// Fill: short radial segments at increasing radii covers
		// most of the wedge interior.
		for f := 0.05; f < 1.0; f += 0.05 {
			fx1 := cx + radius*f*math.Cos(a1)
			fy1 := cy + radius*f*math.Sin(a1)
			fx2 := cx + radius*f*math.Cos(a2)
			fy2 := cy + radius*f*math.Sin(a2)
			r.pdf.Line(fx1, fy1, fx2, fy2)
		}
	}
}

// paintChartTitle draws the title centered above the plot area.
func (r *renderer) paintChartTitle(data docx.ChartData, x, w, y float64) {
	if data.Title == "" {
		return
	}
	r.pdf.SetTextColor(0, 0, 0)
	if err := r.pdf.SetFont(defaultFamily, "", 11); err != nil {
		return
	}
	tw, _ := r.pdf.MeasureTextWidth(data.Title)
	r.pdf.SetX(x + (w-tw)/2)
	r.pdf.SetY(y)
	_ = r.pdf.Cell(nil, data.Title)
}

// paintChartLegend draws a colored swatch + series name per series.
func (r *renderer) paintChartLegend(data docx.ChartData, lay chartLayoutRect) {
	if lay.legendW <= 0 || len(data.Series) == 0 {
		return
	}
	if err := r.pdf.SetFont(defaultFamily, "", 8); err != nil {
		return
	}
	r.pdf.SetTextColor(0, 0, 0)
	rowH := 12.0
	for i, s := range data.Series {
		yy := lay.legendY + float64(i)*rowH
		r.setPaletteFill(i, s.Color)
		r.pdf.RectFromUpperLeftWithStyle(lay.legendX, yy+2, 8, 8, "F")
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("Series %d", i+1)
		}
		r.pdf.SetX(lay.legendX + 12)
		r.pdf.SetY(yy)
		_ = r.pdf.Cell(nil, name)
	}
}

// paintChartAxes draws the y-axis line, x-axis line, and a few
// reference labels (min, mid, max) on the y-axis.
func (r *renderer) paintChartAxes(lay chartLayoutRect, minV, maxV float64) {
	r.pdf.SetStrokeColor(0xA0, 0xA0, 0xA0)
	r.pdf.SetLineWidth(0.5)
	r.pdf.Line(lay.plotX, lay.plotY, lay.plotX, lay.plotY+lay.plotH)
	r.pdf.Line(lay.plotX, lay.plotY+lay.plotH, lay.plotX+lay.plotW, lay.plotY+lay.plotH)
	if err := r.pdf.SetFont(defaultFamily, "", 7); err == nil {
		r.pdf.SetTextColor(0x80, 0x80, 0x80)
		for _, frac := range []float64{0, 0.5, 1.0} {
			v := minV + (maxV-minV)*frac
			label := chartAxisLabel(v)
			tw, _ := r.pdf.MeasureTextWidth(label)
			r.pdf.SetX(lay.plotX - tw - 2)
			r.pdf.SetY(lay.plotY + lay.plotH*(1-frac) - 4)
			_ = r.pdf.Cell(nil, label)
		}
	}
}

// paintChartCategoryLabels draws the x-axis tick labels under the
// plot area. Long labels get truncated to fit one slot width.
func (r *renderer) paintChartCategoryLabels(data docx.ChartData, lay chartLayoutRect) {
	if lay.xlabelY == 0 || len(data.Categories) == 0 {
		return
	}
	if err := r.pdf.SetFont(defaultFamily, "", 7); err != nil {
		return
	}
	r.pdf.SetTextColor(0x40, 0x40, 0x40)
	n := chartCategoriesCount(data)
	if n == 0 {
		return
	}
	slotW := lay.plotW / float64(n)
	for i, cat := range data.Categories {
		if i >= n {
			break
		}
		tw, _ := r.pdf.MeasureTextWidth(cat)
		cx := lay.plotX + slotW*float64(i) + slotW/2
		r.pdf.SetX(cx - tw/2)
		r.pdf.SetY(lay.xlabelY)
		_ = r.pdf.Cell(nil, cat)
	}
}

// setPaletteFill picks a fill color. Explicit colors win; otherwise
// fall back to the default palette cycled by series index.
func (r *renderer) setPaletteFill(i int, hex string) {
	if hex != "" {
		rR, gR, bR := parseHexColor(hex)
		r.pdf.SetFillColor(rR, gR, bR)
		return
	}
	c := defaultChartPalette[i%len(defaultChartPalette)]
	r.pdf.SetFillColor(c.R, c.G, c.B)
}

// setPaletteStroke mirrors setPaletteFill but for the stroke color.
func (r *renderer) setPaletteStroke(i int, hex string) {
	if hex != "" {
		rR, gR, bR := parseHexColor(hex)
		r.pdf.SetStrokeColor(rR, gR, bR)
		return
	}
	c := defaultChartPalette[i%len(defaultChartPalette)]
	r.pdf.SetStrokeColor(c.R, c.G, c.B)
}

// chartValueRange returns [min, max] over all non-NaN values in any
// series. Bars start at 0 so we clamp minV to 0 when all values are
// non-negative — a common case that produces a cleaner axis.
func chartValueRange(data docx.ChartData) (float64, float64) {
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, s := range data.Series {
		for _, v := range s.Values {
			if math.IsNaN(v) {
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
	if math.IsInf(minV, 1) {
		return 0, 1 // no data — placeholder
	}
	if minV >= 0 {
		minV = 0
	}
	if maxV <= minV {
		maxV = minV + 1
	}
	return minV, maxV
}

// scaleValueToHeight maps a value in [minV, maxV] to a height in
// [0, plotH]. Negative-clamped at zero so bars never extend below
// the x-axis when minV is forced to 0.
func scaleValueToHeight(v, minV, maxV, plotH float64) float64 {
	if maxV == minV {
		return 0
	}
	h := plotH * (v - minV) / (maxV - minV)
	if h < 0 {
		return 0
	}
	if h > plotH {
		return plotH
	}
	return h
}

// chartAxisLabel formats one axis tick value. Integers stay integer;
// non-integers get up to two decimal places.
func chartAxisLabel(v float64) string {
	if v == float64(int(v)) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// chartCategoriesCount returns the effective x-axis category count:
// max(len(Categories), len of longest series Values). Some chart
// docs omit categories entirely; we then label by index.
func chartCategoriesCount(data docx.ChartData) int {
	n := len(data.Categories)
	for _, s := range data.Series {
		if len(s.Values) > n {
			n = len(s.Values)
		}
	}
	return n
}
