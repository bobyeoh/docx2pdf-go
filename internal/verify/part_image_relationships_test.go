package verify

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/bobyeoh/docx2pdf-go/internal/render"
	"golang.org/x/image/font/gofont/goregular"
)

func TestPartImageRelationships(t *testing.T) {
	const relNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	const relType = relNS + "/"
	const relsStart = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	part := func(tag, inner string) string {
		return fmt.Sprintf(`<w:%[1]s xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
			xmlns:r="%[2]s" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
			xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
			xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"
			xmlns:asvg="http://schemas.microsoft.com/office/drawing/2016/SVG/main"
			xmlns:v="urn:schemas-microsoft-com:vml">%[3]s</w:%[1]s>`,
			tag, relNS, inner)
	}
	imageRel := func(id, target string) string {
		return fmt.Sprintf(`<Relationship Id="%s" Type="%simage" Target="%s"/>`, id, relType, target)
	}
	inline := func(rid, svgRID string) string {
		svg := ""
		if svgRID != "" {
			svg = fmt.Sprintf(`<a:extLst><a:ext><asvg:svgBlip r:embed="%s"/></a:ext></a:extLst>`, svgRID)
		}
		return fmt.Sprintf(`<w:p><w:r><w:drawing><wp:inline><wp:extent cx="190500" cy="190500"/>
			<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic>
			<pic:blipFill><a:blip r:embed="%s">%s</a:blip></pic:blipFill>
			</pic:pic></a:graphicData></a:graphic>
			</wp:inline></w:drawing></w:r></w:p>`, rid, svg)
	}
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	yellow := color.RGBA{R: 255, G: 255, A: 255}
	dir := t.TempDir()
	input := newDocx().
		Body(inline("rId1", "")+`<w:sectPr><w:headerReference r:id="rH" w:type="default"/>
			<w:footerReference r:id="rF" w:type="default"/></w:sectPr>`).
		Rels(relsStart+imageRel("rId1", "closing/word/media/body.png")+
			imageRel("rId3", "closing/word/media/body.png")+
			imageRel("rId4", "closing/word/media/body.png")+
			imageRel("rId5", "closing/word/media/body.png")+
			`<Relationship Id="rH" Type="`+relType+`header" Target="header1.xml"/>
			 <Relationship Id="rF" Type="`+relType+`footer" Target="/word/parts/footer1.xml"/></Relationships>`).
		Part("header1.xml", part("hdr", inline("rId1", "rId2")+inline("rId1", "rId3")+
			inline("rId4", "")+inline("rId5", ""))).
		Part("_rels/header1.xml.rels", relsStart+imageRel("rId1", "media/header.png")+
			imageRel("rId2", "media/header.svg")+imageRel("rId3", "media/bad.svg")+
			`<Relationship Id="rId4" Type="`+relType+`image" Target="media/header.png" TargetMode="External"/>
			</Relationships>`).
		Part("parts/footer1.xml", part("ftr", `<w:p><w:r><w:pict><v:shape style="width:15pt;height:15pt">
			<v:imagedata r:id="rId1"/></v:shape></w:pict></w:r></w:p>`)).
		Part("parts/_rels/footer1.xml.rels", relsStart+imageRel("rId1", "../media/footer.png")+`</Relationships>`).
		Part("closing/word/media/body.png", string(makeSolidPNG(13, 7, red))).
		Media("header.png", makeSolidPNG(15, 8, blue)).
		Part("media/header.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 11 5"><rect width="11" height="5" fill="#00ff00"/></svg>`).
		Part("media/bad.svg", `<svg`).
		Media("footer.png", makeSolidPNG(17, 9, yellow)).
		Write(t, dir)

	doc, err := docx.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(doc.Sections))
	}
	sec := doc.Sections[0]
	checkImages := func(name string, blocks []docx.Block, want ...color.RGBA) {
		t.Helper()
		if len(blocks) != len(want) {
			t.Fatalf("%s has %d blocks, want %d", name, len(blocks), len(want))
		}
		for i, block := range blocks {
			p, ok := block.(docx.Paragraph)
			if !ok || len(p.Runs) != 1 {
				t.Fatalf("%s block %d has no single image run: %#v", name, i, block)
			}
			img := doc.Images[p.Runs[0].ImageID]
			if want[i].A == 0 {
				if img != nil {
					t.Errorf("%s block %d resolved an unavailable image", name, i)
				}
				continue
			}
			if img == nil {
				t.Errorf("%s block %d image did not resolve", name, i)
				continue
			}
			center := img.Bounds().Min.Add(image.Pt(img.Bounds().Dx()/2, img.Bounds().Dy()/2))
			if got := color.RGBAModel.Convert(img.At(center.X, center.Y)); got != want[i] {
				t.Errorf("%s block %d pixel = %v, want %v", name, i, got, want[i])
			}
		}
	}
	checkImages("body", sec.Blocks, red)
	checkImages("header", doc.HeaderBlocks, green, blue, color.RGBA{}, color.RGBA{})
	checkImages("footer", doc.FooterBlocks, yellow)

	font := filepath.Join(dir, "go-regular.ttf")
	if err := os.WriteFile(font, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	var pdf bytes.Buffer
	if err := render.RenderWriter(doc, &pdf, render.Options{FontRegular: font}); err != nil {
		t.Fatal(err)
	}
	imageObject := regexp.MustCompile(`/Subtype\s*/Image\s+/Width\s+(\d+)\s+/Height\s+(\d+)`)
	var sizes []string
	for _, match := range imageObject.FindAllStringSubmatch(pdf.String(), -1) {
		sizes = append(sizes, match[1]+"x"+match[2])
	}
	slices.Sort(sizes)
	if want := []string{"13x7", "15x8", "17x9", "22x10"}; !slices.Equal(sizes, want) {
		t.Errorf("embedded PDF image sizes = %v, want %v", sizes, want)
	}
}
