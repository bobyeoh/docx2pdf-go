package docx

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildSyntheticEMFWithRectangle creates a minimal EMF stream
// containing an EMR_HEADER and an EMR_RECTANGLE record.
func buildSyntheticEMFWithRectangle(l, t, r, b int32) []byte {
	var buf bytes.Buffer
	wU32 := func(v uint32) {
		var bb [4]byte
		binary.LittleEndian.PutUint32(bb[:], v)
		buf.Write(bb[:])
	}
	wI32 := func(v int32) { wU32(uint32(v)) }

	// EMR_HEADER (record type 1, size 24)
	wU32(1)
	wU32(24)
	wI32(0)
	wI32(0)
	wI32(100)
	wI32(50)

	// EMR_RECTANGLE (43, size 24 = 8 header + 16 RECT)
	wU32(43)
	wU32(24)
	wI32(l)
	wI32(t)
	wI32(r)
	wI32(b)

	return buf.Bytes()
}

func TestEMFBytesToVMLShape_Rectangle(t *testing.T) {
	data := buildSyntheticEMFWithRectangle(10, 20, 70, 40)
	shape := emfBytesToVMLShape(data)
	if shape == nil {
		t.Fatal("expected non-nil VMLShape for rectangle EMF")
	}
	if shape.Kind != "group" {
		t.Errorf("Kind = %q, want group", shape.Kind)
	}
	if len(shape.Children) != 1 || shape.Children[0].Kind != "rect" {
		t.Fatalf("expected 1 rect child, got %+v", shape.Children)
	}
	c := shape.Children[0]
	if c.OffsetXPt != 10 || c.OffsetYPt != 20 || c.WidthPt != 60 || c.HeightPt != 20 {
		t.Errorf("rect geometry = (%.1f, %.1f, %.1f, %.1f), want (10, 20, 60, 20)",
			c.OffsetXPt, c.OffsetYPt, c.WidthPt, c.HeightPt)
	}
}

func TestEMFCDRGBToHex(t *testing.T) {
	// COLORREF is 0x00BBGGRR. 0xFF00FF = pure magenta written as
	// 0x00FF00FF in the source. r=FF, g=00, b=FF.
	got := cdRGBToHex(0x00FF00FF)
	if got != "FF00FF" {
		t.Errorf("cdRGBToHex(magenta) = %q, want FF00FF", got)
	}
}

func TestEMFEmptyReturnsNil(t *testing.T) {
	if shape := emfBytesToVMLShape([]byte{}); shape != nil {
		t.Errorf("empty data should give nil shape, got %+v", shape)
	}
}
