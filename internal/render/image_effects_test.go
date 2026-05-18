package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// helper: make a tiny gradient image so we can check transforms.
func makeStripeImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{R: 80, G: 80, B: 80, A: 255})
	img.SetNRGBA(2, 0, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
	img.SetNRGBA(3, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

func TestApplyImageEffects_Grayscale(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255}) // pure red
	got := applyImageEffects(src, []docx.ImageEffect{{Kind: "grayscl"}})
	c := got.(*image.NRGBA).NRGBAAt(0, 0)
	if c.R != c.G || c.G != c.B {
		t.Fatalf("expected gray, got %v", c)
	}
	// Luma of pure red ≈ 76.
	if c.R < 70 || c.R > 80 {
		t.Errorf("luma of red got %d, want ~76", c.R)
	}
}

func TestApplyImageEffects_BiLevel(t *testing.T) {
	img := makeStripeImage()
	got := applyImageEffects(img, []docx.ImageEffect{{Kind: "biLevel", Threshold: 0.5}})
	g := got.(*image.NRGBA)
	if g.NRGBAAt(0, 0).R != 0 || g.NRGBAAt(1, 0).R != 0 {
		t.Errorf("dark stripes should stay black")
	}
	if g.NRGBAAt(2, 0).R != 255 || g.NRGBAAt(3, 0).R != 255 {
		t.Errorf("light stripes should become white")
	}
}

func TestApplyImageEffects_AlphaMod(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	got := applyImageEffects(src, []docx.ImageEffect{{Kind: "alphaModFix", Amount: 50}})
	c := got.(*image.NRGBA).NRGBAAt(0, 0)
	if c.A < 120 || c.A > 130 {
		t.Errorf("alpha after 50%% = %d, want ~127", c.A)
	}
}
