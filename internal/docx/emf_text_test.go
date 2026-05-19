package docx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildSyntheticEMFWithText creates a minimal EMF byte sequence
// containing one EMR_EXTTEXTOUTW record. Used only for testing the
// extractor; real EMFs have many more record types around the text.
func buildSyntheticEMFWithText(text string) []byte {
	var buf bytes.Buffer
	w := func(v uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		buf.Write(b[:])
	}
	w32 := func(v int32) { w(uint32(v)) }
	w16 := func(v uint16) {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], v)
		buf.Write(b[:])
	}

	// Minimal EMR_HEADER (record type 1, size 24) — bounds-only.
	// Extractor doesn't validate the header so this is enough to give
	// the record walker a known starting record before our text record.
	w(1)     // RecordType = EMR_HEADER
	w(24)    // record size
	w32(0)   // bounds.left
	w32(0)   // bounds.top
	w32(640) // bounds.right
	w32(480) // bounds.bottom

	// EMR_EXTTEXTOUTW (record type 84) with a UTF-16LE buffer.
	utf16text := make([]uint16, 0, len(text))
	for _, r := range text {
		if r < 0x10000 {
			utf16text = append(utf16text, uint16(r))
		} else {
			utf16text = append(utf16text, uint16(r))
		}
	}
	headerLen := uint32(76) // through offDx
	strBytes := uint32(len(utf16text) * 2)
	// Align string length to 4.
	pad := (4 - (strBytes % 4)) % 4
	recSize := headerLen + strBytes + pad
	w(84)                     // record type
	w(recSize)                // record size
	w32(0)                    // bounds.left
	w32(0)                    // bounds.top
	w32(100)                  // bounds.right
	w32(20)                   // bounds.bottom
	w(0)                      // graphics mode
	w(0x3F800000)             // ex scale (1.0 float32)
	w(0x3F800000)             // ey scale (1.0 float32)
	w32(0)                    // reference point x
	w32(0)                    // reference point y
	w(uint32(len(utf16text))) // nChars
	w(76)                     // offString = start of UTF-16 buffer
	w(0)                      // options
	w32(0)                    // rectangle.left
	w32(0)                    // rectangle.top
	w32(100)                  // rectangle.right
	w32(20)                   // rectangle.bottom
	w(0)                      // offDx
	for _, c := range utf16text {
		w16(c)
	}
	for i := uint32(0); i < pad; i++ {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func TestExtractEMFText(t *testing.T) {
	data := buildSyntheticEMFWithText("Hello World")
	// Wrap in an in-memory zip so we can call extractEMFText with a *zip.File.
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, _ := zw.Create("media/image1.emf")
	_, _ = fw.Write(data)
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(zbuf.Bytes()), int64(zbuf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := extractEMFText(zr.File[0])
	if !strings.Contains(got, "Hello World") {
		t.Errorf("extractEMFText = %q, want to contain 'Hello World'", got)
	}
}

func TestExtractEMFText_NoText(t *testing.T) {
	// Just a header — no text records.
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, uint32(88))
	for i := 0; i < 20; i++ {
		binary.Write(&buf, binary.LittleEndian, uint32(0))
	}
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, _ := zw.Create("media/image1.emf")
	_, _ = fw.Write(buf.Bytes())
	zw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(zbuf.Bytes()), int64(zbuf.Len()))
	got := extractEMFText(zr.File[0])
	if got != "" {
		t.Errorf("expected empty for headerless EMF, got %q", got)
	}
}
