package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestParseWrapPolygon verifies wp:wrapPolygon → []WrapPathPoint with the
// trailing duplicate-of-start vertex stripped.
func TestParseWrapPolygon(t *testing.T) {
	src := `<wp:wrapPolygon xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
		<wp:start x="0" y="0"/>
		<wp:lineTo x="0" y="21600"/>
		<wp:lineTo x="21600" y="21600"/>
		<wp:lineTo x="21600" y="0"/>
		<wp:lineTo x="0" y="0"/>
	</wp:wrapPolygon>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	se := tok.(xml.StartElement)
	pts, err := parseWrapPolygon(dec, se)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pts) != 4 {
		t.Fatalf("got %d points, want 4 (duplicate close should be dropped); %+v", len(pts), pts)
	}
	want := []WrapPathPoint{
		{X: 0, Y: 0},
		{X: 0, Y: 21600},
		{X: 21600, Y: 21600},
		{X: 21600, Y: 0},
	}
	for i, w := range want {
		if pts[i] != w {
			t.Errorf("pt[%d] = %+v, want %+v", i, pts[i], w)
		}
	}
}
