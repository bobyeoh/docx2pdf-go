package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestExtractExcelEmbed_Basic(t *testing.T) {
	// Build an in-memory .xlsx with two cells.
	const sheet = `<?xml version="1.0"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2"><v>42</v></c>
      <c r="B2" t="b"><v>1</v></c>
    </row>
  </sheetData>
</worksheet>`
	const shared = `<?xml version="1.0"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>Name</t></si>
  <si><t>Active</t></si>
</sst>`

	var xlsx bytes.Buffer
	zw := zip.NewWriter(&xlsx)
	f, _ := zw.Create("xl/sharedStrings.xml")
	_, _ = f.Write([]byte(shared))
	f, _ = zw.Create("xl/worksheets/sheet1.xml")
	_, _ = f.Write([]byte(sheet))
	_ = zw.Close()

	// Wrap xlsx bytes in an outer zip simulating its place in the
	// word/embeddings/ folder so we can hand a *zip.File to the
	// extractor (which expects to read the bytes itself).
	var outer bytes.Buffer
	ow := zip.NewWriter(&outer)
	wf, _ := ow.Create("word/embeddings/Microsoft_Excel_Worksheet.xlsx")
	_, _ = wf.Write(xlsx.Bytes())
	_ = ow.Close()

	zr, err := zip.NewReader(bytes.NewReader(outer.Bytes()), int64(outer.Len()))
	if err != nil {
		t.Fatalf("outer zip: %v", err)
	}
	embed, err := extractExcelEmbed(zr.File[0])
	if err != nil {
		t.Fatalf("extractExcelEmbed: %v", err)
	}
	if len(embed.Cells) != 2 {
		t.Fatalf("rows = %d, want 2", len(embed.Cells))
	}
	if embed.Cells[0][0] != "Name" || embed.Cells[0][1] != "Active" {
		t.Errorf("row0 = %v", embed.Cells[0])
	}
	if embed.Cells[1][0] != "42" || embed.Cells[1][1] != "TRUE" {
		t.Errorf("row1 = %v", embed.Cells[1])
	}
	flat := flattenExcelGrid(embed)
	want := "Name\tActive\n42\tTRUE"
	if flat != want {
		t.Errorf("flat = %q, want %q", flat, want)
	}
}

func TestColIndexFromRef(t *testing.T) {
	cases := []struct {
		ref  string
		want int
	}{
		{"A1", 0}, {"B7", 1}, {"Z9", 25}, {"AA1", 26}, {"AB1", 27},
	}
	for _, tc := range cases {
		if got := colIndexFromRef(tc.ref, -1); got != tc.want {
			t.Errorf("colIndexFromRef(%q) = %d, want %d", tc.ref, got, tc.want)
		}
	}
}
