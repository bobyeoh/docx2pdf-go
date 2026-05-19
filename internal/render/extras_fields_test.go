package render

import (
	"strings"
	"testing"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestDocVariableLookup(t *testing.T) {
	v := fieldVars{docVars: map[string]string{"ReleaseVersion": "2.4.0"}}
	got, ok := lookupFieldValueWith("DOCVARIABLE", "ReleaseVersion", v)
	if !ok || got != "2.4.0" {
		t.Errorf("DOCVARIABLE = (%q,%v), want (2.4.0,true)", got, ok)
	}
	if _, ok := lookupFieldValueWith("DOCVARIABLE", "Missing", v); ok {
		t.Error("missing var should miss")
	}
}

func TestDocPropertyLookup(t *testing.T) {
	v := fieldVars{docProperties: map[string]string{
		"Title":      "Annual Report",
		"AppVersion": "2.4.0",
	}}
	got, ok := lookupFieldValueWith("DOCPROPERTY", "AppVersion", v)
	if !ok || got != "2.4.0" {
		t.Errorf("DOCPROPERTY = (%q,%v)", got, ok)
	}
}

func TestCitationFormat(t *testing.T) {
	src := docx.BibSource{
		Tag:     "Smith2020",
		Title:   "Big Study",
		Year:    "2020",
		Authors: []string{"Smith"},
	}
	got := formatCitation(src)
	if got != "(Smith, 2020)" {
		t.Errorf("formatCitation = %q, want (Smith, 2020)", got)
	}
}

func TestBibliographyField(t *testing.T) {
	v := fieldVars{bibliography: map[string]docx.BibSource{
		"A": {Tag: "A", Authors: []string{"Adams"}, Year: "2018", Title: "Apples"},
		"B": {Tag: "B", Authors: []string{"Brown"}, Year: "2019", Title: "Bananas"},
	}}
	got, ok := lookupFieldValueWith("BIBLIOGRAPHY", "", v)
	if !ok {
		t.Fatal("BIBLIOGRAPHY: ok=false")
	}
	// Deterministic order by tag — Adams first.
	if !strings.HasPrefix(got, "Adams (2018). Apples") {
		t.Errorf("BIBLIOGRAPHY first line = %q", got)
	}
	if !strings.Contains(got, "Brown (2019). Bananas") {
		t.Errorf("BIBLIOGRAPHY missing second entry: %q", got)
	}
}

func TestTOCSynthesisFromHeadings(t *testing.T) {
	v := fieldVars{headings: []tocEntry{
		{Level: 1, Text: "Chapter 1"},
		{Level: 2, Text: "Section A"},
		{Level: 2, Text: "Section B"},
	}}
	got, ok := lookupFieldValueWith("TOC", "", v)
	if !ok {
		t.Fatal("TOC: ok=false")
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("TOC: %d lines, want 3 — %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "Chapter 1") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  Section A") {
		t.Errorf("line 1 = %q, want indented Section A", lines[1])
	}
}

func TestFlattenFields_MultiLineTOCSplits(t *testing.T) {
	// A TOC field with empty result region — the renderer should
	// synthesize entries from the heading list and split lines into
	// IsBreak-separated runs so the line layer sees real breaks.
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: "TOC"},
		{FieldSep: true},
		{Text: ""}, // empty cached result
		{FieldEnd: true},
	}
	v := fieldVars{headings: []tocEntry{
		{Level: 1, Text: "Intro"},
		{Level: 2, Text: "Background"},
	}}
	out := flattenFields(runs, v)
	// Expect: "Intro" run, IsBreak, "  Background" run.
	if len(out) != 3 {
		t.Fatalf("flatten: %d runs, want 3 — %+v", len(out), out)
	}
	if !strings.HasPrefix(out[0].Text, "Intro") {
		t.Errorf("run0 = %q", out[0].Text)
	}
	if !out[1].IsBreak {
		t.Errorf("run1 should be IsBreak: %+v", out[1])
	}
	if !strings.HasPrefix(out[2].Text, "  Background") {
		t.Errorf("run2 = %q", out[2].Text)
	}
}

func TestFieldDateLayoutSwitch(t *testing.T) {
	got := fieldDateLayoutSwitch(` DATE \@ "yyyy-MM-dd" `)
	if got != "yyyy-MM-dd" {
		t.Errorf("layout = %q, want yyyy-MM-dd", got)
	}
	got = fieldDateLayoutSwitch(` DATE \@ yyyy/MM/dd \* MERGEFORMAT `)
	if got != "yyyy/MM/dd" {
		t.Errorf("unquoted layout = %q", got)
	}
	got = fieldDateLayoutSwitch(` DATE `)
	if got != "" {
		t.Errorf("missing layout should be empty, got %q", got)
	}
}

func TestApplyWordDateLayout(t *testing.T) {
	tt, _ := time.Parse("2006-01-02 15:04", "2024-03-07 14:05")
	cases := []struct {
		layout, want string
	}{
		{"yyyy-MM-dd", "2024-03-07"},
		{"M/d/yyyy", "3/7/2024"},
		{"MMMM d, yyyy", "March 7, 2024"},
		{"h:mm AM/PM", "2:05 PM"},
	}
	for _, c := range cases {
		got := applyWordDateLayout(tt, c.layout)
		if got != c.want {
			t.Errorf("%q → %q, want %q", c.layout, got, c.want)
		}
	}
}

