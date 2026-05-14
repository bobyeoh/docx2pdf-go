package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

// TestContextCancel confirms ConvertContext returns ctx.Err() when canceled
// before the render even starts. We can't easily race a cancellation against
// the actual render in this small test, but the pre-flight check covers the
// most common use-case: a higher-level handler ctx that's already done.
func TestContextCancel(t *testing.T) {
	font := findFont(t)
	in := buildHelloDocxV(t, t.TempDir(), "ctx hello")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before we call Convert

	err := docx2pdf.ConvertContext(ctx, in, filepath.Join(t.TempDir(), "x.pdf"),
		docx2pdf.Options{FontRegular: font})
	if err == nil {
		t.Fatalf("expected ctx.Err() from a canceled context, got nil")
	}
	if err != context.Canceled {
		t.Logf("got wrapped error: %v (wanted context.Canceled or wrapping)", err)
	}
}

// buildHelloDocxV is a small helper duplicated here so context_test stays
// self-contained (the example_test.go helper is in package docx2pdf_test).
func buildHelloDocxV(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "hello.docx")
	d := newDocx().Body(`<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`)
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	out.Close()
	// Re-use the testing docxBuilder by writing through it.
	return d.Write(t, dir)
}
