package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestIsHeadingStyle(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"", false},
		{"Heading1", true},
		{"Heading9", true},
		{"Heading", true},
		{"heading2", true},  // case-insensitive
		{"Heading 3", true}, // tolerate space-separated variant
		{"Title", true},
		{"title", true},
		{"Subtitle", false},
		{"Heading10", false}, // two-digit not recognized; rare in practice
		{"HeadingCustom", false},
		{"Normal", false},
		{"BodyText", false},
	}
	for _, c := range cases {
		got := isHeadingStyle(c.id)
		if got != c.want {
			t.Errorf("isHeadingStyle(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestHeadingTitle(t *testing.T) {
	p := docx.Paragraph{
		StyleID: "Heading1",
		Runs: []docx.Run{
			{Text: "  Chapter "},
			{Text: "One  "},
		},
	}
	if got := headingTitle(p); got != "Chapter One" {
		t.Errorf("headingTitle = %q, want %q", got, "Chapter One")
	}
	// Non-heading paragraph returns empty.
	p2 := docx.Paragraph{StyleID: "Normal", Runs: []docx.Run{{Text: "body"}}}
	if got := headingTitle(p2); got != "" {
		t.Errorf("non-heading returned %q", got)
	}
	// Heading with field markers in runs — markers must not leak into title.
	p3 := docx.Paragraph{
		StyleID: "Heading2",
		Runs: []docx.Run{
			{Text: "Section "},
			{FieldBegin: true},
			{InstrText: "SEQ Section"},
			{FieldSep: true},
			{Text: "3"},
			{FieldEnd: true},
		},
	}
	// The "3" cached field result is plain text inside FieldSep..FieldEnd,
	// so it survives. Field marker runs themselves are dropped.
	if got := headingTitle(p3); got != "Section 3" {
		t.Errorf("heading with field = %q, want %q", got, "Section 3")
	}
}

func TestRoman(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "i"},
		{4, "iv"},
		{9, "ix"},
		{40, "xl"},
		{90, "xc"},
		{400, "cd"},
		{900, "cm"},
		{1994, "mcmxciv"},
	}
	for _, c := range cases {
		got := roman(c.n, false)
		if got != c.want {
			t.Errorf("roman(%d, false) = %q, want %q", c.n, got, c.want)
		}
		upper := roman(c.n, true)
		want := ""
		for _, r := range c.want {
			if r >= 'a' && r <= 'z' {
				want += string(r - ('a' - 'A'))
			} else {
				want += string(r)
			}
		}
		if upper != want {
			t.Errorf("roman(%d, true) = %q, want %q", c.n, upper, want)
		}
	}
}

func TestAlphaLabel(t *testing.T) {
	cases := []struct {
		n     int
		upper bool
		want  string
	}{
		{1, false, "a"},
		{26, false, "z"},
		{27, false, "aa"},
		{52, false, "az"},
		{53, false, "ba"},
		{1, true, "A"},
		{27, true, "AA"},
	}
	for _, c := range cases {
		got := alphaLabel(c.n, c.upper)
		if got != c.want {
			t.Errorf("alphaLabel(%d,%v) = %q, want %q", c.n, c.upper, got, c.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		n      int
		format string
		want   string
	}{
		{3, "decimal", "3"},
		{3, "decimalZero", "03"},
		{11, "decimalZero", "11"},
		{3, "lowerLetter", "c"},
		{3, "upperLetter", "C"},
		{3, "lowerRoman", "iii"},
		{3, "upperRoman", "III"},
		{3, "none", ""},
		{3, "other-unknown", "3"}, // unknown formats fall back to decimal
		{0, "decimal", "1"},       // clamped
	}
	for _, c := range cases {
		got := formatNumber(c.n, c.format)
		if got != c.want {
			t.Errorf("formatNumber(%d,%q) = %q, want %q", c.n, c.format, got, c.want)
		}
	}
}

func TestFormatLevelText(t *testing.T) {
	// Bullet shortcut.
	if got := formatLevelText(docx.NumLevel{Format: "bullet", Text: "•"}, nil, nil); got != "•" {
		t.Errorf("bullet: got %q", got)
	}
	// Default bullet when text is empty.
	if got := formatLevelText(docx.NumLevel{Format: "bullet"}, nil, nil); got != "•" {
		t.Errorf("default bullet: got %q", got)
	}
	// Decimal substitution.
	lv := docx.NumLevel{Format: "decimal", Text: "%1."}
	counters := map[int]int{0: 5}
	all := map[int]docx.NumLevel{0: lv}
	if got := formatLevelText(lv, all, counters); got != "5." {
		t.Errorf("decimal: got %q", got)
	}
	// Multi-level legal numbering — each %N uses its own level's format.
	allMulti := map[int]docx.NumLevel{
		0: {Format: "decimal", Text: "%1."},
		1: {Format: "decimal", Text: "%1.%2."},
		2: {Format: "decimal", Text: "%1.%2.%3"},
	}
	lv = allMulti[2]
	counters = map[int]int{0: 1, 1: 2, 2: 3}
	if got := formatLevelText(lv, allMulti, counters); got != "1.2.3" {
		t.Errorf("multi-level: got %q", got)
	}
	// IsLgl forces decimal even when other levels are roman/letter.
	lv = docx.NumLevel{Format: "upperLetter", Text: "%1.%2", IsLgl: true}
	allLgl := map[int]docx.NumLevel{
		0: {Format: "upperRoman", Text: "%1"},
		1: lv,
	}
	counters = map[int]int{0: 1, 1: 2}
	if got := formatLevelText(lv, allLgl, counters); got != "1.2" {
		t.Errorf("isLgl: got %q", got)
	}
}

// TestFormatLevelText_PerLevelFormat exercises the new behavior where each
// %N substitution honors the FORMAT OF THAT LEVEL rather than blindly using
// the current level's. A mixed roman/decimal outline must show "II.3" when
// level 0 is upperRoman and level 1 is decimal.
func TestFormatLevelText_PerLevelFormat(t *testing.T) {
	all := map[int]docx.NumLevel{
		0: {Format: "upperRoman", Text: "%1."},
		1: {Format: "decimal", Text: "%1.%2"},
	}
	counters := map[int]int{0: 2, 1: 3}
	got := formatLevelText(all[1], all, counters)
	if got != "II.3" {
		t.Errorf("mixed roman+decimal: got %q want II.3", got)
	}
}
