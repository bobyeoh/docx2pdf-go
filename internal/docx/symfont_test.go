package docx

import "testing"

// TestMapSymbolGlyph_Wingdings checks that the most common Wingdings
// private-use codepoints translate to readable Unicode equivalents.
func TestMapSymbolGlyph_Wingdings(t *testing.T) {
	cases := []struct {
		font string
		cp   rune
		want rune
	}{
		{"Wingdings", 0xF0E0, '→'},
		{"Wingdings", 0xF0FC, '✓'},
		{"Wingdings", 0xF0FE, '☐'},
		{"Symbol", 0xF0A4, '∞'},
		{"Symbol", 0xF061, 'α'},
		// PUA ASCII mirror — strip F000 offset.
		{"Unknown", 0xF041, 'A'},
		// Out-of-PUA — pass through untouched.
		{"Wingdings", 0x2014, 0x2014},
	}
	for _, c := range cases {
		got := mapSymbolGlyph(c.font, c.cp)
		if got != c.want {
			t.Errorf("mapSymbolGlyph(%q, %#x) = %#x, want %#x",
				c.font, c.cp, got, c.want)
		}
	}
}
