package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

func makeZipWith(name string, body []byte) *zip.File {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.Create(name)
	_, _ = fw.Write(body)
	_ = w.Close()
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	return zr.File[0]
}

func TestVectorMediaFormat_ByExtension(t *testing.T) {
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"media/image1.emf", "EMF", true},
		{"media/image1.wmf", "WMF", true},
		{"media/image1.png", "", false},
		{"media/image1.JPEG", "", false},
	}
	for _, c := range cases {
		zf := makeZipWith(c.name, []byte("xxxxxxxx"))
		got, ok := vectorMediaFormat(c.name, zf)
		if got != c.want || ok != c.ok {
			t.Errorf("vectorMediaFormat(%q) = (%q,%v), want (%q,%v)",
				c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestVectorMediaFormat_ByMagicBytes(t *testing.T) {
	body := append([]byte{0xD7, 0xCD, 0xC6, 0x9A}, make([]byte, 100)...)
	zf := makeZipWith("media/anon", body)
	got, ok := vectorMediaFormat("media/anon", zf)
	if !ok || got != "WMF" {
		t.Errorf("Aldus WMF sniff = (%q,%v), want (WMF,true)", got, ok)
	}
	body = append([]byte{0x01, 0x00, 0x09, 0x00}, make([]byte, 100)...)
	zf = makeZipWith("media/anon2", body)
	if got, ok := vectorMediaFormat("media/anon2", zf); !ok || got != "WMF" {
		t.Errorf("plain WMF sniff = (%q,%v), want (WMF,true)", got, ok)
	}
}
