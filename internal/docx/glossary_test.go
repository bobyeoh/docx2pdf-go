package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipWithGlossary(t *testing.T, payload string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/glossary/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestParseGlossaryPart_ExtractsDocPartByName(t *testing.T) {
	const payload = `<w:glossaryDocument xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docPart>
  <w:docPartPr><w:name w:val="Signature"/></w:docPartPr>
  <w:docPartBody>
    <w:p><w:r><w:t>Best regards,</w:t></w:r></w:p>
    <w:p><w:r><w:t>The team</w:t></w:r></w:p>
  </w:docPartBody>
</w:docPart>
</w:glossaryDocument>`
	zr := zipWithGlossary(t, payload)
	doc := &Document{}
	if err := parseGlossaryPart(zr.File[0], doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := doc.Glossary["Signature"]
	if !ok {
		t.Fatalf("Signature key missing, glossary = %v", doc.Glossary)
	}
	// Expect both paragraphs concatenated.
	if !strings.Contains(got, "Best regards") || !strings.Contains(got, "The team") {
		t.Errorf("body = %q, want both paragraphs", got)
	}
}

func TestParseGlossaryPart_MultiplePartsKeyed(t *testing.T) {
	const payload = `<w:glossaryDocument xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docPart>
  <w:docPartPr><w:name w:val="One"/></w:docPartPr>
  <w:docPartBody><w:p><w:r><w:t>first</w:t></w:r></w:p></w:docPartBody>
</w:docPart>
<w:docPart>
  <w:docPartPr><w:name w:val="Two"/></w:docPartPr>
  <w:docPartBody><w:p><w:r><w:t>second</w:t></w:r></w:p></w:docPartBody>
</w:docPart>
</w:glossaryDocument>`
	zr := zipWithGlossary(t, payload)
	doc := &Document{}
	if err := parseGlossaryPart(zr.File[0], doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Glossary["One"] != "first" || doc.Glossary["Two"] != "second" {
		t.Errorf("glossary = %v, want One=first Two=second", doc.Glossary)
	}
}
