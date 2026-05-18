package render

import "github.com/bobyeoh/docx2pdf-go/internal/docx"

// columnRect is one column's horizontal placement: starting X and body
// width in points. Used by unequal-width multi-column sections.
type columnRect struct {
	x float64
	w float64
}

// drawColumnSeparators paints a thin vertical rule between adjacent
// columns. Called once per page at section start; the renderer's
// per-page header path also calls it when a new page is fired inside a
// section that has separators enabled.
func drawColumnSeparators(r *renderer, sec docx.Section) {
	if !sec.ColumnSeparator || sec.Columns < 2 {
		return
	}
	topY := r.marT
	bottomY := r.pageH - r.marB
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	if len(r.colSpecs) == int(r.numColumns) {
		for i := 0; i < int(r.numColumns)-1; i++ {
			a := r.colSpecs[i]
			b := r.colSpecs[i+1]
			x := (a.x + a.w + b.x) / 2
			r.pdf.Line(x, topY, x, bottomY)
		}
		return
	}
	for i := 1; i < int(r.numColumns); i++ {
		x := r.colBaseX + float64(i)*(r.colW+r.colGap) - r.colGap/2
		r.pdf.Line(x, topY, x, bottomY)
	}
}
