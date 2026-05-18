package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestLineBandAdjust(t *testing.T) {
	r := &renderer{}
	// No band → no adjustment.
	if _, _, ok := r.lineBandAdjust(100, 50, 400); ok {
		t.Errorf("no band: want ok=false")
	}
	// Active band on the left, line above the band's bottom.
	r.floatBand = &floatWrapBand{leftX: 50, rightX: 150, bottomY: 200, side: "left", gapPt: 5}
	x, w, ok := r.lineBandAdjust(120, 50, 400)
	if !ok {
		t.Fatal("left band: ok=false")
	}
	if x != 155 || w != 295 {
		t.Errorf("left band: x=%v w=%v, want 155, 295", x, w)
	}
	// Below the band — should clear (caller-side detects ok=false).
	if _, _, ok := r.lineBandAdjust(250, 50, 400); ok {
		t.Errorf("below band: want ok=false")
	}
	// Right-side band.
	r.floatBand = &floatWrapBand{leftX: 300, rightX: 400, bottomY: 200, side: "right", gapPt: 4}
	x, w, ok = r.lineBandAdjust(120, 50, 400)
	if !ok {
		t.Fatal("right band: ok=false")
	}
	// limit = 300-4 = 296. baseX=50 → newW = 296-50 = 246.
	if x != 50 || w != 246 {
		t.Errorf("right band: x=%v w=%v, want 50, 246", x, w)
	}
}

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
	// 50% lumMod darkens grey 808080 → 404040 in HSL too (L=0.5 → 0.25).
	if got := applyLumModOff("808080", 0.5, 0); got != "404040" {
		t.Errorf("darken: got %q want 404040", got)
	}
	// 50% lumOff brightens toward white per ECMA-376: L=0 → L+(1-L)*0.5
	// = 0.5 → 128 (0x80).
	if got := applyLumModOff("000000", 0, 0.5); got != "808080" {
		t.Errorf("brighten: got %q want 808080", got)
	}
	// 80% lumOff applied to a mid-grey: L=0.5 → 0.5 + 0.5*0.8 = 0.9 → 230.
	if got := applyLumModOff("808080", 0, 0.8); got != "E6E6E6" {
		t.Errorf("tint mid-grey: got %q want E6E6E6", got)
	}
	// Saturated red darkened to 50%: HSL gives (255,0,0) → L=0.5,S=1 →
	// L=0.25 → 128,0,0 (0x800000), close to "half-red".
	if got := applyLumModOff("FF0000", 0.5, 0); got != "800000" {
		t.Errorf("saturated red shade: got %q want 800000", got)
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
