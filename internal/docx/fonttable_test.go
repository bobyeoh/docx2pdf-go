package docx

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestGUIDToBytes(t *testing.T) {
	got, err := guidToBytes("{AABBCCDD-1122-3344-5566-778899AABBCC}")
	if err != nil {
		t.Fatalf("guidToBytes: %v", err)
	}
	want, _ := hex.DecodeString("DDCCBBAA221144335566778899AABBCC")
	if !bytes.Equal(got, want) {
		t.Errorf("guidToBytes:\n got %x\nwant %x", got, want)
	}
}

func TestGUIDToBytes_Malformed(t *testing.T) {
	cases := []string{
		"",
		"AABBCCDD-1122-3344-5566",
		"{ZZZZZZZZ-1122-3344-5566-778899AABBCC}",
		"{AABBCCDD11223344-5566-778899AABBCC}",
	}
	for _, c := range cases {
		if _, err := guidToBytes(c); err == nil {
			t.Errorf("guidToBytes(%q): expected error", c)
		}
	}
}

func TestDeobfuscateRoundTrip(t *testing.T) {
	original := make([]byte, 64)
	for i := range original {
		original[i] = byte(i)
	}
	guid := "{B0E0F90A-7195-4C50-99B5-DBB4AB45ED5C}"
	key, err := guidToBytes(guid)
	if err != nil {
		t.Fatalf("guid: %v", err)
	}
	obfuscated := make([]byte, len(original))
	copy(obfuscated, original)
	for i := 0; i < 32; i++ {
		obfuscated[i] ^= key[15-(i%16)]
	}
	got, err := deobfuscateFont(obfuscated, guid)
	if err != nil {
		t.Fatalf("deobfuscate: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("round trip failed:\n got %x\nwant %x", got, original)
	}
}

func TestDeobfuscateShortInput(t *testing.T) {
	original := []byte{0x01, 0x02, 0x03, 0x04}
	guid := "{00112233-4455-6677-8899-AABBCCDDEEFF}"
	key, _ := guidToBytes(guid)
	obf := make([]byte, len(original))
	copy(obf, original)
	for i := range obf {
		obf[i] ^= key[15-(i%16)]
	}
	got, err := deobfuscateFont(obf, guid)
	if err != nil {
		t.Fatalf("deobfuscate: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("short-input round trip failed: %x vs %x", got, original)
	}
}
