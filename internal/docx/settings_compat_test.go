package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestSettings_HyphenationAndCompat(t *testing.T) {
	const settingsXML = `<?xml version="1.0"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:autoHyphenation w:val="1"/>
  <w:consecutiveHyphenLimit w:val="3"/>
  <w:hyphenationZone w:val="360"/>
  <w:doNotHyphenateCaps w:val="true"/>
  <w:characterSpacingControl w:val="compressPunctuation"/>
  <w:compat>
    <w:growAutofit w:val="1"/>
    <w:noLeading w:val="0"/>
    <w:useFELayout w:val="1"/>
  </w:compat>
</w:settings>`
	const docXML = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>hi</w:t></w:r></w:p></w:body>
</w:document>`
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>
</Types>`
	const rootRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string) {
		f, _ := w.Create(name)
		_, _ = f.Write([]byte(body))
	}
	add("[Content_Types].xml", contentTypes)
	add("_rels/.rels", rootRels)
	add("word/document.xml", docXML)
	add("word/settings.xml", settingsXML)
	_ = w.Close()

	doc, err := Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := doc.Settings
	if !s.AutoHyphenation {
		t.Error("AutoHyphenation not set")
	}
	if s.ConsecutiveHyphenLimit != 3 {
		t.Errorf("ConsecutiveHyphenLimit = %d", s.ConsecutiveHyphenLimit)
	}
	if s.HyphenationZoneTwips != 360 {
		t.Errorf("HyphenationZoneTwips = %d", s.HyphenationZoneTwips)
	}
	if !s.DoNotHyphenateCaps {
		t.Error("DoNotHyphenateCaps not set")
	}
	if s.CharacterSpacingControl != "compressPunctuation" {
		t.Errorf("CharacterSpacingControl = %q", s.CharacterSpacingControl)
	}
	if s.Compat.GrowAutofit == nil || !*s.Compat.GrowAutofit {
		t.Error("Compat.GrowAutofit not set true")
	}
	if s.Compat.NoLeading == nil || *s.Compat.NoLeading {
		t.Error("Compat.NoLeading should be false")
	}
	if s.Compat.UseFELayout == nil || !*s.Compat.UseFELayout {
		t.Error("Compat.UseFELayout not set true")
	}
}
