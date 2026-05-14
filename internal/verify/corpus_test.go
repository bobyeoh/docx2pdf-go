package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
)

// TestRealWorldCorpus runs the docx2pdf binary over real Word documents
// shipped with the docx4j project (when accessible). These are the most
// realistic regression catch — anything Word writes that we don't handle
// will likely surface here.
//
// Skipped when ../../../docx4j/docs/ isn't reachable so the suite still
// runs in environments without that sibling checkout.
func TestRealWorldCorpus(t *testing.T) {
	requireTool(t, "pdftotext")
	requireTool(t, "pdfinfo")

	fontPath := findFont(t)

	docsDir, err := filepath.Abs("../../../docx4j/docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("docx4j/docs not found at %s — corpus test skipped", docsDir)
	}

	// Each entry: filename + a substring we expect to survive into the PDF.
	// We intentionally pick distinctive strings to avoid coincidental matches.
	corpus := []struct {
		file     string
		mustHave string
	}{
		{"Docx4j_GettingStarted.docx", "docx4j"},
		{"Docx4j_Russian.docx", "docx4j"},
		{"Bookmark_crossrefs.docx", "Bookmark"},
		{"headers_footers.docx", "header"},
		{"OpenDoPE_Images.docx", "Open"},
		{"OpenDoPE_XHTML.docx", "Open"},
	}

	outRoot := mustAbs(t, "out_corpus")
	if err := os.RemoveAll(outRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, entry := range corpus {
		entry := entry
		src := filepath.Join(docsDir, entry.file)
		if _, err := os.Stat(src); err != nil {
			continue // gracefully skip docs that aren't shipped
		}
		t.Run(entry.file, func(t *testing.T) {
			dir := t.TempDir()
			pdf := filepath.Join(dir, "out.pdf")
			opts := convert.Options{
				FontRegular:     fontPath,
				FontFallback:    fontPath, // works for ASCII-heavy docs
				DefaultFontSize: 11,
			}
			if err := convert.Convert(src, pdf, opts); err != nil {
				t.Fatalf("convert: %v", err)
			}
			// Smoke-test: extract text and confirm a distinctive substring
			// survived from the original document. Subjective rendering
			// quality is left to the PNG snapshots.
			txt := pdftotext(t, pdf)
			if !contains(txt, entry.mustHave) {
				t.Errorf("expected substring %q in %s output", entry.mustHave, entry.file)
			}
			// Save a snapshot for visual review.
			caseOut := filepath.Join(outRoot, sanitize(entry.file))
			_ = os.MkdirAll(caseOut, 0o755)
			_ = copyFile(pdf, filepath.Join(caseOut, "out.pdf"))
			_ = renderPNGFirstOnly(t, pdf, caseOut)
		})
	}
}

// renderPNGFirstOnly renders only page 1 to keep corpus snapshots compact.
// Some corpus docs are dozens of pages; we don't need all of them as PNG.
func renderPNGFirstOnly(t *testing.T, pdf, outDir string) error {
	t.Helper()
	return runCmd("pdftoppm", "-png", "-r", "72", "-l", "1", pdf, filepath.Join(outDir, "page"))
}

func runCmd(name string, args ...string) error {
	out, err := combinedOutput(name, args...)
	if err != nil {
		// Treat pdftoppm errors as soft — corpus tests are still meaningful
		// even if snapshot rendering fails.
		_ = out
		return err
	}
	return nil
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func sanitize(name string) string {
	// Strip extension and replace spaces with underscores so the directory
	// name is filesystem-safe and predictable.
	base := name
	if dot := lastIndex(name, '.'); dot >= 0 {
		base = name[:dot]
	}
	out := make([]byte, 0, len(base))
	for i := 0; i < len(base); i++ {
		c := base[i]
		switch {
		case c == ' ' || c == '/' || c == '\\':
			out = append(out, '_')
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

func lastIndex(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
