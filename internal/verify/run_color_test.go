package verify

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"

	docx2pdf "github.com/bobyeoh/docx2pdf-go"
	"golang.org/x/image/font/gofont/goregular"
)

func TestRenderedAutomaticRunColor(t *testing.T) {
	requireTool(t, "pdftoppm")
	font := filepath.Join(t.TempDir(), "go-regular.ttf")
	if err := os.WriteFile(font, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}

	const styles = `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Colored">
    <w:rPr><w:sz w:val="44"/><w:color w:val="1B66D1"/></w:rPr>
  </w:style>
</w:styles>`
	blue := color.RGBA{R: 0x1B, G: 0x66, B: 0xD1, A: 0xFF}
	for _, tc := range []struct {
		name  string
		props string
	}{
		{name: "inherited"},
		{name: "automatic", props: `<w:rPr><w:color w:val="auto"/></w:rPr>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := newDocx().Styles(styles).Body(`<w:p><w:pPr><w:pStyle w:val="Colored"/></w:pPr><w:r>`+
				tc.props+`<w:t>Sample text</w:t></w:r></w:p>`).Write(t, dir)
			out := filepath.Join(dir, "test.pdf")
			if err := docx2pdf.Convert(in, out, docx2pdf.Options{FontRegular: font}); err != nil {
				t.Fatal(err)
			}
			pages := renderPNG(t, out, dir)
			if len(pages) != 1 {
				t.Fatalf("rendered %d pages, want one", len(pages))
			}
			img, err := loadPNGImage(filepath.Join(dir, pages[0]))
			if err != nil {
				t.Fatal(err)
			}

			hasBlue := imageHasColorNear(img, blue, 12, 20)
			if tc.name == "inherited" {
				if !hasBlue {
					t.Fatal("the control did not render inherited blue text")
				}
				return
			}
			if hasBlue || !imageHasColorNear(img, color.RGBA{A: 0xFF}, 12, 20) {
				t.Fatal("automatic color did not reset the inherited blue text")
			}
		})
	}
}
