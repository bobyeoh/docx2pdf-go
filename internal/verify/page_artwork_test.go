package verify

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
	"golang.org/x/image/font/gofont/goregular"
)

func TestPageRelativeCoverArtwork(t *testing.T) {
	requireTool(t, "pdftoppm")
	requireTool(t, "pdftotext")
	dir := t.TempDir()
	font := filepath.Join(dir, "font.ttf")
	if err := os.WriteFile(font, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}

	stripe := image.NewRGBA(image.Rect(0, 0, 3, 3))
	draw.Draw(stripe, stripe.Bounds(), image.NewUniform(color.RGBA{43, 103, 170, 255}), image.Point{}, draw.Src)
	var picture bytes.Buffer
	if err := png.Encode(&picture, stripe); err != nil {
		t.Fatal(err)
	}
	input := newDocx().RawBody(`
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
 xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
 xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
 xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"
 xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape"
 xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <w:body>
  <w:p><w:r><w:t>Cover title</w:t></w:r></w:p>
  <w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline>
    <wp:extent cx="254000" cy="0"/>
    <a:graphic><a:graphicData><wps:wsp><wps:spPr>
      <a:prstGeom prst="line"/><a:ln w="25400"><a:solidFill><a:srgbClr val="F9B828"/></a:solidFill></a:ln>
    </wps:spPr></wps:wsp></a:graphicData></a:graphic>
  </wp:inline></w:drawing></mc:Choice><mc:Fallback/></mc:AlternateContent></w:r></w:p>
  <w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:anchor behindDoc="1">
    <wp:positionH relativeFrom="page"><wp:align>left</wp:align></wp:positionH>
    <wp:positionV relativeFrom="page"><wp:align>top</wp:align></wp:positionV>
    <wp:extent cx="3657600" cy="3657600"/><wp:wrapNone/>
    <a:graphic><a:graphicData><wps:wsp><wps:spPr>
      <a:prstGeom prst="rect"/><a:solidFill><a:srgbClr val="F4E8D0"/></a:solidFill><a:ln><a:noFill/></a:ln>
    </wps:spPr><wps:style><a:lnRef idx="2"><a:srgbClr val="FF0000"/></a:lnRef></wps:style></wps:wsp></a:graphicData></a:graphic>
  </wp:anchor></w:drawing></mc:Choice><mc:Fallback/></mc:AlternateContent></w:r>
  <w:r><w:drawing><wp:anchor behindDoc="1">
    <wp:positionH relativeFrom="page"><wp:align>left</wp:align></wp:positionH>
    <wp:positionV relativeFrom="page"><wp:posOffset>-12700</wp:posOffset></wp:positionV>
    <wp:extent cx="127000" cy="3683000"/><wp:wrapNone/>
    <a:graphic><a:graphicData><a:blip r:embed="stripe"/></a:graphicData></a:graphic>
  </wp:anchor></w:drawing></w:r></w:p>
  <w:p><w:pPr><w:pageBreakBefore/></w:pPr><w:r><w:t>Body page</w:t></w:r></w:p>
  <w:sectPr><w:headerReference w:type="default" r:id="running"/>
    <w:pgSz w:w="5760" w:h="5760"/><w:pgMar w:top="900" w:bottom="900" w:left="900" w:right="900"/>
  </w:sectPr>
 </w:body>
</w:document>`).Rels(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="stripe" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/stripe.png"/>
  <Relationship Id="running" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
</Relationships>`).Part("header1.xml", `<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>Running header</w:t></w:r></w:p></w:hdr>`).
		Media("stripe.png", picture.Bytes()).Write(t, dir)

	out := filepath.Join(dir, "test.pdf")
	if err := convert.Convert(input, out, convert.Options{FontRegular: font}); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(dir, "cover")
	if result, err := exec.Command("pdftoppm", "-f", "1", "-l", "1", "-singlefile", "-r", "72", "-png", out, prefix).CombinedOutput(); err != nil {
		t.Fatalf("render cover: %v: %s", err, result)
	}
	im, err := loadPNGImage(prefix + ".png")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		x, y int
		want color.RGBA
	}{
		{205, 150, color.RGBA{244, 232, 208, 255}},
		{5, 20, color.RGBA{43, 103, 170, 255}},
		{5, 260, color.RGBA{43, 103, 170, 255}},
	} {
		if got := color.RGBAModel.Convert(im.At(check.x, check.y)); got != check.want {
			t.Errorf("pixel (%d,%d) = %v, want %v", check.x, check.y, got, check.want)
		}
	}
	ink, gold := 0, 0
	for y := 43; y < 115; y++ {
		for x := 44; x < 150; x++ {
			r, g, b, _ := im.At(x, y).RGBA()
			if r < 0x5000 && g < 0x5000 && b < 0x5000 {
				ink++
			}
			if r > 0xe800 && g > 0x9900 && g < 0xd000 && b < 0x5000 {
				gold++
			}
		}
	}
	if ink == 0 || gold < 12 {
		t.Errorf("artwork must leave cover text and connector visible (ink=%d gold=%d)", ink, gold)
	}
	if got := pdftotextRange(t, out, 1, 1); strings.Contains(got, "Running header") {
		t.Errorf("running header is visible over the cover: %q", got)
	}
	if got := pdftotextRange(t, out, 2, 2); !strings.Contains(got, "Running header") {
		t.Errorf("running header is absent on the body page: %q", got)
	}
}
