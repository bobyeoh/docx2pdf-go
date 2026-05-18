package docx

import (
	"bytes"
	"testing"
)

func TestTblCellSpacing_TableLevel(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:tbl>
<w:tblPr>
<w:tblCellSpacing w:w="60" w:type="dxa"/>
</w:tblPr>
<w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="2000"/></w:tblGrid>
<w:tr>
<w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc>
<w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc>
</w:tr>
</w:tbl>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := doc.Body[0].(Table)
	if !ok {
		t.Fatalf("expected Table at body[0]; got %T", doc.Body[0])
	}
	if tbl.CellSpacingTwips != 60 {
		t.Errorf("CellSpacingTwips = %d, want 60", tbl.CellSpacingTwips)
	}
}

func TestTblCellSpacing_RowLevel(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:tbl>
<w:tblGrid><w:gridCol w:w="2000"/></w:tblGrid>
<w:tr>
<w:trPr>
<w:tblCellSpacing w:w="40" w:type="dxa"/>
</w:trPr>
<w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc>
</w:tr>
</w:tbl>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	tbl := doc.Body[0].(Table)
	if tbl.Rows[0].CellSpacingTwips != 40 {
		t.Errorf("row CellSpacingTwips = %d, want 40", tbl.Rows[0].CellSpacingTwips)
	}
}
