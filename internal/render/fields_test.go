package render

import (
	"testing"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestFieldCodeAndArgs(t *testing.T) {
	cases := []struct {
		in       string
		wantCode string
		wantPrim string
	}{
		{" SEQ Figure \\* ARABIC ", "SEQ", "Figure"},
		{"REF MyBookmark", "REF", "MyBookmark"},
		{"PAGE", "PAGE", ""},
		{" HYPERLINK \"http://example\" ", "HYPERLINK", "http://example"},
		{"", "", ""},
	}
	for _, c := range cases {
		code, prim := fieldCodeAndArgs(c.in)
		if code != c.wantCode || prim != c.wantPrim {
			t.Errorf("fieldCodeAndArgs(%q) = (%q,%q), want (%q,%q)",
				c.in, code, prim, c.wantCode, c.wantPrim)
		}
	}
}

func TestHyperlinkFieldInstr(t *testing.T) {
	cases := []struct {
		in         string
		wantTgt    string
		wantAnchor bool
	}{
		{"HYPERLINK \"http://a.example/page\"", "http://a.example/page", false},
		{"HYPERLINK \\l \"Section1\"", "Section1", true},
		{"HYPERLINK \\o \"tooltip\" \"http://b.example\"", "http://b.example", false},
	}
	for _, c := range cases {
		tgt, anchor := hyperlinkFieldInstr(c.in)
		if tgt != c.wantTgt || anchor != c.wantAnchor {
			t.Errorf("hyperlinkFieldInstr(%q) = (%q,%v), want (%q,%v)",
				c.in, tgt, anchor, c.wantTgt, c.wantAnchor)
		}
	}
}

func TestFormatPageNumber(t *testing.T) {
	cases := []struct {
		page int
		fmt  string
		want string
	}{
		{1, "", "1"},
		{7, "decimal", "7"},
		{4, "upperRoman", "IV"},
		{4, "lowerRoman", "iv"},
		{2, "upperLetter", "B"},
		{27, "lowerLetter", "aa"},
		{0, "", "1"}, // clamped
	}
	for _, c := range cases {
		got := formatPageNumber(c.page, c.fmt)
		if got != c.want {
			t.Errorf("formatPageNumber(%d,%q) = %q, want %q", c.page, c.fmt, got, c.want)
		}
	}
}

func TestLookupFieldValueWith(t *testing.T) {
	v := fieldVars{
		page:        3,
		numPages:    10,
		pageFmt:     "decimal",
		now:         time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC),
		filename:    "report.docx",
		author:      "alice",
		title:       "Q2 Report",
		subject:     "finance",
		seqCounters: map[string]int{},
		bookmarks:   map[string]string{"Section1": "Introduction"},
	}
	cases := []struct {
		code, arg string
		want      string
		wantOK    bool
	}{
		{"PAGE", "", "3", true},
		{"NUMPAGES", "", "10", true},
		{"DATE", "", "2026-05-14", true},
		{"TIME", "", "09:30", true},
		{"FILENAME", "", "report.docx", true},
		{"AUTHOR", "", "alice", true},
		{"TITLE", "", "Q2 Report", true},
		{"SUBJECT", "", "finance", true},
		{"REF", "Section1", "Introduction", true},
		{"REF", "missing", "", false},
		{"UNKNOWN", "", "", false},
	}
	for _, c := range cases {
		got, ok := lookupFieldValueWith(c.code, c.arg, v)
		if got != c.want || ok != c.wantOK {
			t.Errorf("lookupFieldValueWith(%q,%q) = (%q,%v), want (%q,%v)",
				c.code, c.arg, got, ok, c.want, c.wantOK)
		}
	}
}

func TestLookupFieldValueWith_SEQIncrements(t *testing.T) {
	v := fieldVars{seqCounters: map[string]int{}}
	for i := 1; i <= 3; i++ {
		got, ok := lookupFieldValueWith("SEQ", "Figure", v)
		if !ok {
			t.Fatalf("SEQ #%d: ok=false", i)
		}
		want := []string{"1", "2", "3"}[i-1]
		if got != want {
			t.Errorf("SEQ #%d = %q, want %q", i, got, want)
		}
	}
}

func TestFlattenFields_PageSubstitution(t *testing.T) {
	// Simulate the sequence Word emits for a PAGE field:
	//   { begin } { instr "PAGE" } { sep } { result "cached" } { end }
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: "PAGE"},
		{FieldSep: true},
		{Text: "OLD"},
		{FieldEnd: true},
	}
	vars := fieldVars{page: 5, numPages: 10}
	out := flattenFields(runs, vars)
	if len(out) != 1 {
		t.Fatalf("flattenFields: got %d runs, want 1: %+v", len(out), out)
	}
	if out[0].Text != "5" {
		t.Errorf("flattenFields: substituted text = %q, want 5", out[0].Text)
	}
}

func TestFlattenFields_UnknownFallsThrough(t *testing.T) {
	// MERGEFIELD isn't in our handler — cached result should pass through
	// untouched.
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: "MERGEFIELD Name"},
		{FieldSep: true},
		{Text: "John Smith"},
		{FieldEnd: true},
	}
	out := flattenFields(runs, fieldVars{})
	if len(out) != 1 || out[0].Text != "John Smith" {
		t.Errorf("unknown field: got %+v, want one run with %q", out, "John Smith")
	}
}

func TestFlattenFields_Hyperlink(t *testing.T) {
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: `HYPERLINK "http://example.com"`},
		{FieldSep: true},
		{Text: "click here"},
		{FieldEnd: true},
	}
	out := flattenFields(runs, fieldVars{})
	if len(out) != 1 {
		t.Fatalf("hyperlink: got %d runs, want 1", len(out))
	}
	if out[0].LinkURL != "http://example.com" {
		t.Errorf("hyperlink: LinkURL = %q, want http://example.com", out[0].LinkURL)
	}
	if out[0].Text != "click here" {
		t.Errorf("hyperlink: text = %q, want %q", out[0].Text, "click here")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x"); got != "x" {
		t.Errorf("firstNonEmpty: got %q want x", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty: got %q want a", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty: got %q want empty", got)
	}
}
