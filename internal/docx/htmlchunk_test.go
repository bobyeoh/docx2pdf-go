package docx

import (
	"strings"
	"testing"
)

func TestParseHTMLAltChunk_Paragraphs(t *testing.T) {
	html := `<p>Hello <strong>bold</strong> and <em>italic</em>.</p>
<p>Second paragraph.</p>`
	blocks := parseHTMLAltChunk(html, RunProps{})
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	p1, ok := blocks[0].(Paragraph)
	if !ok {
		t.Fatalf("blocks[0] is %T not Paragraph", blocks[0])
	}
	gotBold := false
	gotItalic := false
	for _, r := range p1.Runs {
		if r.Props.Bold && strings.Contains(r.Text, "bold") {
			gotBold = true
		}
		if r.Props.Italic && strings.Contains(r.Text, "italic") {
			gotItalic = true
		}
	}
	if !gotBold {
		t.Errorf("missing bold run in %+v", p1.Runs)
	}
	if !gotItalic {
		t.Errorf("missing italic run in %+v", p1.Runs)
	}
}

func TestParseHTMLAltChunk_Heading(t *testing.T) {
	html := `<h2>Section Title</h2>`
	blocks := parseHTMLAltChunk(html, RunProps{FontSize: 11})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	p := blocks[0].(Paragraph)
	if p.StyleID != "Heading2" {
		t.Errorf("expected Heading2 style, got %q", p.StyleID)
	}
	if len(p.Runs) == 0 || !p.Runs[0].Props.Bold {
		t.Errorf("heading run should be bold")
	}
	if p.Runs[0].Props.FontSize <= 11 {
		t.Errorf("heading should be enlarged, got %v", p.Runs[0].Props.FontSize)
	}
}

func TestParseHTMLAltChunk_List(t *testing.T) {
	html := `<ul><li>one</li><li>two</li><li>three</li></ul>`
	blocks := parseHTMLAltChunk(html, RunProps{})
	if len(blocks) != 3 {
		t.Fatalf("expected 3 list items, got %d", len(blocks))
	}
	for i, b := range blocks {
		p := b.(Paragraph)
		if len(p.Runs) == 0 || !strings.HasPrefix(p.Runs[0].Text, "•") {
			t.Errorf("item %d missing bullet: %+v", i, p.Runs)
		}
	}
}

func TestParseHTMLAltChunk_Link(t *testing.T) {
	html := `<p>visit <a href="https://example.com">us</a> today</p>`
	blocks := parseHTMLAltChunk(html, RunProps{})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	p := blocks[0].(Paragraph)
	gotLink := false
	for _, r := range p.Runs {
		if r.LinkURL == "https://example.com" && r.Text == "us" {
			gotLink = true
		}
	}
	if !gotLink {
		t.Errorf("missing link run: %+v", p.Runs)
	}
}

func TestParseHTMLAltChunk_Entities(t *testing.T) {
	html := `<p>5 &lt; 6 &amp; 7 &gt; 4 &mdash; ok</p>`
	blocks := parseHTMLAltChunk(html, RunProps{})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	p := blocks[0].(Paragraph)
	all := ""
	for _, r := range p.Runs {
		all += r.Text
	}
	if !strings.Contains(all, "5 < 6 & 7 > 4 — ok") {
		t.Errorf("entities not decoded: %q", all)
	}
}
