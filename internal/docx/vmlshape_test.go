package docx

import (
	"bytes"
	"testing"
)

func TestVMLShape_Rect(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
  xmlns:v="urn:schemas-microsoft-com:vml">
<w:body>
<w:p>
<w:r>
<w:pict>
<v:rect style="width:60pt;height:30pt" fillcolor="#FFAA00" strokecolor="#000000" strokeweight="1pt"/>
</w:pict>
</w:r>
</w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	var shape *VMLShape
	for _, r := range p.Runs {
		if r.VMLShape != nil {
			shape = r.VMLShape
			break
		}
	}
	if shape == nil {
		t.Fatalf("no VMLShape on any run: %+v", p.Runs)
	}
	if shape.Kind != "rect" {
		t.Errorf("Kind = %q, want rect", shape.Kind)
	}
	if shape.WidthPt != 60 || shape.HeightPt != 30 {
		t.Errorf("size = (%v,%v), want (60,30)", shape.WidthPt, shape.HeightPt)
	}
	if shape.FillColor != "FFAA00" {
		t.Errorf("FillColor = %q, want FFAA00", shape.FillColor)
	}
	if shape.StrokeColor != "000000" {
		t.Errorf("StrokeColor = %q, want 000000", shape.StrokeColor)
	}
	if shape.StrokeWeightPt != 1 {
		t.Errorf("StrokeWeightPt = %v, want 1", shape.StrokeWeightPt)
	}
}

func TestVMLShape_Oval_NoFill(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
  xmlns:v="urn:schemas-microsoft-com:vml">
<w:body>
<w:p>
<w:r>
<w:pict>
<v:oval style="width:40pt;height:20pt" filled="false" strokecolor="#FF0000"/>
</w:pict>
</w:r>
</w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	var shape *VMLShape
	for _, r := range p.Runs {
		if r.VMLShape != nil {
			shape = r.VMLShape
			break
		}
	}
	if shape == nil {
		t.Fatal("no VMLShape")
	}
	if shape.Kind != "oval" {
		t.Errorf("Kind = %q, want oval", shape.Kind)
	}
	if shape.FillColor != "" {
		t.Errorf("FillColor = %q, want empty (filled=false)", shape.FillColor)
	}
}

func TestVMLShape_TextBoxRichBlocks(t *testing.T) {
	// A v:rect with a v:textbox containing two bold runs across two
	// paragraphs. We expect both the legacy flat string AND the
	// structured TextBoxBlocks slice to be populated, with the run
	// properties (Bold) preserved on the second paragraph.
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
  xmlns:v="urn:schemas-microsoft-com:vml">
<w:body>
<w:p><w:r><w:pict>
<v:rect style="width:120pt;height:60pt">
  <v:textbox>
    <w:txbxContent>
      <w:p><w:r><w:t>Hello</w:t></w:r></w:p>
      <w:p><w:r><w:rPr><w:b/></w:rPr><w:t>World</w:t></w:r></w:p>
    </w:txbxContent>
  </v:textbox>
</v:rect>
</w:pict></w:r></w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	var shape *VMLShape
	for _, r := range p.Runs {
		if r.VMLShape != nil {
			shape = r.VMLShape
			break
		}
	}
	if shape == nil {
		t.Fatal("no VMLShape")
	}
	if shape.TextBox != "Hello World" {
		t.Errorf("flat TextBox = %q, want %q", shape.TextBox, "Hello World")
	}
	if got := len(shape.TextBoxBlocks); got != 2 {
		t.Fatalf("len(TextBoxBlocks) = %d, want 2", got)
	}
	second, ok := shape.TextBoxBlocks[1].(Paragraph)
	if !ok {
		t.Fatalf("second block type = %T, want Paragraph", shape.TextBoxBlocks[1])
	}
	if len(second.Runs) == 0 || !second.Runs[0].Props.Bold {
		t.Errorf("second paragraph run not bold: %+v", second.Runs)
	}
}
