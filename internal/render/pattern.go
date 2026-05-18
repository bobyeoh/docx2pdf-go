package render

import (
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// drawPatternTile paints a DrawingML preset pattern (a:pattFill@prst)
// inside the given rectangle. Patterns are approximated by repeating
// stripes / dot grids of the foreground color over a background
// rectangle.
//
// Only the most common presets are supported with their authentic look;
// uncovered presets fall back to a 50% horizontal-line hatch which reads
// as "some texture" to the eye.
func drawPatternTile(r *renderer, p *docx.PatternFill, left, top, right, bottom float64) {
	if p == nil {
		return
	}
	fg := p.FgHex
	if fg == "" {
		fg = "000000"
	}
	bg := p.BgHex
	// Paint the background first so the preset's fg lines layer on top.
	if bg != "" {
		br, bgC, bbC := parseHexColor(bg)
		r.pdf.SetFillColor(br, bgC, bbC)
		r.pdf.Rectangle(left, top, right, bottom, "F", 0, 0)
	}
	fr, fg2, fb := parseHexColor(fg)
	r.pdf.SetStrokeColor(fr, fg2, fb)
	r.pdf.SetFillColor(fr, fg2, fb)

	w := right - left
	h := bottom - top
	preset := strings.ToLower(p.Preset)

	switch {
	case strings.HasPrefix(preset, "pct"):
		// pct5 / pct10 / pct20 / pct25 / pct30 / pct40 / pct50 / pct60 /
		// pct70 / pct75 / pct80 / pct90 — render as a dot grid whose density
		// matches the preset. We use 8 dot rows; the percentage drives how
		// many of them are drawn.
		pct := patternPctValue(preset)
		drawDotPattern(r, left, top, w, h, pct)
		return
	case preset == "horz", preset == "ltHorz":
		drawHorzLines(r, left, top, w, h, 3)
		return
	case preset == "dkhorz":
		drawHorzLines(r, left, top, w, h, 1.5)
		return
	case preset == "vert", preset == "ltvert":
		drawVertLines(r, left, top, w, h, 3)
		return
	case preset == "dkvert":
		drawVertLines(r, left, top, w, h, 1.5)
		return
	case preset == "updiag", preset == "ltupdiag":
		drawDiagLines(r, left, top, w, h, 3, true)
		return
	case preset == "dkupdiag":
		drawDiagLines(r, left, top, w, h, 1.5, true)
		return
	case preset == "dndiag", preset == "ltdndiag":
		drawDiagLines(r, left, top, w, h, 3, false)
		return
	case preset == "dkdndiag":
		drawDiagLines(r, left, top, w, h, 1.5, false)
		return
	case preset == "cross":
		drawHorzLines(r, left, top, w, h, 4)
		drawVertLines(r, left, top, w, h, 4)
		return
	case preset == "diagcross":
		drawDiagLines(r, left, top, w, h, 4, true)
		drawDiagLines(r, left, top, w, h, 4, false)
		return
	case preset == "horzbrick":
		drawBrickPattern(r, left, top, w, h, false)
		return
	case preset == "diagbrick":
		drawBrickPattern(r, left, top, w, h, true)
		return
	}
	// Fallback: medium hatch.
	drawHorzLines(r, left, top, w, h, 3)
}

// patternPctValue extracts the percentage from a "pctNN" preset.
func patternPctValue(preset string) float64 {
	v := strings.TrimPrefix(preset, "pct")
	switch v {
	case "5":
		return 5
	case "10":
		return 10
	case "20":
		return 20
	case "25":
		return 25
	case "30":
		return 30
	case "40":
		return 40
	case "50":
		return 50
	case "60":
		return 60
	case "70":
		return 70
	case "75":
		return 75
	case "80":
		return 80
	case "90":
		return 90
	}
	return 50
}

// drawHorzLines paints evenly-spaced horizontal lines inside (left, top,
// w, h). spacingPt is the line-to-line gap.
func drawHorzLines(r *renderer, left, top, w, h, spacingPt float64) {
	r.pdf.SetLineWidth(0.5)
	for y := top + spacingPt/2; y < top+h; y += spacingPt {
		r.pdf.Line(left, y, left+w, y)
	}
}

func drawVertLines(r *renderer, left, top, w, h, spacingPt float64) {
	r.pdf.SetLineWidth(0.5)
	for x := left + spacingPt/2; x < left+w; x += spacingPt {
		r.pdf.Line(x, top, x, top+h)
	}
}

// drawDiagLines paints diagonal lines. ascending=true means lines go from
// bottom-left to top-right; false means top-left to bottom-right.
func drawDiagLines(r *renderer, left, top, w, h, spacingPt float64, ascending bool) {
	r.pdf.SetLineWidth(0.5)
	// Step diagonally by sqrt(2)*spacing so the visual spacing matches
	// the horizontal/vertical case at the same parameter.
	diag := spacingPt * 1.414
	for off := -h; off < w+h; off += diag {
		if ascending {
			r.pdf.Line(left+off, top+h, left+off+h, top)
		} else {
			r.pdf.Line(left+off, top, left+off+h, top+h)
		}
	}
}

// drawDotPattern fills the rect with a 6×6-cell dot grid whose dot count
// is scaled by the requested percentage.
func drawDotPattern(r *renderer, left, top, w, h, pct float64) {
	if pct <= 0 {
		return
	}
	// Adaptive cell size: 6pt cells for low density, 3pt for high density.
	cell := 4.0
	if pct >= 50 {
		cell = 2.5
	}
	dot := cell * 0.4
	skip := 1
	if pct < 50 {
		// Skip cells to lower density: pct=25 → every-other-cell.
		skip = int(50.0 / pct)
		if skip < 1 {
			skip = 1
		}
	}
	count := 0
	for y := top + cell/2; y < top+h; y += cell {
		for x := left + cell/2; x < left+w; x += cell {
			if count%skip == 0 {
				r.pdf.Rectangle(x-dot/2, y-dot/2, x+dot/2, y+dot/2, "F", 0, 0)
			}
			count++
		}
	}
}

// drawBrickPattern paints a brick layout with horizontal courses (8pt
// tall) where alternate rows are offset by half a brick. When diag is
// true the rows tilt by 30°.
func drawBrickPattern(r *renderer, left, top, w, h float64, diag bool) {
	const brickW = 12.0
	const brickH = 6.0
	r.pdf.SetLineWidth(0.5)
	for y := top; y < top+h; y += brickH {
		r.pdf.Line(left, y, left+w, y)
	}
	row := 0
	for y := top; y < top+h; y += brickH {
		offset := 0.0
		if row%2 == 1 {
			offset = brickW / 2
		}
		for x := left + offset; x < left+w; x += brickW {
			r.pdf.Line(x, y, x, y+brickH)
		}
		row++
	}
	_ = diag
}
