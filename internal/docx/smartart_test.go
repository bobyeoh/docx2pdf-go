package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

// drawingZipFile is a small helper that bundles a single XML payload at
// the given zip path so we can exercise extractDiagramDrawing in
// isolation. Mirrors chartZipFile for charts.
func drawingZipFile(t *testing.T, path, xml string) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File[0]
}

func TestExtractDiagramDrawing_TwoNodes(t *testing.T) {
	// dsp:drawing with two ellipse shapes positioned in EMU. Each is
	// 1in (914400 EMU) square, offset 0/0 and 100/0 points (or
	// equivalent EMU). Renderer should see two children inside a
	// group whose CoordSizeW is at least the rightmost shape's right
	// edge.
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<dsp:drawing xmlns:dsp="d" xmlns:a="a">
  <dsp:spTree>
    <dsp:sp>
      <dsp:spPr>
        <a:xfrm>
          <a:off x="0" y="0"/>
          <a:ext cx="914400" cy="914400"/>
        </a:xfrm>
        <a:prstGeom prst="ellipse"/>
        <a:solidFill><a:srgbClr val="4F81BD"/></a:solidFill>
      </dsp:spPr>
      <dsp:txBody>
        <a:p><a:r><a:t>Step 1</a:t></a:r></a:p>
      </dsp:txBody>
    </dsp:sp>
    <dsp:sp>
      <dsp:spPr>
        <a:xfrm>
          <a:off x="1828800" y="0"/>
          <a:ext cx="914400" cy="914400"/>
        </a:xfrm>
        <a:prstGeom prst="ellipse"/>
        <a:solidFill><a:srgbClr val="F79646"/></a:solidFill>
      </dsp:spPr>
      <dsp:txBody>
        <a:p><a:r><a:t>Step 2</a:t></a:r></a:p>
      </dsp:txBody>
    </dsp:sp>
  </dsp:spTree>
</dsp:drawing>`
	zf := drawingZipFile(t, "word/diagrams/drawing1.xml", xml)
	sh, err := extractDiagramDrawing(zf)
	if err != nil {
		t.Fatal(err)
	}
	if sh == nil {
		t.Fatal("expected a non-nil shape")
	}
	if sh.Kind != "group" {
		t.Errorf("Kind = %q, want group", sh.Kind)
	}
	if len(sh.Children) != 2 {
		t.Fatalf("got %d children, want 2", len(sh.Children))
	}
	// 914400 EMU / 12700 = 72pt. So each ellipse is 72×72.
	// Right edge: 1828800/12700 + 914400/12700 = 144 + 72 = 216pt.
	if sh.CoordSizeW < 215 || sh.CoordSizeW > 217 {
		t.Errorf("CoordSizeW = %v, want ~216", sh.CoordSizeW)
	}
	c0 := sh.Children[0]
	if c0.Kind != "oval" {
		t.Errorf("child[0].Kind = %q, want oval", c0.Kind)
	}
	if c0.FillColor != "4F81BD" {
		t.Errorf("child[0].FillColor = %q", c0.FillColor)
	}
	if c0.TextBox != "Step 1" {
		t.Errorf("child[0].TextBox = %q", c0.TextBox)
	}
	if c0.WidthPt < 71 || c0.WidthPt > 73 {
		t.Errorf("child[0].WidthPt = %v, want ~72", c0.WidthPt)
	}
	c1 := sh.Children[1]
	if c1.OffsetXPt < 143 || c1.OffsetXPt > 145 {
		t.Errorf("child[1].OffsetXPt = %v, want ~144", c1.OffsetXPt)
	}
}

func TestDiagramSiblingDrawing(t *testing.T) {
	// Build a fake docx zip with paired data + drawing parts and
	// verify the sibling lookup pairs them.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	must := func(name string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("<root/>"))
	}
	must("word/diagrams/data1.xml")
	must("word/diagrams/drawing1.xml")
	must("word/diagrams/data2.xml")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	if got := diagramSiblingDrawing(files, "diagrams/data1.xml"); got == nil || got.Name != "word/diagrams/drawing1.xml" {
		t.Errorf("data1 sibling = %v, want drawing1.xml", got)
	}
	if got := diagramSiblingDrawing(files, "diagrams/data2.xml"); got != nil {
		t.Errorf("data2 has no drawing2.xml, want nil sibling")
	}
}
