package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestDeobfuscateODTTF round-trips: take a 32-byte payload + tail,
// obfuscate by XORing with the parsed key, and verify
// deobfuscateODTTF recovers the original.
func TestDeobfuscateODTTF(t *testing.T) {
	const guid = "{12345678-1234-5678-9ABC-DEF012345678}"
	original := []byte(
		"ABCDEFGHIJKLMNOP" + // bytes 0-15 (first key apply)
			"QRSTUVWXYZ012345" + // bytes 16-31 (second key apply)
			"rest of font file payload should be untouched",
	)

	key := parseODTTFKey(guid)
	if key == nil || len(key) != 16 {
		t.Fatalf("parseODTTFKey returned %v, want 16 bytes", key)
	}

	obfuscated := make([]byte, len(original))
	copy(obfuscated, original)
	for i := 0; i < 32; i++ {
		obfuscated[i] ^= key[i%16]
	}

	got := deobfuscateODTTF(obfuscated, guid)
	if !bytes.Equal(got, original) {
		t.Errorf("deobfuscateODTTF round-trip failed\n got: %x\nwant: %x", got, original)
	}
}

// TestDeobfuscateODTTF_ShortInput confirms inputs <32 bytes are passed
// through unchanged — Word never writes a short obfuscated font, so
// the safe action is no-op rather than panic.
func TestDeobfuscateODTTF_ShortInput(t *testing.T) {
	in := []byte{1, 2, 3, 4, 5}
	got := deobfuscateODTTF(in, "{12345678-1234-5678-9ABC-DEF012345678}")
	if !bytes.Equal(got, in) {
		t.Errorf("short input: got %x, want %x", got, in)
	}
}

// TestParseODTTFKey covers happy path + malformed inputs (returns nil
// so the caller can fall through).
func TestParseODTTFKey(t *testing.T) {
	cases := []struct {
		in       string
		wantNil  bool
		wantByte byte // first byte of returned key when not nil
	}{
		// 32 hex chars after stripping; reverse of last byte = 0x78
		{"{12345678-1234-5678-9ABC-DEF012345678}", false, 0x78},
		{"12345678-1234-5678-9ABC-DEF012345678", false, 0x78}, // no braces
		{"", true, 0},
		{"not-a-guid", true, 0},
		{"{12345678-1234-5678-9ABC-XXXX12345678}", true, 0},
	}
	for _, c := range cases {
		got := parseODTTFKey(c.in)
		if (got == nil) != c.wantNil {
			t.Errorf("parseODTTFKey(%q) nil? = %v, want nil=%v", c.in, got == nil, c.wantNil)
		}
		if !c.wantNil && got[0] != c.wantByte {
			t.Errorf("parseODTTFKey(%q)[0] = %02X, want %02X", c.in, got[0], c.wantByte)
		}
	}
}

// TestParseFontTable builds a tiny docx-in-memory with a fontTable.xml
// + matching rels + a font payload, then confirms the parser
// populates Document.EmbeddedFonts and round-trips the bytes for both
// plain .ttf parts and ODTTF-obfuscated parts.
func TestParseFontTable(t *testing.T) {
	const guid = "{12345678-1234-5678-9ABC-DEF012345678}"
	plainBytes := []byte("PLAINFONTBYTES_PLAINFONTBYTES_PLAINFONTBYTES")
	odttfPayload := []byte("ODTTFFONTBYTES__ODTTFFONTBYTES__rest payload")

	// Obfuscate the ODTTF payload the way Word does so the parser has
	// to deobfuscate it.
	key := parseODTTFKey(guid)
	obfuscated := make([]byte, len(odttfPayload))
	copy(obfuscated, odttfPayload)
	for i := 0; i < 32 && i < len(obfuscated); i++ {
		obfuscated[i] ^= key[i%16]
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mustWrite := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("word/fontTable.xml", []byte(`<?xml version="1.0"?>
<w:fonts xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
         xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:font w:name="Arial">
    <w:embedRegular r:id="rFontA"/>
  </w:font>
  <w:font w:name="Acme Brand">
    <w:embedRegular r:id="rFontB" w:fontKey="`+guid+`"/>
  </w:font>
</w:fonts>`))
	mustWrite("word/_rels/fontTable.xml.rels", []byte(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rFontA" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/font" Target="fonts/arial.ttf"/>
  <Relationship Id="rFontB" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/font" Target="fonts/acme.odttf"/>
</Relationships>`))
	mustWrite("word/fonts/arial.ttf", plainBytes)
	mustWrite("word/fonts/acme.odttf", obfuscated)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Build the files map the parser expects.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	doc := &Document{}
	if err := parseFontTable(files, doc); err != nil {
		t.Fatalf("parseFontTable: %v", err)
	}

	got, ok := doc.EmbeddedFonts["arial"]
	if !ok {
		t.Fatalf("EmbeddedFonts missing 'arial', got keys: %v", mapKeys(doc.EmbeddedFonts))
	}
	if !bytes.Equal(got.Regular, plainBytes) {
		t.Errorf("arial.Regular = %x, want %x", got.Regular, plainBytes)
	}

	got, ok = doc.EmbeddedFonts["acme brand"]
	if !ok {
		t.Fatalf("EmbeddedFonts missing 'acme brand', got keys: %v", mapKeys(doc.EmbeddedFonts))
	}
	if !bytes.Equal(got.Regular, odttfPayload) {
		t.Errorf("acme.Regular = %x, want %x (deobfuscated)", got.Regular, odttfPayload)
	}
}

func mapKeys(m map[string]EmbeddedFontSet) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
