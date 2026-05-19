package docx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
)

// emf_dib.go pulls embedded raster DIBs out of EMF metafiles. Many EMFs
// Word emits (screen captures, "Picture (Enhanced Metafile)" paste, the
// "save as picture" path) are not actually vector — they wrap a single
// DIB (Device Independent Bitmap) inside an EMR_STRETCHDIBITS or similar
// record. Extracting that bitmap and treating it as PNG gives us a real
// raster rendering instead of a blank placeholder.
//
// Records covered (MS-EMF 2.3.x):
//
//	EMR_BITBLT(76)            — fixed-rect copy with optional DIB source
//	EMR_STRETCHBLT(77)        — scaled copy with optional DIB source
//	EMR_STRETCHDIBITS(81)     — scaled copy of a DIB
//	EMR_SETDIBITSTODEVICE(80) — unscaled copy of a DIB
//
// Each record carries OffBmiSrc / cbBmiSrc and OffBitsSrc / cbBitsSrc
// offsets relative to the record start that locate the BITMAPINFOHEADER
// and the pixel data inside the record body.
//
// Supported pixel formats: 1, 4, 8, 24, 32 bpp (BI_RGB only). Compressed
// DIBs (BI_RLE4 / BI_RLE8 / BI_BITFIELDS) fall back gracefully.

// emfToDIBPng walks the EMF byte stream and, if it finds an embedded DIB,
// returns it converted to PNG bytes. Returns (nil, false) when no
// extractable bitmap is present so the caller can fall back to the
// vector replay path.
func emfToDIBPng(f *zip.File) ([]byte, bool) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	return emfBytesToDIBPng(data)
}

func emfBytesToDIBPng(data []byte) ([]byte, bool) {
	const (
		emrBitBlt            = 76
		emrStretchBlt        = 77
		emrSetDIBitsToDevice = 80
		emrStretchDIBits     = 81
	)
	// Per MS-EMF, the BMI/Bits offsets in these records sit at fixed
	// positions within the record body (which excludes the 8-byte
	// {Type, Size} header). Offsets are from the record start, so we
	// add 0 to body offsets to compare against `recStart` + offset.
	type extract struct {
		bmiOff, bmiCb   int
		bitsOff, bitsCb int
	}
	for off := 0; off+8 <= len(data); {
		recType := binary.LittleEndian.Uint32(data[off:])
		recSize := binary.LittleEndian.Uint32(data[off+4:])
		if recSize < 8 || int(recSize) > len(data)-off {
			break
		}
		end := off + int(recSize)
		body := data[off+8 : end]
		var info extract
		switch recType {
		case emrStretchDIBits:
			// Body layout (offsets from record start):
			//   0  Bounds RECTL(16)
			//   16 xDest, yDest, xSrc, ySrc, cxSrc, cySrc (24)
			//   40 offBmiSrc, cbBmiSrc, offBitsSrc, cbBitsSrc (16)
			//   56 iUsageSrc(4), iBitBltRasterOp(4)
			//   64 cxDest(4), cyDest(4)
			if len(body) >= 64 {
				info.bmiOff = int(binary.LittleEndian.Uint32(body[40:]))
				info.bmiCb = int(binary.LittleEndian.Uint32(body[44:]))
				info.bitsOff = int(binary.LittleEndian.Uint32(body[48:]))
				info.bitsCb = int(binary.LittleEndian.Uint32(body[52:]))
			}
		case emrSetDIBitsToDevice:
			// Body layout (offsets from record start):
			//   0  Bounds RECTL(16)
			//   16 xDest, yDest, xSrc, ySrc, cxSrc, cySrc (24)
			//   40 offBmiSrc, cbBmiSrc, offBitsSrc, cbBitsSrc (16)
			//   56 iUsageSrc(4), iStartScan(4), cScans(4)
			if len(body) >= 56 {
				info.bmiOff = int(binary.LittleEndian.Uint32(body[40:]))
				info.bmiCb = int(binary.LittleEndian.Uint32(body[44:]))
				info.bitsOff = int(binary.LittleEndian.Uint32(body[48:]))
				info.bitsCb = int(binary.LittleEndian.Uint32(body[52:]))
			}
		case emrBitBlt:
			// EMR_BITBLT body layout (offsets from record start):
			//   0  Bounds RECTL(16)
			//   16 xDest, yDest, cxDest, cyDest (16)
			//   32 dwRop(4), xSrc, ySrc (8)
			//   44 XformSrc XFORM (24)
			//   68 BkColorSrc (4), iUsageSrc(4)
			//   76 offBmiSrc, cbBmiSrc, offBitsSrc, cbBitsSrc (16)
			if len(body) >= 92 {
				info.bmiOff = int(binary.LittleEndian.Uint32(body[76:]))
				info.bmiCb = int(binary.LittleEndian.Uint32(body[80:]))
				info.bitsOff = int(binary.LittleEndian.Uint32(body[84:]))
				info.bitsCb = int(binary.LittleEndian.Uint32(body[88:]))
			}
		case emrStretchBlt:
			// EMR_STRETCHBLT body layout:
			//   0  Bounds(16) + xDest,yDest,cxDest,cyDest(16)
			//   32 dwRop(4), xSrc, ySrc(8)
			//   44 XformSrc(24)
			//   68 BkColorSrc(4), iUsageSrc(4)
			//   76 offBmiSrc, cbBmiSrc, offBitsSrc, cbBitsSrc(16)
			//   92 cxSrc(4), cySrc(4)
			if len(body) >= 100 {
				info.bmiOff = int(binary.LittleEndian.Uint32(body[76:]))
				info.bmiCb = int(binary.LittleEndian.Uint32(body[80:]))
				info.bitsOff = int(binary.LittleEndian.Uint32(body[84:]))
				info.bitsCb = int(binary.LittleEndian.Uint32(body[88:]))
			}
		default:
			off = end
			continue
		}
		// Offsets are from record start; data slice is the full file.
		if info.bmiCb >= 40 && info.bitsCb > 0 &&
			off+info.bmiOff+info.bmiCb <= len(data) &&
			off+info.bitsOff+info.bitsCb <= len(data) {
			bmi := data[off+info.bmiOff : off+info.bmiOff+info.bmiCb]
			bits := data[off+info.bitsOff : off+info.bitsOff+info.bitsCb]
			if pngBytes, ok := dibToPNG(bmi, bits); ok {
				return pngBytes, true
			}
		}
		off = end
	}
	return nil, false
}

