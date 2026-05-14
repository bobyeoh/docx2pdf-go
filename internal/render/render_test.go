package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestRenderWriter_NoFont covers the system-font auto-detection path.
// When Options.FontRegular is empty, RenderWriter falls back to a
// common system font. We assert one of two outcomes depending on the
// host: either the call SUCCEEDS (a system font was found and the
// document rendered), or the error message names the candidate paths
// we tried. Either outcome confirms the guard wires through the
// fallback rather than the old hard-fail.
func TestRenderWriter_NoFont(t *testing.T) {
	doc := &docx.Document{
		Body: []docx.Block{
			docx.Paragraph{Runs: []docx.Run{{Text: "hello"}}},
		},
	}
	var buf bytes.Buffer
	err := RenderWriter(doc, &buf, Options{})
	if err == nil {
		// Success path: a system font was discovered. Sanity-check
		// we produced something that looks like a PDF.
		if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
			t.Errorf("rendered output doesn't start with %%PDF: %q", buf.Bytes()[:min(8, len(buf.Bytes()))])
		}
		return
	}
	// Failure path: must call out FontRegular AND name a candidate
	// path so the caller knows what we tried.
	msg := err.Error()
	if !strings.Contains(msg, "FontRegular") {
		t.Errorf("error message = %q, want it to mention FontRegular", msg)
	}
	if !strings.Contains(msg, ".ttf") && !strings.Contains(msg, ".ttc") {
		t.Errorf("error message = %q, want it to list candidate font paths", msg)
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
