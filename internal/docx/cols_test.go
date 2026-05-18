package docx

import (
	"bytes"
	"testing"
)

func TestColsUnequalWidth(t *testing.T) {
	const docXML = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>hi</w:t></w:r></w:p>
    <w:sectPr>
      <w:cols w:num="3" w:sep="1" w:equalWidth="0">
        <w:col w:w="3000" w:space="200"/>
        <w:col w:w="2000" w:space="200"/>
        <w:col w:w="4000" w:space="0"/>
      </w:cols>
    </w:sectPr>
  </w:body>
</w:document>`
	data := buildMinimalDocx(t, docXML)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Sections) == 0 {
		t.Fatalf("no sections")
	}
	sec := doc.Sections[0]
	if sec.Columns != 3 {
		t.Errorf("Columns = %d, want 3", sec.Columns)
	}
	if !sec.ColumnSeparator {
		t.Errorf("ColumnSeparator not set")
	}
	if sec.ColumnEqualWidth {
		t.Errorf("ColumnEqualWidth should be false")
	}
	if len(sec.ColumnSpecs) != 3 {
		t.Fatalf("ColumnSpecs len = %d, want 3", len(sec.ColumnSpecs))
	}
	if sec.ColumnSpecs[0].WidthTwips != 3000 || sec.ColumnSpecs[0].SpaceTwips != 200 {
		t.Errorf("col[0] = %+v", sec.ColumnSpecs[0])
	}
	if sec.ColumnSpecs[2].WidthTwips != 4000 || sec.ColumnSpecs[2].SpaceTwips != 0 {
		t.Errorf("col[2] = %+v", sec.ColumnSpecs[2])
	}
}
