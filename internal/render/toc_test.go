package render

import (
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestHeadingLevel covers the styleID → level mapping. Style IDs come
// in several flavors: bare "Heading1", spaced "Heading 1", lower-case
// "heading1", and "Title" which is treated as level 0.
func TestHeadingLevel(t *testing.T) {
	cases := []struct {
		id   string
		want int
	}{
		{"Heading1", 1},
		{"Heading 3", 3},
		{"heading7", 7},
		{"Heading9", 9},
		{"Heading", 1}, // bare "Heading" → level 1
		{"Title", 0},
		{"title", 0},
		{"BodyText", -1},
		{"", -1},
	}
	for _, c := range cases {
		if got := headingLevel(c.id); got != c.want {
			t.Errorf("headingLevel(%q) = %d, want %d", c.id, got, c.want)
		}
	}
}

// TestParagraphHasTOCField confirms detection of a TOC field via the
// instrText run content.
func TestParagraphHasTOCField(t *testing.T) {
	p := docx.Paragraph{
		Runs: []docx.Run{
			{FieldBegin: true},
			{InstrText: ` TOC \o "1-3" \h `},
			{FieldSep: true},
			{Text: "cached"},
			{FieldEnd: true},
		},
	}
	if !paragraphHasTOCField(p) {
		t.Errorf("TOC field not detected")
	}
	plain := docx.Paragraph{Runs: []docx.Run{{Text: "hello"}}}
	if paragraphHasTOCField(plain) {
		t.Errorf("plain paragraph misdetected as TOC field")
	}
}

// TestHasTOCField walks the doc-level helper used by RenderWriter.
func TestHasTOCField(t *testing.T) {
	doc := &docx.Document{
		Body: []docx.Block{
			docx.Paragraph{Runs: []docx.Run{{Text: "header"}}},
			docx.Paragraph{Runs: []docx.Run{
				{FieldBegin: true},
				{InstrText: "TOC"},
				{FieldSep: true},
				{FieldEnd: true},
			}},
		},
	}
	if !hasTOCField(doc) {
		t.Errorf("hasTOCField missed TOC in Body")
	}

	clean := &docx.Document{Body: []docx.Block{
		docx.Paragraph{Runs: []docx.Run{{Text: "header"}}},
	}}
	if hasTOCField(clean) {
		t.Errorf("hasTOCField false positive on TOC-free doc")
	}
}

// TestCollectHeadings confirms only heading-styled paragraphs surface
// as TOC entries, with text trimmed and level resolved.
func TestCollectHeadings(t *testing.T) {
	doc := &docx.Document{
		Body: []docx.Block{
			docx.Paragraph{StyleID: "Title", Runs: []docx.Run{{Text: "Doc Title"}}},
			docx.Paragraph{StyleID: "Heading1", Runs: []docx.Run{{Text: "Chapter One"}}},
			docx.Paragraph{StyleID: "Heading 2", Runs: []docx.Run{{Text: "Section 1.1"}}},
			docx.Paragraph{StyleID: "Normal", Runs: []docx.Run{{Text: "Body text"}}},
			docx.Paragraph{StyleID: "Heading3", Runs: []docx.Run{{Text: "Detail"}}},
		},
	}
	got := collectHeadings(doc)
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(got), got)
	}
	wantTitles := []string{"Doc Title", "Chapter One", "Section 1.1", "Detail"}
	wantLevels := []int{0, 1, 2, 3}
	for i, e := range got {
		if e.Title != wantTitles[i] {
			t.Errorf("[%d] title = %q, want %q", i, e.Title, wantTitles[i])
		}
		if e.Level != wantLevels[i] {
			t.Errorf("[%d] level = %d, want %d", i, e.Level, wantLevels[i])
		}
	}
}

// TestBuildTOCParagraph confirms the constructed TOC entry has the
// expected three-run shape: title + dot leader + page.
func TestBuildTOCParagraph(t *testing.T) {
	p := buildTOCParagraph(tocEntry{Level: 2, Title: "Section A", Page: 12})
	if len(p.Runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(p.Runs))
	}
	if p.Runs[0].Text != "Section A" {
		t.Errorf("title run = %q, want %q", p.Runs[0].Text, "Section A")
	}
	if !strings.Contains(p.Runs[1].Text, "...") {
		t.Errorf("dot-leader run = %q, want it to contain dots", p.Runs[1].Text)
	}
	if p.Runs[2].Text != "12" {
		t.Errorf("page run = %q, want 12", p.Runs[2].Text)
	}
	if p.IndentLeftPt <= 0 {
		t.Errorf("level-2 entry should be indented, got %v", p.IndentLeftPt)
	}
}

// TestSpliceTOCBlocks replaces the TOC-bearing paragraph with the
// supplied entries; non-TOC paragraphs pass through.
func TestSpliceTOCBlocks(t *testing.T) {
	blocks := []docx.Block{
		docx.Paragraph{Runs: []docx.Run{{Text: "before"}}},
		docx.Paragraph{Runs: []docx.Run{
			{FieldBegin: true},
			{InstrText: "TOC"},
			{FieldSep: true},
			{FieldEnd: true},
		}},
		docx.Paragraph{Runs: []docx.Run{{Text: "after"}}},
	}
	replacement := []docx.Block{
		docx.Paragraph{Runs: []docx.Run{{Text: "Heading A"}}},
		docx.Paragraph{Runs: []docx.Run{{Text: "Heading B"}}},
	}
	out := spliceTOCBlocks(blocks, replacement)
	if len(out) != 4 {
		t.Fatalf("got %d blocks, want 4 (before, A, B, after): %+v", len(out), out)
	}
	if out[0].(docx.Paragraph).Runs[0].Text != "before" ||
		out[3].(docx.Paragraph).Runs[0].Text != "after" {
		t.Errorf("non-TOC blocks not preserved: %+v", out)
	}
	if out[1].(docx.Paragraph).Runs[0].Text != "Heading A" ||
		out[2].(docx.Paragraph).Runs[0].Text != "Heading B" {
		t.Errorf("TOC entries not spliced: %+v", out)
	}
}

func TestTOCKey(t *testing.T) {
	a := tocKey("Intro", "Heading1")
	b := tocKey("Intro", "Heading2")
	if a == b {
		t.Errorf("tocKey collided on same title, different style: %q == %q", a, b)
	}
}
