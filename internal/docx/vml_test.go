package docx

import "testing"

func TestParseCSSLength(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"72pt", 72},
		{"1in", 72},
		{"1.5in", 108},
		{"2.54cm", 72},
		{"25.4mm", 72},
		{"96px", 72},
		{"1pc", 12},
		{"100", 100}, // no unit → pt
		{"", 0},
		{"junk", 0},
		{"-3pt", -3}, // negative still parses
	}
	for _, c := range cases {
		got := parseCSSLength(c.in)
		// allow tiny float jitter
		if got < c.want-0.001 || got > c.want+0.001 {
			t.Errorf("parseCSSLength(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseVMLSize(t *testing.T) {
	cases := []struct {
		style        string
		wantW, wantH float64
	}{
		{"width:100pt;height:50pt", 100, 50},
		{"height:1in;width:2in", 144, 72},
		{"WIDTH: 36pt ; HEIGHT: 18pt", 36, 18}, // mixed case + spaces
		{"width:100pt", 100, 0},
		{"", 0, 0},
		{"margin-left:5pt", 0, 0}, // unrelated CSS
	}
	for _, c := range cases {
		w, h := parseVMLSize(c.style)
		if w != c.wantW || h != c.wantH {
			t.Errorf("parseVMLSize(%q) = (%v,%v), want (%v,%v)",
				c.style, w, h, c.wantW, c.wantH)
		}
	}
}
