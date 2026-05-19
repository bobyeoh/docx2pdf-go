package render

import (
	"math"
	"strconv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

// setFill / setStroke take a 6-hex color, parse it, and apply to gopdf.
// Tiny local helpers so the chartEx renderers don't repeat the
// parseHexColor/SetFillColor dance at every call site.
func setFill(r *renderer, hex string) {
	rr, gg, bb := parseHexColor(hex)
	r.pdf.SetFillColor(rr, gg, bb)
}

func setStroke(r *renderer, hex string, weight float64) {
	rr, gg, bb := parseHexColor(hex)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.SetLineWidth(weight)
}

// chartex.go renders the chartEx families introduced by Office 2016:
// waterfall, treemap, sunburst, funnel. Each takes a ChartData whose
// Series[0] carries the numeric values and Categories the labels.
//
// These are 2D approximations: chartEx in Word also supports rich
// styling (gradient fills, custom marker shapes) we don't yet emulate.

// drawWaterfallChart paints a vertical waterfall. Each non-subtotal
// data point is a delta (positive/negative); subtotal indexes reset the
// running total and render as a solid full-height column from zero.
// Positive deltas use green (#5B9BD5 → matching Word's default
// 'positive' fill), negatives use red (#ED7D31), subtotals use gray.
func drawWaterfallChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		return
	}
	vals := c.Series[0].Values
	cats := c.Categories
	subtotal := make(map[int]bool, len(c.WaterfallSubtotals))
	for _, i := range c.WaterfallSubtotals {
		subtotal[i] = true
	}
	const axisPad = 32.0
	plotL := left + axisPad
	plotR := right - 4
	plotT := top + r.opts.DefaultFontSize*0.5
	plotB := bottom - r.opts.DefaultFontSize*1.4
	if plotR-plotL < 20 || plotB-plotT < 20 {
		return
	}

	// First pass: compute the cumulative running total + min/max so the
	// y-scale envelopes the full waterfall.
	type bar struct {
		from, to float64
		kind     byte // 'p' positive, 'n' negative, 's' subtotal
		label    string
	}
	bars := make([]bar, 0, len(vals))
	running := 0.0
	minV, maxV := 0.0, 0.0
	for i, v := range vals {
		b := bar{}
		switch {
		case subtotal[i]:
			b.from = 0
			b.to = running + v
			running = b.to
			b.kind = 's'
		case v >= 0:
			b.from = running
			b.to = running + v
			running = b.to
			b.kind = 'p'
		default:
			b.from = running + v
			b.to = running
			running = b.from
			b.kind = 'n'
		}
		if i < len(cats) {
			b.label = cats[i]
		}
		bars = append(bars, b)
		if b.from < minV {
			minV = b.from
		}
		if b.to > maxV {
			maxV = b.to
		}
	}
	if maxV-minV < 1e-9 {
		return
	}

	// Y-axis line + zero baseline.
	yScale := (plotB - plotT) / (maxV - minV)
	yAt := func(v float64) float64 {
		return plotB - (v-minV)*yScale
	}
	setStroke(r, "808080", 0.5)
	r.pdf.Line(plotL, plotT, plotL, plotB)
	r.pdf.Line(plotL, yAt(0), plotR, yAt(0))

	// Bars.
	bw := (plotR - plotL) / float64(len(bars))
	gap := bw * 0.15
	for i, b := range bars {
		x0 := plotL + float64(i)*bw + gap/2
		x1 := plotL + float64(i+1)*bw - gap/2
		y0 := yAt(b.to)
		y1 := yAt(b.from)
		if y0 > y1 {
			y0, y1 = y1, y0
		}
		fill := "5B9BD5"
		switch b.kind {
		case 'p':
			fill = "70AD47"
		case 'n':
			fill = "C00000"
		case 's':
			fill = "808080"
		}
		setFill(r, fill)
		r.pdf.RectFromUpperLeftWithStyle(x0, y0, x1-x0, y1-y0, "F")
		// Category label below.
		if b.label != "" {
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
			r.pdf.SetTextColor(0, 0, 0)
			lw, _ := r.pdf.MeasureTextWidth(b.label)
			r.pdf.SetX(x0 + (x1-x0-lw)/2)
			r.pdf.SetY(plotB + 2)
			_ = r.pdf.Cell(nil, b.label)
		}
	}
}

// drawTreemapChart paints a flat treemap using the simple "slice and
// dice" algorithm: alternate horizontal/vertical splits, area
// proportional to value. Word's treemap supports hierarchy via
// cx:strDim with multiple levels; we honor only the first (flat) level
// here, which covers the most common single-tier case.
func drawTreemapChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		return
	}
	vals := c.Series[0].Values
	cats := c.Categories
	total := 0.0
	for _, v := range vals {
		if v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return
	}
	palette := []string{"5B9BD5", "ED7D31", "70AD47", "FFC000", "264478", "9E480E", "636363", "997300"}
	// Treemap-flat: divide horizontally; each rect's width is value/total.
	x := left
	for i, v := range vals {
		if v <= 0 {
			continue
		}
		w := (right - left) * v / total
		setFill(r, palette[i%len(palette)])
		r.pdf.RectFromUpperLeftWithStyle(x, top, w, bottom-top, "F")
		setStroke(r, "FFFFFF", 0.8)
		r.pdf.RectFromUpperLeftWithStyle(x, top, w, bottom-top, "D")
		// Label inside the rect.
		label := ""
		if i < len(cats) {
			label = cats[i]
		}
		if label != "" && w > 20 {
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.85)
			r.pdf.SetTextColor(255, 255, 255)
			r.pdf.SetX(x + 3)
			r.pdf.SetY(top + 3)
			_ = r.pdf.Cell(nil, label)
			r.pdf.SetY(top + 3 + r.opts.DefaultFontSize)
			r.pdf.SetX(x + 3)
			_ = r.pdf.Cell(nil, strconv.FormatFloat(v, 'f', -1, 64))
		}
		x += w
	}
}

