package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestMatchingConditions_FirstRow(t *testing.T) {
	look := docx.TableLook{FirstRow: true}
	got := matchingConditions(look, 0, 0, 3, 3, 1, 1, docx.CnfStyle{}, docx.CnfStyle{})
	wantHas := func(needle string) bool {
		for _, s := range got {
			if s == needle {
				return true
			}
		}
		return false
	}
	if !wantHas("firstRow") {
		t.Errorf("first row missing 'firstRow' in %v", got)
	}
}

func TestMatchingConditions_Banding(t *testing.T) {
	look := docx.TableLook{} // no NoHBand → bands ON
	// Row 0 → band1Horz, Row 1 → band2Horz, Row 2 → band1Horz
	c0 := matchingConditions(look, 0, 0, 3, 1, 1, 1, docx.CnfStyle{}, docx.CnfStyle{})
	c1 := matchingConditions(look, 1, 0, 3, 1, 1, 1, docx.CnfStyle{}, docx.CnfStyle{})
	if c0[0] != "band1Horz" || c1[0] != "band2Horz" {
		t.Errorf("banding rows: c0=%v c1=%v", c0, c1)
	}
}

func TestMatchingConditions_NoHBandSuppresses(t *testing.T) {
	look := docx.TableLook{NoHBand: true}
	got := matchingConditions(look, 0, 0, 3, 1, 1, 1, docx.CnfStyle{}, docx.CnfStyle{})
	for _, s := range got {
		if s == "band1Horz" || s == "band2Horz" {
			t.Errorf("NoHBand should suppress banding, got %v", got)
		}
	}
}

func TestMatchingConditions_CornerWins(t *testing.T) {
	look := docx.TableLook{FirstRow: true, FirstColumn: true}
	got := matchingConditions(look, 0, 0, 3, 3, 1, 1, docx.CnfStyle{}, docx.CnfStyle{})
	last := got[len(got)-1]
	if last != "nwCell" {
		t.Errorf("corner should land last in %v", got)
	}
}

func TestApplyTableStyleToCells_AppliesShading(t *testing.T) {
	doc := &docx.Document{
		TableStyles: map[string]docx.TableStyle{
			"BlueHeader": {
				ID: "BlueHeader",
				Conditional: map[string]docx.TableCondPr{
					"firstRow": {
						Run:         docx.RunProps{Bold: true, Color: "FFFFFF"},
						CellShading: "0070C0",
					},
				},
			},
		},
	}
	r := &renderer{doc: doc}
	tbl := docx.Table{
		StyleID: "BlueHeader",
		Look:    docx.TableLook{FirstRow: true},
		Rows: []docx.TableRow{
			{Cells: []docx.TableCell{{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "head"}}},
			}}}},
			{Cells: []docx.TableCell{{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "body"}}},
			}}}},
		},
	}
	r.applyTableStyleToCells(&tbl)
	if tbl.Rows[0].Cells[0].Shading != "0070C0" {
		t.Errorf("first row cell shading = %q, want 0070C0", tbl.Rows[0].Cells[0].Shading)
	}
	if tbl.Rows[1].Cells[0].Shading != "" {
		t.Errorf("second row should not inherit shading, got %q", tbl.Rows[1].Cells[0].Shading)
	}
	headRun := tbl.Rows[0].Cells[0].Blocks[0].(docx.Paragraph).Runs[0]
	if !headRun.Props.Bold || headRun.Props.Color != "FFFFFF" {
		t.Errorf("first row run props = %+v, want Bold/white", headRun.Props)
	}
}
