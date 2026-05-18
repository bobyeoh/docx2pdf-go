package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestParseGradFillLinear(t *testing.T) {
	src := `<a:gradFill xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
		<a:gsLst>
			<a:gs pos="0"><a:srgbClr val="FF0000"/></a:gs>
			<a:gs pos="100000"><a:srgbClr val="0000FF"/></a:gs>
		</a:gsLst>
		<a:lin ang="5400000"/>
	</a:gradFill>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := tok.(xml.StartElement)
	stops, angle, kind, err := parseGradFill(dec, start)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "linear" {
		t.Errorf("kind = %q, want linear", kind)
	}
	if angle != 90.0 {
		t.Errorf("angle = %v, want 90", angle)
	}
	if len(stops) != 2 {
		t.Fatalf("got %d stops, want 2", len(stops))
	}
	if stops[0].Pos != 0 || stops[0].Color != "FF0000" {
		t.Errorf("stop[0] = %+v", stops[0])
	}
	if stops[1].Pos != 1 || stops[1].Color != "0000FF" {
		t.Errorf("stop[1] = %+v", stops[1])
	}
}

func TestParsePattFillAverage(t *testing.T) {
	src := `<a:pattFill xmlns:a="x" prst="pct50">
		<a:fgClr><a:srgbClr val="000000"/></a:fgClr>
		<a:bgClr><a:srgbClr val="FFFFFF"/></a:bgClr>
	</a:pattFill>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := tok.(xml.StartElement)
	c, err := parsePattFill(dec, start)
	if err != nil {
		t.Fatal(err)
	}
	if c != "7F7F7F" {
		t.Errorf("color = %q, want 7F7F7F (avg of black + white)", c)
	}
}

func TestParseEffectListOuterShadow(t *testing.T) {
	src := `<a:effectLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
		<a:outerShdw blurRad="50800" dist="38100" dir="2700000">
			<a:srgbClr val="333333"/>
		</a:outerShdw>
	</a:effectLst>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := tok.(xml.StartElement)
	eff, err := parseEffectList(dec, start)
	if err != nil {
		t.Fatal(err)
	}
	if eff == nil {
		t.Fatal("expected non-nil shadow")
	}
	if eff.Color != "333333" {
		t.Errorf("color = %q", eff.Color)
	}
	// dist=38100 EMU / 12700 = 3pt; direction 2700000 / 60000 = 45 deg.
	// At 45°: x = 3*cos(45°), y = 3*sin(45°) ≈ 2.12 each.
	if eff.OffsetXPt < 2.0 || eff.OffsetXPt > 2.5 {
		t.Errorf("offsetX = %v, want ~2.12", eff.OffsetXPt)
	}
	if eff.BlurPt != 4 {
		t.Errorf("blur = %v, want 4 (50800/12700)", eff.BlurPt)
	}
}
