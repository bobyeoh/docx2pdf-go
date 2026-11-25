package docx

import (
	"bytes"
	"testing"
)

func TestParagraphStyleSpacingExplicitZero(t *testing.T) {
	data := buildDocxParts(t, map[string]string{
		"word/styles.xml": `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:pPrDefault><w:pPr><w:spacing w:before="60" w:after="80"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:styleId="Base"><w:pPr><w:spacing w:before="100" w:after="120"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Compact"><w:basedOn w:val="Base"/><w:pPr><w:spacing w:before="0"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Inherited"><w:basedOn w:val="Compact"/><w:pPr><w:spacing w:after="0"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Standalone"><w:pPr><w:spacing w:after="0"/></w:pPr></w:style>
</w:styles>`,
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:tbl><w:tr><w:tc>
<w:p><w:pPr><w:pStyle w:val="Compact"/></w:pPr><w:r><w:t>Base with one override</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Inherited"/></w:pPr><w:r><w:t>Both overrides</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Standalone"/></w:pPr><w:r><w:t>Document default and override</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Inherited"/><w:spacing w:before="40" w:after="60"/></w:pPr><w:r><w:t>Direct formatting</w:t></w:r></w:p>
</w:tc></w:tr></w:tbl></w:body></w:document>`,
	})
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := doc.Body[0].(Table).Rows[0].Cells[0].Paragraphs()
	want := [][2]float64{{0, 6}, {0, 0}, {3, 0}, {2, 3}}
	if len(paragraphs) != len(want) {
		t.Fatalf("got %d paragraphs, want %d", len(paragraphs), len(want))
	}
	for i, p := range paragraphs {
		if got := [2]float64{p.SpacingBefore, p.SpacingAfter}; got != want[i] {
			t.Errorf("paragraph %d before/after spacing = %v, want %v", i+1, got, want[i])
		}
	}
}
