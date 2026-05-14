package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestTwipsToPt(t *testing.T) {
	cases := []struct {
		in   int
		want float64
	}{
		{0, 0},
		{20, 1},
		{1440, 72},
		{11906, 595.3},
	}
	for _, c := range cases {
		got := twipsToPt(c.in)
		if c.in == 0 {
			if got != 0 {
				t.Errorf("twipsToPt(0) = %v, want 0", got)
			}
			continue
		}
		// 0.05pt tolerance for the rounded A4 width case.
		if got < c.want-0.05 || got > c.want+0.05 {
			t.Errorf("twipsToPt(%d) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in                  string
		wantR, wantG, wantB uint8
	}{
		{"FF0000", 0xFF, 0x00, 0x00},
		{"#00FF00", 0x00, 0xFF, 0x00},
		{"0000FF", 0x00, 0x00, 0xFF},
		{"ABCDEF", 0xAB, 0xCD, 0xEF},
		{"bad", 0, 0, 0}, // wrong length → zero value
		{"", 0, 0, 0},
	}
	for _, c := range cases {
		r, g, b := parseHexColor(c.in)
		if r != c.wantR || g != c.wantG || b != c.wantB {
			t.Errorf("parseHexColor(%q) = (%v,%v,%v), want (%v,%v,%v)",
				c.in, r, g, b, c.wantR, c.wantG, c.wantB)
		}
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	if FileExists(p) {
		t.Errorf("FileExists(%q) = true for non-existent file", p)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !FileExists(p) {
		t.Errorf("FileExists(%q) = false after writing it", p)
	}
}

func TestApplyLumModOff(t *testing.T) {
	// No modifier → identity.
	if got := applyLumModOff("FF0000", 0, 0); got != "FF0000" {
		t.Errorf("identity: got %q", got)
	}
	// 50% lumMod darkens.
	if got := applyLumModOff("808080", 0.5, 0); got != "404040" {
		t.Errorf("darken: got %q want 404040", got)
	}
	// 50% lumOff brightens toward white.
	if got := applyLumModOff("000000", 0, 0.5); got != "7F7F7F" {
		t.Errorf("brighten: got %q want 7F7F7F", got)
	}
}

func TestHighlightRGB(t *testing.T) {
	r, g, b, ok := highlightRGB("yellow")
	if !ok || r != 0xFF || g != 0xFF || b != 0x00 {
		t.Errorf("yellow = (%v,%v,%v,%v)", r, g, b, ok)
	}
	if _, _, _, ok := highlightRGB("not-a-color"); ok {
		t.Error("unknown color returned ok=true")
	}
}

func TestRunBackgroundRGB(t *testing.T) {
	// Highlight wins when both set.
	r, g, b, ok := runBackgroundRGB(docx.RunProps{Highlight: "yellow", Shading: "000000"})
	if !ok || r != 0xFF || g != 0xFF {
		t.Errorf("highlight precedence: got (%v,%v,%v,%v)", r, g, b, ok)
	}
	// Shading-only.
	r, g, b, ok = runBackgroundRGB(docx.RunProps{Shading: "112233"})
	if !ok || r != 0x11 || g != 0x22 || b != 0x33 {
		t.Errorf("shading: got (%v,%v,%v,%v)", r, g, b, ok)
	}
	// Neither set.
	if _, _, _, ok := runBackgroundRGB(docx.RunProps{}); ok {
		t.Error("empty props returned ok=true")
	}
}

func TestIsCJK(t *testing.T) {
	for _, r := range "漢字abc日本語한국어" {
		got := isCJK(r)
		isAscii := r < 0x80
		if isAscii && got {
			t.Errorf("isCJK(%q) = true, want false (ascii)", r)
		}
		if !isAscii && !got {
			t.Errorf("isCJK(%q) = false, want true (cjk)", r)
		}
	}
}
