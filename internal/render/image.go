package render

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
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

// applyImageEffects walks effs and applies each filter to img, returning a
// new image. The pixel ops happen in a fresh NRGBA so the input is not
// mutated. The order in effs matters — Word processes them top to bottom
// inside <a:blip>.
func applyImageEffects(img image.Image, effs []docx.ImageEffect) image.Image {
	if len(effs) == 0 {
		return img
	}
	b := img.Bounds()
	out := image.NewNRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	for _, eff := range effs {
		switch eff.Kind {
		case "grayscl":
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					y8 := uint8(0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B))
					out.SetNRGBA(x, y, color.NRGBA{R: y8, G: y8, B: y8, A: c.A})
				}
			}
		case "biLevel":
			thr := uint8(eff.Threshold * 255)
			if thr == 0 {
				thr = 128
			}
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					y8 := uint8(0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B))
					v := uint8(0)
					if y8 >= thr {
						v = 255
					}
					out.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: c.A})
				}
			}
		case "lum":
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					rr, gg, bb := adjustLum(c.R, c.G, c.B, eff.Bright, eff.Contrast)
					out.SetNRGBA(x, y, color.NRGBA{R: rr, G: gg, B: bb, A: c.A})
				}
			}
		case "duotone":
			fg := mustHex(eff.FgHex, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
			bg := mustHex(eff.BgHex, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					t := float64(0.299*float64(c.R)+0.587*float64(c.G)+0.114*float64(c.B)) / 255
					rr := uint8(float64(bg.R)*(1-t) + float64(fg.R)*t)
					gg := uint8(float64(bg.G)*(1-t) + float64(fg.G)*t)
					bbv := uint8(float64(bg.B)*(1-t) + float64(fg.B)*t)
					out.SetNRGBA(x, y, color.NRGBA{R: rr, G: gg, B: bbv, A: c.A})
				}
			}
		case "alphaModFix":
			amt := eff.Amount
			if amt <= 0 {
				amt = 100
			}
			if amt > 100 {
				amt = 100
			}
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					c.A = uint8(float64(c.A) * amt / 100)
					out.SetNRGBA(x, y, c)
				}
			}
		case "clrChange":
			from := mustHex(eff.FgHex, color.NRGBA{})
			to := mustHex(eff.BgHex, color.NRGBA{})
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					if c.R == from.R && c.G == from.G && c.B == from.B {
						out.SetNRGBA(x, y, color.NRGBA{R: to.R, G: to.G, B: to.B, A: c.A})
					}
				}
			}
		case "sepia":
			// Sepia matrix from a:duotone — but pre-set warm browns.
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					rr := float64(c.R)*0.393 + float64(c.G)*0.769 + float64(c.B)*0.189
					gg := float64(c.R)*0.349 + float64(c.G)*0.686 + float64(c.B)*0.168
					bb := float64(c.R)*0.272 + float64(c.G)*0.534 + float64(c.B)*0.131
					out.SetNRGBA(x, y, color.NRGBA{
						R: uint8(clamp01(rr / 255 * 255)),
						G: uint8(clamp01(gg / 255 * 255)),
						B: uint8(clamp01(bb / 255 * 255)),
						A: c.A,
					})
				}
			}
		case "alphaInv":
			// Invert alpha: areas that were opaque become transparent and
			// vice versa. eff.FgHex (optional) is the color used to tint
			// the inverted region; without it the pixel keeps its RGB.
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					c.A = 255 - c.A
					out.SetNRGBA(x, y, c)
				}
			}
		case "hsl":
			// HSL shift. eff.HueDeg shifts hue; eff.Saturation scales S;
			// eff.Lum (Lightness) scales L.
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					h, s, l := rgbToHSL(c.R, c.G, c.B)
					h = mathMod(h+eff.HueDeg/360.0, 1.0)
					s *= 1 + eff.Saturation
					l *= 1 + eff.Lum
					if s < 0 {
						s = 0
					} else if s > 1 {
						s = 1
					}
					if l < 0 {
						l = 0
					} else if l > 1 {
						l = 1
					}
					rr, gg, bb := hslToRGB(h, s, l)
					out.SetNRGBA(x, y, color.NRGBA{R: rr, G: gg, B: bb, A: c.A})
				}
			}
		case "tint":
			// Mix toward white by `eff.Amount` (0..100 → 0..1).
			amt := clamp01(eff.Amount / 100)
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					rr := uint8(float64(c.R) + (255-float64(c.R))*amt)
					gg := uint8(float64(c.G) + (255-float64(c.G))*amt)
					bb := uint8(float64(c.B) + (255-float64(c.B))*amt)
					out.SetNRGBA(x, y, color.NRGBA{R: rr, G: gg, B: bb, A: c.A})
				}
			}
		case "shade":
			// Mix toward black by `eff.Amount` (0..100 → 0..1).
			amt := clamp01(eff.Amount / 100)
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					c := out.NRGBAAt(x, y)
					rr := uint8(float64(c.R) * (1 - amt))
					gg := uint8(float64(c.G) * (1 - amt))
					bb := uint8(float64(c.B) * (1 - amt))
					out.SetNRGBA(x, y, color.NRGBA{R: rr, G: gg, B: bb, A: c.A})
				}
			}
		}
	}
	return out
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 255 {
		return 255
	}
	return f
}

func mathMod(x, m float64) float64 {
	for x < 0 {
		x += m
	}
	for x >= m {
		x -= m
	}
	return x
}


// adjustLum applies bright + contrast (each in [-1, 1]) to a single
// channel triple. Bright shifts; contrast scales around 0.5.
func adjustLum(r, g, b uint8, bright, contrast float64) (uint8, uint8, uint8) {
	apply := func(v uint8) uint8 {
		f := float64(v) / 255.0
		// Contrast: scale around 0.5.
		f = (f-0.5)*(1+contrast) + 0.5
		// Brightness: additive.
		f += bright
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		return uint8(f * 255)
	}
	return apply(r), apply(g), apply(b)
}

func mustHex(hex string, fallback color.NRGBA) color.NRGBA {
	if len(hex) < 6 {
		return fallback
	}
	v, err := strconv.ParseUint(hex[:6], 16, 32)
	if err != nil {
		return fallback
	}
	return color.NRGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
		A: 255,
	}
}

// drawTransformedImage applies optional flip + rotation to an image
// before painting it. Flips operate on the image data (we synthesize a
// horizontally / vertically mirrored copy); rotation uses gopdf's
// transform.
func (r *renderer) drawTransformedImage(img image.Image, x, y, w, h float64, rotDeg float64, flipH, flipV bool) error {
	if flipH {
		img = flipImageH(img)
	}
	if flipV {
		img = flipImageV(img)
	}
	if rotDeg != 0 {
		cx, cy := x+w/2, y+h/2
		r.pdf.Rotate(rotDeg, cx, cy)
		defer r.pdf.RotateReset()
	}
	return r.drawImage(img, x, y, w, h)
}

// flipImageH returns a horizontally mirrored copy of img.
func flipImageH(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			sx := b.Max.X - 1 - (x - b.Min.X)
			dst.Set(x, y, src.At(sx, y))
		}
	}
	return dst
}

// flipImageV returns a vertically mirrored copy of img.
func flipImageV(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		sy := b.Max.Y - 1 - (y - b.Min.Y)
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, src.At(x, sy))
		}
	}
	return dst
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