// drawSunburstChart paints a single-ring sunburst — values arrayed as
// pie slices around a central hole. Word's sunburst supports nested
// rings for hierarchy levels; we render only the outer ring, which is
// the leaf level and what the eye reads as the data.
func drawSunburstChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		return
	}
	vals := c.Series[0].Values
	cats := c.Categories
	total := 0.0
	for _, v := range vals {
		if v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return
	}
	cx := (left + right) / 2
	cy := (top + bottom) / 2
	rOuter := math.Min(right-left, bottom-top) / 2
	if rOuter <= 0 {
		return
	}
	rInner := rOuter * 0.35
	palette := []string{"5B9BD5", "ED7D31", "70AD47", "FFC000", "264478", "9E480E", "636363", "997300"}
	angle := -math.Pi / 2
	for i, v := range vals {
		if v <= 0 {
			continue
		}
		sweep := 2 * math.Pi * v / total
		fillSunburstSlice(r, cx, cy, rInner, rOuter, angle, angle+sweep, palette[i%len(palette)])
		// Label outside the ring.
		mid := angle + sweep/2
		if i < len(cats) {
			tx := cx + math.Cos(mid)*(rOuter+8)
			ty := cy + math.Sin(mid)*(rOuter+8)
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.8)
			r.pdf.SetTextColor(0, 0, 0)
			lw, _ := r.pdf.MeasureTextWidth(cats[i])
			r.pdf.SetX(tx - lw/2)
			r.pdf.SetY(ty - r.opts.DefaultFontSize/2)
			_ = r.pdf.Cell(nil, cats[i])
		}
		angle += sweep
	}
}

// fillSunburstSlice paints one annular slice between rInner and rOuter
// from angle a0 to a1 (radians). The slice is approximated as a fan of
// thin trapezoids; gopdf doesn't expose arc fills so this is the
// simplest accurate approach.
func fillSunburstSlice(r *renderer, cx, cy, rInner, rOuter, a0, a1 float64, hex string) {
	setFill(r, hex)
	const steps = 24
	da := (a1 - a0) / steps
	for i := 0; i < steps; i++ {
		t0 := a0 + da*float64(i)
		t1 := t0 + da
		x0a, y0a := cx+math.Cos(t0)*rInner, cy+math.Sin(t0)*rInner
		x1a, y1a := cx+math.Cos(t1)*rInner, cy+math.Sin(t1)*rInner
		x0b, y0b := cx+math.Cos(t0)*rOuter, cy+math.Sin(t0)*rOuter
		x1b, y1b := cx+math.Cos(t1)*rOuter, cy+math.Sin(t1)*rOuter
		// gopdf has no polygon fill primitive; draw a quad as two
		// triangles using a 4-point bezier path via Polygon().
		r.pdf.Polygon([]gopdf.Point{
			{X: x0a, Y: y0a},
			{X: x0b, Y: y0b},
			{X: x1b, Y: y1b},
			{X: x1a, Y: y1a},
		}, "F")
	}
}

// drawFunnelChart stacks horizontal centered bars whose widths shrink
// proportionally to their values. Reads top-to-bottom like Word.
func drawFunnelChart(r *renderer, c *docx.ChartData, left, top, right, bottom float64) {
	if len(c.Series) == 0 || len(c.Series[0].Values) == 0 {
		return
	}
	vals := c.Series[0].Values
	cats := c.Categories
	maxV := 0.0
	for _, v := range vals {
		if v > maxV {
			maxV = v
		}
	}
	if maxV <= 0 {
		return
	}
	rowH := (bottom - top) / float64(len(vals))
	mid := (left + right) / 2
	for i, v := range vals {
		w := (right - left) * v / maxV
		y0 := top + float64(i)*rowH + 2
		y1 := top + float64(i+1)*rowH - 2
		setFill(r, "5B9BD5")
		r.pdf.RectFromUpperLeftWithStyle(mid-w/2, y0, w, y1-y0, "F")
		if i < len(cats) && cats[i] != "" {
			_ = r.pdf.SetFont(defaultFamily, "", r.opts.DefaultFontSize*0.85)
			r.pdf.SetTextColor(255, 255, 255)
			lw, _ := r.pdf.MeasureTextWidth(cats[i])
			r.pdf.SetX(mid - lw/2)
			r.pdf.SetY(y0 + (y1-y0-r.opts.DefaultFontSize)/2)
			_ = r.pdf.Cell(nil, cats[i])
		}
	}
}
