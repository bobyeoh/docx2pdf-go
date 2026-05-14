package verify

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// FuzzDocxOpen mutates known-good docx bytes and feeds them into the parser
// looking for panics. We seed with our hand-built samples plus a couple of
// docx4j corpus files when available, then let the Go fuzzer mutate.
//
// Run with:
//
//	go test ./internal/verify/... -run=^$ -fuzz=FuzzDocxOpen -fuzztime=30s
//
// The package's other tests are excluded via -run=^$ so the fuzzer doesn't
// also run the heavy verify suite for every mutation.
func FuzzDocxOpen(f *testing.F) {
	seeds := []string{
		"../../testdata/sample_zh.docx",
		"../../testdata/sample_hf.docx",
		"../../testdata/sample.docx",
		"../../../docx4j/docs/Docx4j_Russian.docx",
	}
	for _, p := range seeds {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Bound the input size so we don't burn time on multi-MB inputs the
		// fuzzer would otherwise wander into. Real docx files are typically
		// 5 KB – 200 KB.
		if len(data) == 0 || len(data) > 1<<20 {
			return
		}

		dir := t.TempDir()
		path := filepath.Join(dir, "fuzz.docx")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("docx.Open panicked: %v", r)
			}
		}()
		// We don't care whether Open succeeds — only that it doesn't panic.
		_, _ = docx.Open(path)
	})
}

// FuzzInMemoryDocx is a complementary fuzzer that builds a *valid* zip envelope
// around fuzzer-supplied bytes for document.xml. This finds parser bugs the
// outer-zip fuzz would miss because the zip wrapping is usually still valid.
func FuzzInMemoryDocx(f *testing.F) {
	seeds := []string{
		`<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>hi</w:t></w:r></w:p></w:body></w:document>`,
		`<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:tbl><w:tblGrid><w:gridCol w:w="500"/></w:tblGrid><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, docXML []byte) {
		// Build a zip with the fuzzer's bytes as word/document.xml.
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("word/document.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(docXML); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		path := filepath.Join(t.TempDir(), "fuzz.docx")
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("docx.Open panicked on fuzzed XML: %v", r)
			}
		}()
		_, _ = docx.Open(path)
	})
}
