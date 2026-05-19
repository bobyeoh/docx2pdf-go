package docx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"

	_ "golang.org/x/image/tiff"
)

// eps_preview.go salvages a viewable bitmap from an EPS file. We can't
// (and don't try to) interpret PostScript; what we CAN do is detect the
// DOS EPS Binary File Format (Adobe TN #5002) which embeds a preview
// image at fixed offsets in the file header. Many EPSs Word imports
// carry a TIFF or WMF preview specifically so non-PostScript renderers
// have something to show; we extract that.
//
// DOS EPS Binary File Header layout (30 bytes):
//
//	0:4    magic 0xC5 0xD0 0xD3 0xC6
//	4:8    PostScript section offset
//	8:12   PostScript section length
//	12:16  WMF preview offset (0 if absent)
//	16:20  WMF preview length (0 if absent)
//	20:24  TIFF preview offset (0 if absent)
//	24:28  TIFF preview length (0 if absent)
//	28:30  checksum
//
// When a TIFF preview exists we can decode it via standard image
// libraries; a WMF preview is left to vectorMediaFormat (which already
// surfaces a placeholder).

func extractEPSPreview(zf *zip.File) (image.Image, bool) {
	rc, err := openZipFile(zf)
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	return extractEPSPreviewBytes(data)
}

func extractEPSPreviewBytes(data []byte) (image.Image, bool) {
	if len(data) < 30 {
		return nil, false
	}
	// DOS EPS magic (little-endian DWORD 0xC6D3D0C5 byte-wise).
	if !(data[0] == 0xC5 && data[1] == 0xD0 && data[2] == 0xD3 && data[3] == 0xC6) {
		return nil, false
	}
	// TIFF preview is the most likely to decode; try it first.
	tiffOff := binary.LittleEndian.Uint32(data[20:24])
	tiffLen := binary.LittleEndian.Uint32(data[24:28])
	if tiffOff > 0 && tiffLen > 0 &&
		int(tiffOff)+int(tiffLen) <= len(data) {
		preview := data[tiffOff : tiffOff+tiffLen]
		if img, _, err := image.Decode(bytes.NewReader(preview)); err == nil {
			return img, true
		}
	}
	return nil, false
}
