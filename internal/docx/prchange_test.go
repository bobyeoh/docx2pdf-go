package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestPrChange_Roundtrip exercises every *PrChange variant the parser
// recognizes. We build a small fake docx, parse it, and assert that the
// PrChange pointers carry the expected author/date/id.
func TestPrChange_Roundtrip(t *testing.T) {
	doc := buildPrChangeDoc(t)
	parsed := parseFakeDocx(t, doc)

	// Paragraph w:pPrChange
	if len(parsed.Sections) == 0 {
		t.Fatalf("no sections parsed")
	}
	sec := parsed.Sections[0]
	if len(sec.Blocks) == 0 {
		t.Fatalf("no blocks in section")
	}
	p, ok := sec.Blocks[0].(Paragraph)
	if !ok {
		t.Fatalf("block[0] is %T, want Paragraph", sec.Blocks[0])
	}
	if p.PrChange == nil {
		t.Fatalf("paragraph PrChange not parsed")
	}
	if p.PrChange.Kind != "pPr" || p.PrChange.Author != "Alice" || p.PrChange.ID != "1" {
		t.Errorf("pPrChange = %+v", p.PrChange)
	}

	// Run w:rPrChange
	if len(p.Runs) == 0 {
		t.Fatalf("no runs")
	}
	if p.Runs[0].PrChange == nil {
		t.Fatalf("run PrChange not parsed")
	}
	if p.Runs[0].PrChange.Kind != "rPr" || p.Runs[0].PrChange.Author != "Bob" {
		t.Errorf("rPrChange = %+v", p.Runs[0].PrChange)
	}

	// Table w:tblPrChange + row w:trPrChange + cell w:tcPrChange
	var tbl Table
	foundT := false
	for _, b := range sec.Blocks {
		if v, ok := b.(Table); ok {
			tbl = v
			foundT = true
			break
		}
	}
	if !foundT {
		t.Fatalf("no table parsed")
	}
	if tbl.PrChange == nil || tbl.PrChange.Author != "Carol" {
		t.Errorf("tblPrChange = %+v", tbl.PrChange)
	}
	if len(tbl.Rows) == 0 || tbl.Rows[0].PrChange == nil || tbl.Rows[0].PrChange.Author != "Dan" {
		t.Errorf("trPrChange = %+v", func() *PrChange {
			if len(tbl.Rows) > 0 {
				return tbl.Rows[0].PrChange
			}
			return nil
		}())
	}
	if len(tbl.Rows[0].Cells) == 0 || tbl.Rows[0].Cells[0].PrChange == nil ||
		tbl.Rows[0].Cells[0].PrChange.Author != "Eve" {
		t.Errorf("tcPrChange not parsed correctly")
	}

	// Section w:sectPrChange — Section is the parent of the body.
	if sec.PrChange == nil || sec.PrChange.Author != "Frank" {
		t.Errorf("sectPrChange = %+v", sec.PrChange)
	}
}

func buildPrChangeDoc(t *testing.T) []byte {
	t.Helper()
	const docXML = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr>
        <w:pPrChange w:id="1" w:author="Alice" w:date="2024-01-01T00:00:00Z">
          <w:pPr/>
        </w:pPrChange>
      </w:pPr>
      <w:r>
        <w:rPr>
          <w:rPrChange w:id="2" w:author="Bob" w:date="2024-01-02T00:00:00Z">
            <w:rPr/>
          </w:rPrChange>
        </w:rPr>
        <w:t>hello</w:t>
      </w:r>
    </w:p>
    <w:tbl>
      <w:tblPr>
        <w:tblPrChange w:id="3" w:author="Carol" w:date="2024-01-03T00:00:00Z">
          <w:tblPr/>
        </w:tblPrChange>
      </w:tblPr>
      <w:tblGrid><w:gridCol w:w="2000"/></w:tblGrid>
      <w:tr>
        <w:trPr>
          <w:trPrChange w:id="4" w:author="Dan" w:date="2024-01-04T00:00:00Z">
            <w:trPr/>
          </w:trPrChange>
        </w:trPr>
        <w:tc>
          <w:tcPr>
            <w:tcPrChange w:id="5" w:author="Eve" w:date="2024-01-05T00:00:00Z">
              <w:tcPr/>
            </w:tcPrChange>
          </w:tcPr>
          <w:p><w:r><w:t>cell</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
    </w:tbl>
    <w:sectPr>
      <w:sectPrChange w:id="6" w:author="Frank" w:date="2024-01-06T00:00:00Z">
        <w:sectPr/>
      </w:sectPrChange>
    </w:sectPr>
  </w:body>
</w:document>`
	return buildMinimalDocx(t, docXML)
}

// buildMinimalDocx wraps a body XML into a barebones docx zip.
func buildMinimalDocx(t *testing.T, docXML string) []byte {
	t.Helper()
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	const rootRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string) {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		_, _ = f.Write([]byte(body))
	}
	add("[Content_Types].xml", contentTypes)
	add("_rels/.rels", rootRels)
	add("word/document.xml", docXML)
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func parseFakeDocx(t *testing.T, data []byte) *Document {
	t.Helper()
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

// silence unused import
var _ = strings.NewReader
