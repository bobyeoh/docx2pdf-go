package docx

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/png"
	"testing"
)

// buildSyntheticEMFWithDIB constructs the smallest EMF byte sequence
// that exercises the EMR_STRETCHDIBITS extraction path. The embedded
// DIB is a 2×2, 24-bit, BI_RGB bitmap with four corner colors:
//
//	(0,0) red    (1,0) green
//	(0,1) blue   (1,1) white
//
// EMR_STRETCHDIBITS body offsets (from record start, including the
// 8-byte EMR header) are documented in emf_dib.go. The bitmap rows are
// stored bottom-up, 4-byte aligned (each 2-pixel row is 6 bytes of RGB
// + 2 bytes of pad = 8 bytes), so the byte stream encodes the BOTTOM
// row (blue/white) first, then (red/green).
func buildSyntheticEMFWithDIB() []byte {
	var out bytes.Buffer
	w32 := func(v uint32) { binary.Write(&out, binary.LittleEndian, v) }

	// Record 1: EMR_HEADER. Only the bounds rect bytes need to be valid
	// for our extractor; the EMF spec demands more, but we tolerate it.
	w32(1)  // type EMR_HEADER
	w32(40) // size = 8 (header) + 32 (body)
	// 16 bytes Bounds RECTL: (0,0)-(2,2).
	w32(0)
	w32(0)
	w32(2)
	w32(2)
	// 16 bytes Frame RECTL.
	w32(0)
	w32(0)
	w32(2)
	w32(2)

	// Build the embedded DIB: BITMAPINFOHEADER + 24bpp pixel rows.
	var dib bytes.Buffer
	dw32 := func(v uint32) { binary.Write(&dib, binary.LittleEndian, v) }
	dw16 := func(v uint16) { binary.Write(&dib, binary.LittleEndian, v) }
	dw32(40) // biSize
	dw32(2)  // biWidth
	dw32(2)  // biHeight (positive = bottom-up)
	dw16(1)  // biPlanes
	dw16(24) // biBitCount
	dw32(0)  // biCompression = BI_RGB
	dw32(0)  // biSizeImage
	dw32(0)  // biXPelsPerMeter
	dw32(0)  // biYPelsPerMeter
	dw32(0)  // biClrUsed
	dw32(0)  // biClrImportant
	bmiBytes := dib.Bytes()

	// 24bpp stride: ceil(2*24/8)/4 → 4 bytes? 2*3=6, padded to 8. So
	// each row is 8 bytes. Bottom-up: row 0 (bottom) first.
	// Pixels are stored BGR.
	row := func(b1, g1, r1, b2, g2, r2 byte) []byte {
		return []byte{b1, g1, r1, b2, g2, r2, 0, 0}
	}
	bottomRow := row(0xFF, 0x00, 0x00, 0xFF, 0xFF, 0xFF) // blue, white
	topRow := row(0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00)    // red, green
	pixelBytes := append(append([]byte{}, bottomRow...), topRow...)

	// EMR_STRETCHDIBITS layout body (offsets from record start):
	//   0:8    header type+size
	//   8:24   Bounds RECTL
	//   24:48  xDest, yDest, xSrc, ySrc, cxSrc, cySrc (uint32 each)
	//   48:64  offBmiSrc, cbBmiSrc, offBitsSrc, cbBitsSrc
	//   64:72  iUsageSrc, iBitBltRasterOp
	//   72:80  cxDest, cyDest
	//   80:    BMI bytes, then pixel bytes
	bodyHeaderSize := uint32(8 + 16 + 24 + 16 + 8 + 8) // = 80
	bmiOff := bodyHeaderSize                           // BMI starts immediately after fixed body
	bitsOff := bmiOff + uint32(len(bmiBytes))
	recSize := bitsOff + uint32(len(pixelBytes))

	w32(81)      // EMR_STRETCHDIBITS
	w32(recSize) // size
	// Bounds(16)
	w32(0)
	w32(0)
	w32(2)
	w32(2)
	// xDest/yDest/xSrc/ySrc/cxSrc/cySrc (24)
	w32(0)
	w32(0)
	w32(0)
	w32(0)
	w32(2)
	w32(2)
	// offBmiSrc/cbBmiSrc/offBitsSrc/cbBitsSrc (16)
	w32(bmiOff)
	w32(uint32(len(bmiBytes)))
	w32(bitsOff)
	w32(uint32(len(pixelBytes)))
	// iUsageSrc(4) + iBitBltRasterOp(4)
	w32(0)
	w32(0x00CC0020) // SRCCOPY
	// cxDest(4) + cyDest(4)
	w32(2)
	w32(2)
	// BMI bytes + pixel bytes
	out.Write(bmiBytes)
	out.Write(pixelBytes)

	return out.Bytes()
}

func TestEmfBytesToDIBPng_Decodes24bppDIB(t *testing.T) {
	emf := buildSyntheticEMFWithDIB()
	pngBytes, ok := emfBytesToDIBPng(emf)
	if !ok {
		t.Fatal("emfBytesToDIBPng returned !ok for a valid embedded DIB")
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("dimensions = %dx%d, want 2x2", img.Bounds().Dx(), img.Bounds().Dy())
	}
	// Top-left should be red. Bottom-up DIB → after flipping, top row in
	// our PNG is the "top" row we wrote.
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 0xFF || g>>8 != 0x00 || b>>8 != 0x00 {
		t.Errorf("top-left pixel = RGB(%02X,%02X,%02X), want red", r>>8, g>>8, b>>8)
	}
	r, g, b, _ = img.At(1, 0).RGBA()
	if r>>8 != 0x00 || g>>8 != 0xFF || b>>8 != 0x00 {
		t.Errorf("top-right pixel = RGB(%02X,%02X,%02X), want green", r>>8, g>>8, b>>8)
	}
	r, g, b, _ = img.At(0, 1).RGBA()
	if r>>8 != 0x00 || g>>8 != 0x00 || b>>8 != 0xFF {
		t.Errorf("bottom-left pixel = RGB(%02X,%02X,%02X), want blue", r>>8, g>>8, b>>8)
	}
}

func TestEmfBytesToDIBPng_NoBitmap(t *testing.T) {
	// EMF with only an EMR_HEADER and no DIB-bearing records → !ok.
	var out bytes.Buffer
	w32 := func(v uint32) { binary.Write(&out, binary.LittleEndian, v) }
	w32(1)
	w32(8)
	if _, ok := emfBytesToDIBPng(out.Bytes()); ok {
		t.Fatal("expected !ok when EMF has no DIB record")
	}
}
