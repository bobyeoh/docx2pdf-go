package render

import (
	"math"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// fixedRenderer is a minimal renderer with just enough state for
// resolveColumnWidths.
func fixedRenderer() *renderer {
	return &renderer{
		opts:     Options{DefaultFontSize: 11},
		contentW: 400,
		marL:     0,
	}
}

func TestResolveColumnWidths_FixedHonorsTblGrid(t *testing.T) {
	r := fixedRenderer()
	tbl := docx.Table{
		Layout:            "fixed",
		ColumnWidthsTwips: []int{2000, 4000, 2000}, // 100pt, 200pt, 100pt
	}
	w := r.resolveColumnWidths(tbl, 3)
	want := []float64{100, 200, 100}
	for i, x := range want {
		if math.Abs(w[i]-x) > 0.01 {
			t.Errorf("col %d = %v, want %v", i, w[i], x)
		}
	}
}

// In fixed mode, the table is allowed to overflow contentW — Word will
// render the columns at their declared widths and let the right edge
// stick out past the margin. Autofit would scale them down; fixed must
// not.
func TestResolveColumnWidths_FixedDoesNotShrinkOnOverflow(t *testing.T) {
	r := fixedRenderer() // contentW = 400
	tbl := docx.Table{
		Layout:            "fixed",
		ColumnWidthsTwips: []int{12000, 12000}, // 600pt each, sum 1200pt — way past 400pt
	}
	w := r.resolveColumnWidths(tbl, 2)
	if math.Abs(w[0]-600) > 0.01 || math.Abs(w[1]-600) > 0.01 {
		t.Errorf("fixed-mode column widths must not shrink: got %v", w)
	}
}

// In autofit (the default), the same overflow case scales columns to
// match contentW. This pins the contrast with the fixed path.
func TestResolveColumnWidths_AutofitShrinksOverflow(t *testing.T) {
	r := fixedRenderer()
	tbl := docx.Table{
		Layout:            "", // autofit (Word default)
		ColumnWidthsTwips: []int{12000, 12000},
	}
	w := r.resolveColumnWidths(tbl, 2)
	if math.Abs((w[0]+w[1])-400) > 0.5 {
		t.Errorf("autofit should clamp sum to contentW=400, got sum=%v widths=%v", w[0]+w[1], w)
	}
}

// tblW pct STILL applies in fixed mode — Word scales the columns to
// match the requested table-level percentage.
func TestResolveColumnWidths_FixedHonorsTblWPct(t *testing.T) {
	r := fixedRenderer() // contentW=400
	tbl := docx.Table{
		Layout:            "fixed",
		ColumnWidthsTwips: []int{2000, 2000}, // 100pt each, sum 200pt
		TableWidthType:    "pct",
		TableWidthTwips:   2500, // 50% → target 200pt, already matches
	}
	w := r.resolveColumnWidths(tbl, 2)
	if math.Abs((w[0]+w[1])-200) > 0.5 {
		t.Errorf("fixed+pct sum = %v, want 200", w[0]+w[1])
	}
}
