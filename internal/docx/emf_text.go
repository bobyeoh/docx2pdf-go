package docx

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf16"
)

// extractEMFText walks an EMF (Enhanced Metafile) byte stream and
// concatenates every EMR_EXTTEXTOUTW (record type 84) text payload it
// finds. This is the most common record Word emits in EMFs produced by
// PowerPoint/Visio embeds, so even without a full rasterizer the
// placeholder can read the actual title and labels.
//
// Returns "" on parse error or when no text records are present.
//
// Record format (Win32 EMRTEXT inside EMR_EXTTEXTOUTW):
//
//	offset    field
//	  0       EMR type (uint32) = 84
//	  4       Size (uint32) of this entire record
//	  8..23   Bounds RECT (int32 ×4) — not used here
//	 24..27   GraphicsMode (uint32)
//	 28..35   ex/ey scale (float32 ×2)
//	 36..43   Reference point (int32 ×2)
//	 44..47   nChars (uint32)
//	 48..51   offString (uint32) — offset to the wide-char buffer
//	          from the start of this record
//	 52..55   options (uint32)
//	 56..71   Rectangle (int32 ×4)
//	 72..75   offDx (uint32)
//	 [offString..offString+2*nChars]  UTF-16LE characters
func extractEMFText(f *zip.File) string {
	rc, err := openZipFile(f)
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return ""
	}
	const (
		emrExttextoutW = 84
		emrExttextoutA = 83 // ANSI variant — same shape, single-byte buffer
	)
	var out strings.Builder
	for off := 0; off+8 <= len(data); {
		recType := binary.LittleEndian.Uint32(data[off:])
		recSize := binary.LittleEndian.Uint32(data[off+4:])
		if recSize < 8 || int(recSize) > len(data)-off {
			break
		}
		end := off + int(recSize)
		switch recType {
		case emrExttextoutW:
			if off+52 <= end {
				nChars := binary.LittleEndian.Uint32(data[off+44:])
				offStr := binary.LittleEndian.Uint32(data[off+48:])
				strStart := off + int(offStr)
				strEnd := strStart + int(nChars)*2
				if strStart >= off+52 && strEnd <= end && nChars > 0 {
					us := make([]uint16, nChars)
					for i := uint32(0); i < nChars; i++ {
						us[i] = binary.LittleEndian.Uint16(data[strStart+int(i)*2:])
					}
					if out.Len() > 0 {
						out.WriteByte(' ')
					}
					out.WriteString(string(utf16.Decode(us)))
				}
			}
		case emrExttextoutA:
			if off+52 <= end {
				nChars := binary.LittleEndian.Uint32(data[off+44:])
				offStr := binary.LittleEndian.Uint32(data[off+48:])
				strStart := off + int(offStr)
				strEnd := strStart + int(nChars)
				if strStart >= off+52 && strEnd <= end && nChars > 0 {
					if out.Len() > 0 {
						out.WriteByte(' ')
					}
					out.Write(data[strStart:strEnd])
				}
			}
		}
		off = end
	}
	return out.String()
}
