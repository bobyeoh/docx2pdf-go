package render

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

func mustWriteBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLooksLikeTTC_NonTTC: a regular TTF (first 4 bytes are a numeric
// sfnt version, not "ttcf") is not flagged as a collection.
func TestLooksLikeTTC_NonTTC(t *testing.T) {
	tmp := t.TempDir() + "/plain.ttf"
	// 0x00010000 is a real sfnt version; "OTTO" is OpenType. Either
	// way it's not "ttcf".
	mustWriteBytes(t, tmp, []byte{0x00, 0x01, 0x00, 0x00})
	if looksLikeTTC(tmp) {
		t.Error("looksLikeTTC said yes to a plain TTF signature")
	}
}

func TestLooksLikeTTC_TTC(t *testing.T) {
	tmp := t.TempDir() + "/coll.ttc"
	mustWriteBytes(t, tmp, []byte("ttcf"))
	if !looksLikeTTC(tmp) {
		t.Error("looksLikeTTC missed a 'ttcf'-tagged file")
	}
}

// TestExtractTTCFace0_Smoke builds a minimal valid TTC in memory with
// one tiny "fake" face containing a single 'name' table. We just verify
// extraction produces output whose first 4 bytes are the sfnt version
// (not "ttcf") and whose declared numTables matches. Full TTF parsing
// is gopdf's job — this test only protects the byte arithmetic.
func TestExtractTTCFace0_Smoke(t *testing.T) {
	// One face, one table. Table data: 4 dummy bytes.
	tableData := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	const tableTag = uint32(0x6E616D65) // "name"
	const sfntVersion = uint32(0x00010000)

	// Layout the TTC bytewise.
	var b bytes.Buffer
	// TTC header: tag, version, numFonts, offset[0]
	b.Write([]byte("ttcf"))
	w32 := func(v uint32) {
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], v)
		b.Write(buf[:])
	}
	w16 := func(v uint16) {
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], v)
		b.Write(buf[:])
	}
	w32(0x00010000) // version
	w32(1)          // numFonts
	// offsetTable[0] = where the sfnt offset table starts. We'll fill
	// in after we know our total prefix size: 12 (TTC header) + 4
	// (one offset entry) = 16.
	const face0Offset = uint32(16)
	w32(face0Offset)

	// sfnt offset table for face 0
	w32(sfntVersion) // sfntVersion
	w16(1)           // numTables
	w16(0x10)        // searchRange (cosmetic)
	w16(0)           // entrySelector
	w16(0)           // rangeShift
	// table directory entry: tag, checksum, offset, length
	// Compute table offset: after offset table (12) + one dir entry
	// (16). So at byte face0Offset + 12 + 16 = 44.
	const tableOffset = face0Offset + 12 + 16
	w32(tableTag)
	w32(0) // checksum (ignored by us)
	w32(tableOffset)
	w32(uint32(len(tableData)))

	// table payload at absolute offset tableOffset
	if uint32(b.Len()) != tableOffset {
		t.Fatalf("layout error: b.Len=%d, tableOffset=%d", b.Len(), tableOffset)
	}
	b.Write(tableData)

	out, err := extractTTCFace0Bytes(b.Bytes())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got := binary.BigEndian.Uint32(out[0:4]); got != sfntVersion {
		t.Errorf("output sfntVersion = %x, want %x", got, sfntVersion)
	}
	if got := binary.BigEndian.Uint16(out[4:6]); got != 1 {
		t.Errorf("output numTables = %d, want 1", got)
	}
	if got := binary.BigEndian.Uint32(out[12:16]); got != tableTag {
		t.Errorf("output first table tag = %x, want %x (name)", got, tableTag)
	}
	// The rewritten offset must point INSIDE the output buffer, not
	// the original TTC. Specifically: header (12) + 1×16 dir entry = 28.
	newOff := binary.BigEndian.Uint32(out[20:24])
	if newOff != 28 {
		t.Errorf("rewritten offset = %d, want 28", newOff)
	}
	// And the bytes at the rewritten offset must be our table payload.
	if !bytes.Equal(out[newOff:newOff+uint32(len(tableData))], tableData) {
		t.Errorf("table payload at rewritten offset doesn't match")
	}
}

func TestExtractTTCFace0_BadSignature(t *testing.T) {
	if _, err := extractTTCFace0Bytes([]byte{'O', 'T', 'T', 'O', 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Error("expected error for non-ttcf input")
	}
}

func TestExtractTTCFace0_TooShort(t *testing.T) {
	if _, err := extractTTCFace0Bytes([]byte{'t', 't', 'c', 'f'}); err == nil {
		t.Error("expected error for truncated TTC")
	}
}
