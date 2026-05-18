package render

import (
	"testing"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func timeFromYMD(y, m, d, hh, mm, ss int) time.Time {
	return time.Date(y, time.Month(m), d, hh, mm, ss, 0, time.UTC)
}

func TestApplyGeneralFormatSwitch(t *testing.T) {
	cases := []struct {
		value, instr, want string
	}{
		{"hello world", ` REF a \* Upper `, "HELLO WORLD"},
		{"HELLO", ` REF a \* Lower `, "hello"},
		{"alice smith", ` REF a \* Caps `, "Alice Smith"},
		{"alice", ` REF a \* FirstCap `, "Alice"},
		{"alice", ` REF a \* MERGEFORMAT `, "alice"},
		{"alice", ` REF a \* CHARFORMAT `, "alice"},
		// numeric formats
		{"4", ` SEQ x \* roman `, "iv"},
		{"4", ` SEQ x \* Roman `, "IV"},
		{"1", ` SEQ x \* alphabetic `, "a"},
		{"3", ` SEQ x \* ALPHABETIC `, "C"},
		{"255", ` SEQ x \* Hex `, "FF"},
		{"21", ` SEQ x \* Ordinal `, "21st"},
		{"2", ` SEQ x \* Ordinal `, "2nd"},
		{"11", ` SEQ x \* Ordinal `, "11th"},
		{"5", ` SEQ x \* CardText `, "Five"},
		{"21", ` SEQ x \* OrdText `, "Twenty-First"},
		// no switch leaves value untouched
		{"plain", ` REF a `, "plain"},
	}
	for _, c := range cases {
		got := applyGeneralFormatSwitch(c.value, c.instr)
		if got != c.want {
			t.Errorf("applyGeneralFormatSwitch(%q, %q) = %q, want %q",
				c.value, c.instr, got, c.want)
		}
	}
}

func TestFormatNumericSwitch_LiteralsAndNegFormat(t *testing.T) {
	// These are the cases the original TestFormatNumericSwitch in
	// extras_fields_test.go doesn't cover: literal prefix/suffix
	// passthrough, percent multiplier, and the ';neg' subformat.
	cases := []struct {
		v     float64
		instr string
		want  string
	}{
		// Currency literal prefix.
		{1500, ` PAGE \# "$#,##0.00" `, "$1,500.00"},
		// Percent multiplies by 100.
		{0.25, ` PAGE \# "0%" `, "25%"},
		// Quote-escaped literal — the quotes are stripped.
		{1500, ` PAGE \# "'$'#,##0" `, "$1,500"},
		// Positive ; negative subformat.
		{-42, ` PAGE \# "0;(0)" `, "(42)"},
		{42, ` PAGE \# "0;(0)" `, "42"},
		// No switch present returns "".
		{3, ` PAGE `, ""},
	}
	for _, c := range cases {
		got := formatNumericSwitch(c.v, c.instr)
		if got != c.want {
			t.Errorf("formatNumericSwitch(%v, %q) = %q, want %q",
				c.v, c.instr, got, c.want)
		}
	}
	// And spot-check the timeFromYMD helper is still consumed somewhere
	// — keep linters happy if we ever drop the only caller.
	_ = timeFromYMD(2026, 1, 1, 0, 0, 0)
}

func TestSymbolFontSwitch(t *testing.T) {
	cases := []struct {
		instr, want string
	}{
		{` SYMBOL 61472 \f "Wingdings" `, "Wingdings"},
		{` SYMBOL 61472 \f Wingdings `, "Wingdings"},
		{` SYMBOL 65 \s 24 `, ""},
	}
	for _, c := range cases {
		got := symbolFontSwitch(c.instr)
		if got != c.want {
			t.Errorf("symbolFontSwitch(%q) = %q, want %q", c.instr, got, c.want)
		}
	}
}

func TestSymbolFontSizeSwitch(t *testing.T) {
	if got := symbolFontSizeSwitch(` SYMBOL 65 \s 24 `); got != 12 {
		t.Errorf("size = %v, want 12 (24 half-points)", got)
	}
	if got := symbolFontSizeSwitch(` SYMBOL 65 `); got != 0 {
		t.Errorf("size = %v, want 0 (missing)", got)
	}
}

func TestParseSymbolHex(t *testing.T) {
	r, ok := parseSymbolCodePointWithSwitches("F0E0", ` SYMBOL F0E0 \h `)
	if !ok || r != 0xF0E0 {
		t.Errorf("hex parse: got (%U,%v), want (U+F0E0,true)", r, ok)
	}
	// Without \h, a bare hex string must fail (no 0x prefix).
	if _, ok := parseSymbolCodePointWithSwitches("FF", ` SYMBOL FF `); ok {
		t.Error("bare hex without \\h should not parse")
	}
}

func TestFlattenFields_SymbolFontSwitch(t *testing.T) {
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: ` SYMBOL 65 \f "Wingdings" \s 24 `},
		{FieldSep: true},
		{Text: "cached"},
		{FieldEnd: true},
	}
	out := flattenFields(runs, fieldVars{})
	if len(out) != 1 {
		t.Fatalf("got %d runs, want 1", len(out))
	}
	if out[0].Text != "A" {
		t.Errorf("text = %q, want A", out[0].Text)
	}
	if out[0].Props.FontFamily != "Wingdings" {
		t.Errorf("font = %q, want Wingdings", out[0].Props.FontFamily)
	}
	if out[0].Props.FontSize != 12 {
		t.Errorf("size = %v, want 12", out[0].Props.FontSize)
	}
}

func TestNeedsForwardPageRefPass(t *testing.T) {
	cases := []struct {
		name string
		doc  *docx.Document
		want bool
	}{
		{
			name: "empty",
			doc:  &docx.Document{},
			want: false,
		},
		{
			name: "body has PAGEREF",
			doc: &docx.Document{
				Body: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{{InstrText: " PAGEREF Sec1 \\h "}}},
				},
			},
			want: true,
		},
		{
			name: "TOC field",
			doc: &docx.Document{
				Body: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{{InstrText: "TOC \\o \"1-3\" "}}},
				},
			},
			want: true,
		},
		{
			name: "no forward refs",
			doc: &docx.Document{
				Body: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{{InstrText: " AUTHOR "}, {Text: "hi"}}},
				},
			},
			want: false,
		},
		{
			name: "PAGEREF in nested table cell",
			doc: &docx.Document{
				Body: []docx.Block{
					docx.Table{Rows: []docx.TableRow{{Cells: []docx.TableCell{{Blocks: []docx.Block{
						docx.Paragraph{Runs: []docx.Run{{InstrText: " PAGEREF X "}}},
					}}}}}},
				},
			},
			want: true,
		},
	}
	for _, c := range cases {
		got := needsForwardPageRefPass(c.doc)
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFlattenFields_GeneralFormatSwitch(t *testing.T) {
	// PAGE with \* Roman should render the page number as ROMAN.
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: ` PAGE \* Roman `},
		{FieldSep: true},
		{Text: "OLD"},
		{FieldEnd: true},
	}
	out := flattenFields(runs, fieldVars{page: 4, pageFmt: "decimal"})
	if len(out) != 1 || out[0].Text != "IV" {
		t.Errorf("got %+v, want one run with text IV", out)
	}
}