func TestFormatNumericSwitch(t *testing.T) {
	got := formatNumericSwitch(1234.5, ` \# "#,##0.00" `)
	if got != "1,234.50" {
		t.Errorf("formatted = %q, want 1,234.50", got)
	}
	got = formatNumericSwitch(7, ` \# "0000" `)
	if got != "0007" {
		t.Errorf("zero-pad = %q", got)
	}
}

func TestFormFieldOutput_Checkbox(t *testing.T) {
	ff := &docx.FormFieldInfo{Kind: "checkbox", Checked: true}
	got, ok := formFieldOutput(ff, "FORMCHECKBOX")
	if !ok || got != "☒" {
		t.Errorf("checked → (%q,%v), want (☒,true)", got, ok)
	}
	ff.Checked = false
	got, _ = formFieldOutput(ff, "FORMCHECKBOX")
	if got != "☐" {
		t.Errorf("unchecked → %q, want ☐", got)
	}
}

func TestFormFieldOutput_Dropdown(t *testing.T) {
	ff := &docx.FormFieldInfo{
		Kind:     "dropdown",
		Choices:  []string{"Red", "Green", "Blue"},
		Selected: 1,
	}
	got, ok := formFieldOutput(ff, "FORMDROPDOWN")
	if !ok || got != "Green ▾" {
		t.Errorf("dropdown → (%q,%v), want (\"Green ▾\",true)", got, ok)
	}
}

func TestPAGEREF_PageIndex(t *testing.T) {
	v := fieldVars{
		bookmarkPages: map[string]int{"Sec1": 7},
		bookmarks:     map[string]string{"Sec1": "Section One"},
	}
	got, ok := lookupFieldValueWith("PAGEREF", "Sec1", v)
	if !ok || got != "7" {
		t.Errorf("PAGEREF = (%q,%v), want (7,true)", got, ok)
	}
}

func TestSEQResetAndHidden(t *testing.T) {
	v := fieldVars{seqCounters: map[string]int{}}
	// First call increments to 1.
	got, _ := lookupFieldValueFull("SEQ", "Fig", "SEQ Fig", v)
	if got != "1" {
		t.Errorf("first SEQ = %q", got)
	}
	// \r 7 → 7
	got, _ = lookupFieldValueFull("SEQ", "Fig", `SEQ Fig \r 7`, v)
	if got != "7" {
		t.Errorf("reset SEQ = %q, want 7", got)
	}
	// \c → repeat 7
	got, _ = lookupFieldValueFull("SEQ", "Fig", `SEQ Fig \c`, v)
	if got != "7" {
		t.Errorf("repeat SEQ = %q, want 7", got)
	}
	// next → 8
	got, _ = lookupFieldValueFull("SEQ", "Fig", `SEQ Fig`, v)
	if got != "8" {
		t.Errorf("next SEQ = %q, want 8", got)
	}
	// \h → empty but counter advances to 9
	got, _ = lookupFieldValueFull("SEQ", "Fig", `SEQ Fig \h`, v)
	if got != "" {
		t.Errorf("hidden SEQ = %q, want empty", got)
	}
	if v.seqCounters["Fig"] != 9 {
		t.Errorf("hidden SEQ should still advance, counter = %d", v.seqCounters["Fig"])
	}
}

func TestSTYLEREFFirstMatch(t *testing.T) {
	v := fieldVars{styleParagraphs: map[string][]string{
		"Heading1": {"Chapter One", "Chapter Two"},
	}}
	got, ok := lookupFieldValueWith("STYLEREF", "Heading1", v)
	if !ok || got != "Chapter One" {
		t.Errorf("STYLEREF = (%q,%v), want (Chapter One,true)", got, ok)
	}
	if _, ok := lookupFieldValueWith("STYLEREF", "Missing", v); ok {
		t.Error("missing style should miss")
	}
}

func TestEDITTIME(t *testing.T) {
	v := fieldVars{totalMinutes: 75}
	got, ok := lookupFieldValueWith("EDITTIME", "", v)
	if !ok {
		t.Fatal("EDITTIME ok=false")
	}
	if !strings.Contains(got, "1h") {
		t.Errorf("EDITTIME = %q, want hour-minute form", got)
	}
}

func TestHeadingLevelDetection(t *testing.T) {
	doc := &docx.Document{}
	cases := []struct {
		styleID string
		outline int
		want    int
	}{
		{"Heading1", 0, 1},
		{"Heading3", 0, 3},
		{"Title", 0, 1},
		{"BodyText", 0, 0},
		{"", 2, 2},
		{"", 0, 0}, // unset
	}
	for _, c := range cases {
		p := docx.Paragraph{StyleID: c.styleID, OutlineLvl: c.outline}
		got := headingLevel(p, doc)
		if got != c.want {
			t.Errorf("headingLevel(style=%q outline=%d) = %d, want %d",
				c.styleID, c.outline, got, c.want)
		}
	}
}
