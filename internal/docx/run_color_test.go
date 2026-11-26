package docx

import "testing"

func TestRunColorOverridesInheritedStyle(t *testing.T) {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Colored">
    <w:rPr><w:color w:val="FACD62" w:themeColor="accent2" w:themeShade="80"/></w:rPr>
  </w:style>
</w:styles>`
	document := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Colored"/></w:pPr>
      <w:r><w:t>Inherited</w:t></w:r>
      <w:r><w:rPr><w:color w:val="auto"/></w:rPr><w:t>Automatic</w:t></w:r>
      <w:r><w:rPr><w:color w:val="123456"/></w:rPr><w:t>Explicit</w:t></w:r>
      <w:r><w:rPr><w:color w:val="auto" w:themeColor="accent3" w:themeTint="80"/></w:rPr><w:t>Theme</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`

	doc, err := Open(buildDocxWithStyles(t, document, styles))
	if err != nil {
		t.Fatal(err)
	}
	runs := doc.Body[0].(Paragraph).Runs
	want := []struct {
		text, color, theme string
		shade, tint        float64
	}{
		{"Inherited", "FACD62", "accent2", 128.0 / 255, 0},
		{"Automatic", "auto", "", 0, 0},
		{"Explicit", "123456", "", 0, 0},
		{"Theme", "", "accent3", 0, 127.0 / 255},
	}
	if len(runs) != len(want) {
		t.Fatalf("got %d runs, want %d", len(runs), len(want))
	}
	for i, run := range runs {
		expected := want[i]
		got := run.Props
		if run.Text != expected.text || got.Color != expected.color || got.ThemeColor != expected.theme || got.LumMod != expected.shade || got.LumOff != expected.tint {
			t.Errorf("run %d: got text %q, color %q, theme %q, shade %g, tint %g; want %+v", i, run.Text, got.Color, got.ThemeColor, got.LumMod, got.LumOff, expected)
		}
	}
}
