package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

func makeTestRenderer(t *testing.T) *renderer {
	t.Helper()
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: 595, H: 842}})
	pdf.AddPage()
	if err := pdf.AddTTFFontData(defaultFamily, embeddedRegularFont); err != nil {
		t.Fatalf("register font: %v", err)
	}
	doc := &docx.Document{
		PageSize:    docx.A4Twips,
		Margins:     docx.DefaultMarginsTwips,
		Styles:      map[string]docx.ParagraphStyle{},
		CharStyles:  map[string]docx.RunProps{},
		TableStyles: map[string]docx.TableStyle{},
	}
	return &renderer{
		pdf:   pdf,
		doc:   doc,
		opts:  Options{DefaultFontSize: 11},
		fonts: map[string]bool{defaultFamily: true},
	}
}

func TestRowHeight_ExactClampsToTwips(t *testing.T) {
	longPara := docx.Paragraph{
		Runs: []docx.Run{{Text: "this is a very long string that wraps across multiple lines and would expand the cell beyond its declared exact row height"}},
	}
	row := docx.TableRow{
		HeightTwips:     400,
		HeightRuleExact: true,
		Cells: []docx.TableCell{
			{GridSpan: 1, Blocks: []docx.Block{longPara}},
		},
	}
	r := makeTestRenderer(t)
	got := r.predictRowHeight(row, []float64{100})
	if got != 20 {
		t.Errorf("exact rowHeight = %v, want 20 (twips/20)", got)
	}
}

func TestRowHeight_AtLeastExpandsForLongContent(t *testing.T) {
	longPara := docx.Paragraph{
		Runs: []docx.Run{{Text: "this string will wrap and require more vertical space than the row's declared minimum"}},
	}
	row := docx.TableRow{
		HeightTwips: 200,
		Cells: []docx.TableCell{
			{GridSpan: 1, Blocks: []docx.Block{longPara}},
		},
	}
	r := makeTestRenderer(t)
	got := r.predictRowHeight(row, []float64{100})
	if got <= 10 {
		t.Errorf("atLeast rowHeight should grow above the 10pt minimum; got %v", got)
	}
}

func TestRowHeight_AtLeastHonorsMinimum(t *testing.T) {
	shortPara := docx.Paragraph{
		Runs: []docx.Run{{Text: "x"}},
	}
	row := docx.TableRow{
		HeightTwips: 2000,
		Cells: []docx.TableCell{
			{GridSpan: 1, Blocks: []docx.Block{shortPara}},
		},
	}
	r := makeTestRenderer(t)
	got := r.predictRowHeight(row, []float64{200})
	if got != 100 {
		t.Errorf("atLeast min rowHeight = %v, want 100", got)
	}
}

func TestMeasureTable_ExpandExactRows(t *testing.T) {
	r := makeTestRenderer(t)
	table := docx.Table{
		ColumnWidthsTwips: []int{2000},
		Layout:            "fixed",
		Rows: []docx.TableRow{{
			HeightTwips:     160,
			HeightRule:      "exact",
			HeightRuleExact: true,
			Cells: []docx.TableCell{{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "Two lines\nof body text"}}},
			}}},
		}},
	}
	if got := r.measureTable(table); got != 8 {
		t.Errorf("fixed table height = %g, want 8pt", got)
	}
	r.opts.ExpandExactTableRows = true
	want := r.predictRowHeight(table.Rows[0], []float64{100})
	if want <= 8 {
		t.Fatalf("row prediction did not expand: %gpt", want)
	}
	if got := r.measureTable(table); got != want {
		t.Errorf("expanded table height = %g, want %gpt", got, want)
	}
}