// dibToPNG converts a BITMAPINFOHEADER + bit array to PNG bytes.
// Returns (nil, false) for unsupported compression schemes or malformed
// headers. Supports 1/4/8/24/32 bpp uncompressed (BI_RGB).
func dibToPNG(bmi, bits []byte) ([]byte, bool) {
	if len(bmi) < 40 {
		return nil, false
	}
	// BITMAPINFOHEADER layout:
	//   0  biSize(4)
	//   4  biWidth(4)
	//   8  biHeight(4)
	//   12 biPlanes(2)
	//   14 biBitCount(2)
	//   16 biCompression(4)
	//   20 biSizeImage(4)
	//   24 biXPelsPerMeter(4)
	//   28 biYPelsPerMeter(4)
	//   32 biClrUsed(4)
	//   36 biClrImportant(4)
	w := int(int32(binary.LittleEndian.Uint32(bmi[4:])))
	h := int(int32(binary.LittleEndian.Uint32(bmi[8:])))
	bpp := int(binary.LittleEndian.Uint16(bmi[14:]))
	comp := binary.LittleEndian.Uint32(bmi[16:])
	if w <= 0 || w > 16384 {
		return nil, false
	}
	flip := false
	if h < 0 {
		h = -h
	} else {
		// Positive height = bottom-up DIB.
		flip = true
	}
	if h <= 0 || h > 16384 {
		return nil, false
	}
	const (
		biRGB       = 0
		biBitFields = 3
	)
	// BI_BITFIELDS with default RGB888 masks is equivalent to BI_RGB at
	// 24/32 bpp; everything else (RLE4/RLE8/JPEG/PNG inside DIB) we punt.
	if comp != biRGB && !(comp == biBitFields && (bpp == 16 || bpp == 32)) {
		return nil, false
	}

	// Row stride is 4-byte aligned.
	stride := ((w*bpp + 31) / 32) * 4
	if stride*h > len(bits) {
		// Truncated — bail rather than read past the end.
		return nil, false
	}

	// Palette (1/4/8 bpp) lives between header and pixel bits. clrUsed=0
	// means default (1<<bpp) entries.
	var palette []color.RGBA
	if bpp == 1 || bpp == 4 || bpp == 8 {
		clrUsed := int(binary.LittleEndian.Uint32(bmi[32:]))
		if clrUsed == 0 {
			clrUsed = 1 << bpp
		}
		palStart := int(binary.LittleEndian.Uint32(bmi[0:]))
		if palStart < 40 {
			palStart = 40
		}
		if palStart+clrUsed*4 > len(bmi) {
			return nil, false
		}
		palette = make([]color.RGBA, clrUsed)
		for i := 0; i < clrUsed; i++ {
			b := bmi[palStart+i*4+0]
			g := bmi[palStart+i*4+1]
			r := bmi[palStart+i*4+2]
			palette[i] = color.RGBA{r, g, b, 0xFF}
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcRow := y
		if flip {
			srcRow = h - 1 - y
		}
		rowOff := srcRow * stride
		row := bits[rowOff : rowOff+stride]
		for x := 0; x < w; x++ {
			var r, g, b, a uint8 = 0, 0, 0, 0xFF
			switch bpp {
			case 1:
				bit := (row[x/8] >> (7 - uint(x%8))) & 1
				if int(bit) < len(palette) {
					p := palette[bit]
					r, g, b = p.R, p.G, p.B
				}
			case 4:
				nib := row[x/2]
				if x%2 == 0 {
					nib >>= 4
				}
				idx := int(nib & 0x0F)
				if idx < len(palette) {
					p := palette[idx]
					r, g, b = p.R, p.G, p.B
				}
			case 8:
				idx := int(row[x])
				if idx < len(palette) {
					p := palette[idx]
					r, g, b = p.R, p.G, p.B
				}
			case 16:
				v := binary.LittleEndian.Uint16(row[x*2:])
				// Default 5-5-5 with default BI_BITFIELDS; promote to 8-bit.
				r = uint8(((v >> 10) & 0x1F) << 3)
				g = uint8(((v >> 5) & 0x1F) << 3)
				b = uint8((v & 0x1F) << 3)
			case 24:
				b = row[x*3+0]
				g = row[x*3+1]
				r = row[x*3+2]
			case 32:
				b = row[x*4+0]
				g = row[x*4+1]
				r = row[x*4+2]
				a = row[x*4+3]
				// DIBs frequently encode 32bpp with a zero alpha channel
				// even when fully opaque (Win32 GDI ignores it). Treat a
				// zero alpha layer as opaque so we don't render a fully
				// transparent screenshot.
				if a == 0 {
					a = 0xFF
				}
			default:
				return nil, false
			}
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}
