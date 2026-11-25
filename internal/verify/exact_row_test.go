package verify

import (
	"bytes"
	"fmt"
	"image/png"
	"os/exec"
	"path/filepath"
	"testing"

	docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

func TestExactRowPreservesTextAfterFormattedBlankLine(t *testing.T) {
	requireTool(t, "pdftoppm")

	type ink struct {
		redPixels   int
		firstGreenY int
	}
	renderRow := func(rule string, height int, expand bool) ink {
		t.Helper()
		dir := t.TempDir()
		input := newDocx().Body(fmt.Sprintf(`
<w:tbl><w:tblPr><w:tblLayout w:type="fixed"/></w:tblPr>
  <w:tblGrid><w:gridCol w:w="2400"/></w:tblGrid>
  <w:tr><w:trPr><w:trHeight w:val="%d" w:hRule="%s"/></w:trPr>
    <w:tc><w:p>
      <w:pPr><w:spacing w:line="160" w:lineRule="atLeast"/></w:pPr>
      <w:r><w:rPr><w:sz w:val="12"/></w:rPr><w:br/></w:r>
      <w:r><w:rPr><w:sz w:val="12"/><w:color w:val="FF0000"/></w:rPr><w:t>Final line including descenders: ypqg</w:t></w:r>
    </w:p></w:tc>
  </w:tr>
</w:tbl>
<w:p><w:r><w:rPr><w:color w:val="00A000"/></w:rPr><w:t>After the table</w:t></w:r></w:p>`, height, rule)).Write(t, dir)
		output := filepath.Join(dir, "render.pdf")
		if err := docx2pdf.Convert(input, output, docx2pdf.Options{ExpandExactTableRows: expand}); err != nil {
			t.Fatal(err)
		}

		data, err := exec.Command("pdftoppm", "-png", "-r", "144", "-f", "1", "-l", "1", "-singlefile", output).Output()
		if err != nil {
			t.Fatalf("rasterize PDF: %v", err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}

		// Extractors can see the run even where the PDF clips its ink. Compare
		// the colored glyph pixels against a row tall enough to show all of it.
		painted := ink{firstGreenY: img.Bounds().Max.Y}
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r > 0xdddd && g < 0x8888 && b < 0x8888 {
					painted.redPixels++
				}
				if y < painted.firstGreenY && g > 0x8888 && r < 0x8888 && b < 0x8888 {
					painted.firstGreenY = y
				}
			}
		}
		if painted.firstGreenY == img.Bounds().Max.Y {
			t.Fatal("paragraph after the table has no visible text")
		}
		return painted
	}

	reference := renderRow("atLeast", 1200, false)
	if reference.redPixels == 0 {
		t.Fatal("reference has no visible text")
	}
	if got := renderRow("exact", 400, false); got.redPixels != reference.redPixels {
		t.Errorf("formatted line in a 20 point exact row paints %d red pixels, want %d", got.redPixels, reference.redPixels)
	}
	if got := renderRow("exact", 160, false); got.redPixels != 0 {
		t.Errorf("overflow in an 8 point exact row paints %d red pixels, want 0", got.redPixels)
	}
	// Expansion should preserve the same ink and leave the following paragraph
	// in the same place as a row with an authored minimum height.
	minimum := renderRow("atLeast", 160, false)
	if minimum.redPixels != reference.redPixels {
		t.Fatalf("reference minimum-height row paints %d red pixels, want %d", minimum.redPixels, reference.redPixels)
	}
	if got := renderRow("exact", 160, true); got != minimum {
		t.Errorf("expanded row (red pixels, next paragraph y) = %+v, want %+v", got, minimum)
	}
	fixed := renderRow("exact", 1200, false)
	if got := renderRow("exact", 1200, true); got != fixed {
		t.Errorf("expanding a row that already fits changed (red pixels, next paragraph y) from %+v to %+v", fixed, got)
	}
}
