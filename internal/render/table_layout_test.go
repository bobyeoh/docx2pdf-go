package render

import (
	"archive/zip"
	"bytes"
	"fmt"
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

func TestTableRowHeightUsesCellMarginsAndRunSize(t *testing.T) {
	for _, tt := range []struct {
		name         string
		tableMargins string
		rowMargins   string
		cellMargins  string
		width        int
		text         string
		want         float64
	}{
		{name: "unspecified margins", width: 2000, text: "short text", want: 17.6},
		{name: "explicit zero margins", tableMargins: cellMarginsXML(0, 0, 0, 0), width: 2000, text: "short text", want: 9.6},
		{name: "row and cell override by side", tableMargins: cellMarginsXML(120, 120, 60, 60),
			rowMargins: `<w:top w:w="0" w:type="dxa"/>`, cellMargins: `<w:bottom w:type="nil"/>`,
			width: 2000, text: "short text", want: 9.6},
		{name: "row margins add height", tableMargins: cellMarginsXML(0, 0, 0, 0),
			rowMargins: cellMarginsXML(120, 120, 0, 0), width: 2000, text: "short text", want: 21.6},
		{name: "horizontal margins cause wrapping", cellMargins: cellMarginsXML(0, 0, 240, 240),
			width: 800, text: "one two", want: 19.2},
		{name: "zero horizontal margins prevent wrapping", tableMargins: cellMarginsXML(0, 0, 240, 240),
			cellMargins: `<w:start w:w="0" w:type="dxa"/><w:end w:w="0" w:type="dxa"/>`,
			width:       800, text: "one two", want: 9.6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			xml := fmt.Sprintf(`<w:tblPr><w:tblLayout w:type="fixed"/><w:tblCellMar>%s</w:tblCellMar></w:tblPr>
<w:tblGrid><w:gridCol w:w="%d"/></w:tblGrid><w:tr><w:tblPrEx><w:tblCellMar>%s</w:tblCellMar></w:tblPrEx>
<w:tc><w:tcPr><w:tcMar>%s</w:tcMar></w:tcPr><w:p>
<w:r><w:rPr><w:sz w:val="16"/></w:rPr><w:t>%s</w:t></w:r></w:p></w:tc></w:tr>`,
				tt.tableMargins, tt.width, tt.rowMargins, tt.cellMargins, tt.text)
			tbl := parseTableLayout(t, xml)
			r := makeTestRenderer(t)
			r.noPageBreak = true
			r.contentW = 200
			if got := r.measureTable(tbl); math.Abs(got-tt.want) > 0.01 {
				t.Errorf("predicted row height = %g pt, want %g pt", got, tt.want)
			}
			if err := r.drawTable(tbl); err != nil {
				t.Fatal(err)
			}
			if got := r.cursorY; math.Abs(got-tt.want) > 0.01 {
				t.Errorf("rendered row height = %g pt, want %g pt", got, tt.want)
			}
			if got := r.pdf.GetY(); got >= r.cursorY {
				t.Errorf("drawn text position = %g pt, row bottom = %g pt", got, r.cursorY)
			}
		})
	}
}

func cellMarginsXML(top, bottom, left, right int) string {
	return fmt.Sprintf(`<w:top w:w="%d" w:type="dxa"/><w:bottom w:w="%d" w:type="dxa"/>
<w:left w:w="%d" w:type="dxa"/><w:right w:w="%d" w:type="dxa"/>`, top, bottom, left, right)
}

func parseTableLayout(t *testing.T, tableXML string) docx.Table {
	t.Helper()
	var data bytes.Buffer
	zipWriter := zip.NewWriter(&data)
	part, err := zipWriter.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(part, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:tbl>%s</w:tbl></w:body></w:document>`, tableXML)
	if err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	doc, err := docx.Parse(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return doc.Body[0].(docx.Table)
}
