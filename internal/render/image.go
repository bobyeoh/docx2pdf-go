package render

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"

	"github.com/signintech/gopdf"
)

// cropImage returns a SubImage view of img according to per-edge percentage
// crop. Percentages are 0..100; cumulative percentages > 100 collapse to
// the minimum 1×1 region.
func cropImage(img image.Image, top, bottom, left, right float64) image.Image {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	cropL := int(float64(w) * left / 100)
	cropR := int(float64(w) * right / 100)
	cropT := int(float64(h) * top / 100)
	cropB := int(float64(h) * bottom / 100)
	x1 := b.Min.X + cropL
	x2 := b.Max.X - cropR
	y1 := b.Min.Y + cropT
	y2 := b.Max.Y - cropB
	if x2 <= x1 {
		x2 = x1 + 1
	}
	if y2 <= y1 {
		y2 = y1 + 1
	}
	rect := image.Rect(x1, y1, x2, y2)
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(rect)
	}
	out := image.NewNRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(out, out.Bounds(), img, rect.Min, draw.Src)
	return out
}

func (r *renderer) fitImage(img image.Image) (w, h float64) {
	b := img.Bounds()
	const dpi = 96
	w = float64(b.Dx()) * 72 / dpi
	h = float64(b.Dy()) * 72 / dpi
	if w > r.contentW {
		scale := r.contentW / w
		w *= scale
		h *= scale
	}
	return w, h
}

// drawImage normalizes img to 8-bit NRGBA before PNG-encoding. JPEG sources
// come back from image.Decode as image.YCbCr, which png.Encode emits in a
// form gopdf rejects with "16-bit depth not supported". The explicit copy
// also guarantees a portable byte layout regardless of source format.
func (r *renderer) drawImage(img image.Image, x, y, w, h float64) error {
	bounds := img.Bounds()
	if _, isNRGBA := img.(*image.NRGBA); !isNRGBA {
		nrgba := image.NewNRGBA(bounds)
		draw.Draw(nrgba, bounds, img, bounds.Min, draw.Src)
		img = nrgba
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	holder, err := gopdf.ImageHolderByBytes(buf.Bytes())
	if err != nil {
		return err
	}
	return r.pdf.ImageByHolder(holder, x, y, &gopdf.Rect{W: w, H: h})
}
