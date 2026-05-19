package docx

import (
	"bytes"
	"strings"
	"testing"
)

func TestParse_DetectsEncryptedCFB(t *testing.T) {
	// Just the CFB magic; the rest doesn't matter for detection.
	data := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 0, 0, 0, 0, 0, 0, 0, 0}
	_, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error for CFB container, got nil")
	}
	if !strings.Contains(err.Error(), "password-protected") {
		t.Errorf("error should mention password protection; got %v", err)
	}
}

func TestParseWithPassword_AttemptsDecrypt(t *testing.T) {
	// CFB magic but with a malformed body — decrypt path should
	// produce a "decrypt docx" error rather than the "supply password"
	// path. This confirms the password branch actually runs.
	data := make([]byte, 512)
	copy(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	_, err := ParseWithPassword(bytes.NewReader(data), int64(len(data)), "test")
	if err == nil {
		t.Fatal("expected decrypt failure on malformed CFB; got nil")
	}
	if !strings.Contains(err.Error(), "decrypt") &&
		!strings.Contains(err.Error(), "EncryptionInfo") &&
		!strings.Contains(err.Error(), "cfb") {
		t.Errorf("wrong error class: %v", err)
	}
}

func TestDecodeBase64(t *testing.T) {
	// Decode "Hello" → 48 65 6c 6c 6f.
	got := decodeBase64("SGVsbG8=")
	want := []byte{0x48, 0x65, 0x6c, 0x6c, 0x6f}
	if !bytes.Equal(got, want) {
		t.Errorf("decodeBase64(SGVsbG8=) = %x, want %x", got, want)
	}
}

func TestUtf16LE(t *testing.T) {
	got := utf16LE("AB")
	want := []byte{0x41, 0x00, 0x42, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("utf16LE(AB) = %x, want %x", got, want)
	}
}

func TestParse_PlainZipStillWorks(t *testing.T) {
	// Empty zip should still get to "open zip" error path, not be
	// misdetected as CFB.
	data := []byte("PK\x03\x04not-a-real-zip")
	_, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil && strings.Contains(err.Error(), "password-protected") {
		t.Errorf("ZIP misclassified as encrypted: %v", err)
	}
}
