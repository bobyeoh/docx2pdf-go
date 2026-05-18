package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestRowHeight_AutoIgnoresHint validates the new w:trHeight hRule="auto"
// semantics: when explicitly auto, the declared HeightTwips is a HINT
// that content overrides — Word ignores it once the row's content
// settles. (The old code treated this identically to atLeast, which is
// why long content always overflowed pages.)
func TestRowHeight_AutoIgnoresHint(t *testing.T) {
	row := docx.TableRow{
		HeightTwips: 2000,
		HeightRule:  "auto",
		Cells: []docx.TableCell{
			{GridSpan: 1, Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "x"}}},
			}},
		},
	}
	r := makeTestRenderer(t)
	got := r.predictRowHeight(row, []float64{200})
	if got >= 50 {
		t.Errorf("hRule=auto should not honor HeightTwips=2000; got rowH=%v", got)
	}
}

// TestRowHeight_AtLeastWithRuleString validates the new HeightRule string
// path: setting HeightRule="atLeast" (rather than only the bool) honors
// HeightTwips as a minimum.
func TestRowHeight_AtLeastWithRuleString(t *testing.T) {
	row := docx.TableRow{
		HeightTwips: 2000,
		HeightRule:  "atLeast",
		Cells: []docx.TableCell{
			{GridSpan: 1, Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "x"}}},
			}},
		},
	}
	r := makeTestRenderer(t)
	got := r.predictRowHeight(row, []float64{200})
	if got != 100 {
		t.Errorf("hRule=atLeast min = %v want 100 (2000/20)", got)
	}
}

// TestFitTextScale_Shrinks confirms computeFitTextScale returns a ratio
// less than 1 when content is wider than the column.
func TestFitTextScale_Shrinks(t *testing.T) {
	r := makeTestRenderer(t)
	long := docx.Paragraph{Runs: []docx.Run{{
		Text: "A really very lengthy textual block that should not fit in twenty points",
	}}}
	scale := r.computeFitTextScale([]docx.Block{long}, 20.0)
	if !(scale > 0 && scale < 1) {
		t.Errorf("expected fit-scale in (0,1); got %v", scale)
	}
}

// TestFitTextScale_NoOpWhenFits returns 1 when the content already fits.
func TestFitTextScale_NoOpWhenFits(t *testing.T) {
	r := makeTestRenderer(t)
	short := docx.Paragraph{Runs: []docx.Run{{Text: "x"}}}
	scale := r.computeFitTextScale([]docx.Block{short}, 500.0)
	if scale != 1 {
		t.Errorf("expected fit-scale = 1 when fits; got %v", scale)
	}
}
