package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestMatchingConditions_RowBandSize(t *testing.T) {
	look := docx.TableLook{}
	// With band size 2: rows 0,1 → band1; rows 2,3 → band2; etc.
	cases := []struct {
		row  int
		want string
	}{
		{0, "band1Horz"},
		{1, "band1Horz"},
		{2, "band2Horz"},
		{3, "band2Horz"},
		{4, "band1Horz"},
	}
	for _, tc := range cases {
		got := matchingConditions(look, tc.row, 0, 6, 1, 2, 1, docx.CnfStyle{}, docx.CnfStyle{})
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("row=%d band size 2: got %v want %s first", tc.row, got, tc.want)
		}
	}
}

func TestMatchingConditions_RowCnfForcesFirstRow(t *testing.T) {
	// Mid-table row carrying cnfStyle firstRow should pose as firstRow.
	rowCnf := docx.CnfStyle{FirstRow: true}
	got := matchingConditions(docx.TableLook{}, 2, 0, 4, 3, 1, 1, rowCnf, docx.CnfStyle{})
	if !contains(got, "firstRow") {
		t.Errorf("row cnf firstRow missing from %v", got)
	}
}

func TestApplyTableStyle_TableShadingFallsThroughToCells(t *testing.T) {
	doc := &docx.Document{TableStyles: map[string]docx.TableStyle{}}
	r := &renderer{doc: doc}
	tbl := docx.Table{
		Shading: "EEEEEE",
		Rows: []docx.TableRow{
			{Cells: []docx.TableCell{{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "a"}}},
			}}}},
			{Cells: []docx.TableCell{{Shading: "FF0000", Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "b"}}},
			}}}},
		},
	}
	r.applyTableStyleToCells(&tbl)
	if got := tbl.Rows[0].Cells[0].Shading; got != "EEEEEE" {
		t.Errorf("empty cell shading = %q, want EEEEEE (inherit from table)", got)
	}
	if got := tbl.Rows[1].Cells[0].Shading; got != "FF0000" {
		t.Errorf("cell with explicit shading = %q, want FF0000 (no inherit)", got)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
