package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestRenderWriter_NoFont verifies the "FontRegular required" guard.
func TestRenderWriter_NoFont(t *testing.T) {
	doc := &docx.Document{}
	var buf bytes.Buffer
	err := RenderWriter(doc, &buf, Options{})
	if err == nil {
		t.Fatal("expected error when FontRegular is unset")
	}
	if !strings.Contains(err.Error(), "FontRegular") {
		t.Errorf("error message = %q, want it to mention FontRegular", err.Error())
	}
}

// TestRenderWriter_MissingFontFile verifies registerFonts surfaces the
// underlying filesystem error.
func TestRenderWriter_MissingFontFile(t *testing.T) {
	doc := &docx.Document{
		Body: []docx.Block{
			docx.Paragraph{Runs: []docx.Run{{Text: "hello"}}},
		},
	}
	var buf bytes.Buffer
	err := RenderWriter(doc, &buf, Options{FontRegular: "/does/not/exist.ttf"})
	if err == nil {
		t.Fatal("expected error for missing font file")
	}
}
