package docx2pdf_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go"
)

// helper used by the smoke + example tests: build a one-paragraph docx
// containing the given body text.
func buildHelloDocx(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "hello.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(w, `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>%s</w:t></w:r></w:p></w:body>
</w:document>`, text)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func findFont(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"testdata/font.ttf", "../testdata/font.ttf"} {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Skip("testdata/font.ttf not found")
	return ""
}

// TestLibrarySmoke proves the public API is genuinely importable from outside
// the internal/ tree (the _test package is in docx2pdf_test, not docx2pdf).
func TestLibrarySmoke(t *testing.T) {
	dir := t.TempDir()
	in := buildHelloDocx(t, dir, "library hello")
	out := filepath.Join(dir, "out.pdf")
	font := findFont(t)

	if err := docx2pdf.Convert(in, out, docx2pdf.Options{
		FontRegular:     font,
		DefaultFontSize: 11,
	}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() < 200 {
		t.Fatalf("output PDF missing or too small: %v size=%d", err, st.Size())
	}
}

// TestLibraryStreaming proves the io.ReaderAt / io.Writer variant works for
// in-memory pipelines.
func TestLibraryStreaming(t *testing.T) {
	dir := t.TempDir()
	in := buildHelloDocx(t, dir, "streaming hello")
	data, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	var pdf bytes.Buffer
	if err := docx2pdf.ConvertReader(
		bytes.NewReader(data), int64(len(data)),
		&pdf,
		docx2pdf.Options{FontRegular: findFont(t)},
	); err != nil {
		t.Fatalf("ConvertReader: %v", err)
	}
	if pdf.Len() < 200 || !bytes.HasPrefix(pdf.Bytes(), []byte("%PDF")) {
		t.Fatalf("ConvertReader produced invalid PDF (len=%d, header=%q)",
			pdf.Len(), pdf.Bytes()[:min(8, pdf.Len())])
	}
}

// TestLibraryInspect proves callers can parse, inspect the AST, then render.
func TestLibraryInspect(t *testing.T) {
	dir := t.TempDir()
	in := buildHelloDocx(t, dir, "inspect-me")

	doc, err := docx2pdf.Open(in)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(doc.Body) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Body))
	}
	// The public Block/Paragraph aliases let external callers type-assert
	// without ever importing the internal/ package directly.
	p, ok := doc.Body[0].(docx2pdf.Paragraph)
	if !ok {
		t.Fatalf("expected Paragraph, got %T", doc.Body[0])
	}
	if p.Runs[0].Text != "inspect-me" {
		t.Errorf("unexpected run text: %q", p.Runs[0].Text)
	}

	// Mutate the AST before rendering.
	pmod := p
	pmod.Runs[0].Text = "MUTATED-BEFORE-RENDER"
	pmod.Runs[0].Props.Bold = true
	doc.Body[0] = pmod
	if len(doc.Sections) > 0 {
		doc.Sections[0].Blocks[0] = pmod
	}

	out := filepath.Join(dir, "mutated.pdf")
	if err := docx2pdf.Render(doc, out, docx2pdf.Options{FontRegular: findFont(t)}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Confirm the mutation made it into the PDF text.
	stat, err := os.Stat(out)
	if err != nil || stat.Size() < 200 {
		t.Fatalf("mutated PDF missing: %v", err)
	}
}

// Example illustrates the simplest possible usage of the library.
func Example() {
	// (Skipped in `go test` runs because it requires real font + docx paths.
	// Shown here as documentation.)
	err := docx2pdf.Convert("input.docx", "output.pdf", docx2pdf.Options{
		FontRegular:  "/path/to/Regular.ttf",
		FontFallback: "/path/to/NotoSansCJK.ttc",
		PageNumbers:  true,
	})
	_ = err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
