package render

import (
	"image"
	"image/color"
	"testing"
)

// TestFlipImageH verifies the horizontal-mirror helper actually swaps
// pixels across the vertical axis.
func TestFlipImageH(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(2, 0, color.NRGBA{B: 255, A: 255})
	out := flipImageH(src)
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 != 0 {
		t.Errorf("pixel 0 should be blue after flipH, got rgba %v", out.At(0, 0))
	}
	if _, _, b, _ := out.At(2, 0).RGBA(); b>>8 != 0 {
		t.Errorf("pixel 2 should be red after flipH, got rgba %v", out.At(2, 0))
	}
}

// TestFlipImageV verifies vertical mirror swaps rows.
func TestFlipImageV(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 3))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(0, 1, color.NRGBA{G: 255, A: 255})
	src.Set(0, 2, color.NRGBA{B: 255, A: 255})
	out := flipImageV(src)
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 != 0 {
		t.Errorf("row 0 should be blue after flipV, got rgba %v", out.At(0, 0))
	}
	if _, _, b, _ := out.At(0, 2).RGBA(); b>>8 != 0 {
		t.Errorf("row 2 should be red after flipV, got rgba %v", out.At(0, 2))
	}
}
