package render

import "testing"

// TestNumericSwitch_ThreePartAndXDropper exercises the format-switch
// upgrades surfaced by the docx4j audit: three-part `pos;neg;zero`
// split, the `x` digit dropper, and the `+` / `-` sign prefix tokens.
func TestNumericSwitch_ThreePartAndXDropper(t *testing.T) {
	cases := []struct {
		v     float64
		instr string
		want  string
	}{
		{42, ` \# "0;(0);-" `, "42"},
		{-42, ` \# "0;(0);-" `, "(42)"},
		{0, ` \# "0;(0);-" `, "-"},
		{1.2345, ` \# "0.0xx" `, "1.2"},
		{5, ` \# "+0" `, "+5"},
		{-5, ` \# "+0" `, "-5"},
		{5, ` \# "-0" `, " 5"},
		{-5, ` \# "-0" `, "-5"},
	}
	for _, c := range cases {
		got := formatNumericSwitch(c.v, c.instr)
		if got != c.want {
			t.Errorf("formatNumericSwitch(%v, %q) = %q, want %q",
				c.v, c.instr, got, c.want)
		}
	}
}

// TestNumericSwitch_LocaleSymbol exercises the locale-aware variant
// that swaps "." / "," for w:decimalSymbol / w:listSeparator.
func TestNumericSwitch_LocaleSymbol(t *testing.T) {
	out := formatNumericSwitchSep(1234.56, ` \# "#,##0.00" `, ",", ".")
	if out != "1.234,56" {
		t.Errorf("locale-aware numeric: got %q want %q", out, "1.234,56")
	}
}

// TestArabicDash covers the `\* ArabicDash` general-format switch.
func TestArabicDash(t *testing.T) {
	got := applyGeneralFormatSwitch("3", ` PAGE \* ArabicDash `)
	if got != "- 3 -" {
		t.Errorf("ArabicDash: got %q want %q", got, "- 3 -")
	}
}

// TestDBCHAR exercises the full-width digit transform.
func TestDBCHAR(t *testing.T) {
	got := applyGeneralFormatSwitch("123", ` SEQ x \* DBCHAR `)
	want := "１２３"
	if got != want {
		t.Errorf("DBCHAR: got %q want %q", got, want)
	}
}

// TestAMPMVariants checks that the date-layout parser accepts the
// extra AM/PM separators Word allows.
func TestAMPMVariants(t *testing.T) {
	t9 := timeFromYMD(2024, 1, 1, 9, 0, 0)
	t15 := timeFromYMD(2024, 1, 1, 15, 0, 0)
	cases := []struct {
		hour        int
		layout, out string
	}{
		{9, "h AMPM", "9 AM"},
		{15, "h ampm", "3 pm"},
		{9, "h A/P", "9 A"},
		{15, "h a/p", "3 p"},
	}
	for _, c := range cases {
		tm := t9
		if c.hour == 15 {
			tm = t15
		}
		got := applyWordDateLayout(tm, c.layout)
		if got != c.out {
			t.Errorf("applyWordDateLayout(hr=%d, %q) = %q want %q",
				c.hour, c.layout, got, c.out)
		}
	}
}

// TestExtractNumber peels currency / unit decorations off raw strings.
func TestExtractNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"$1,234.56", 1234.56},
		{"€180,000.00 EUR", 180000.0},
		{"(42)", -42.0},
		{"100%", 100.0},
		{"1.234,56", 1234.56},
	}
	for _, c := range cases {
		n, _, ok := extractNumber(c.in)
		if !ok {
			t.Errorf("extractNumber(%q): ok=false", c.in)
			continue
		}
		if n != c.want {
			t.Errorf("extractNumber(%q) = %v, want %v", c.in, n, c.want)
		}
	}
}
