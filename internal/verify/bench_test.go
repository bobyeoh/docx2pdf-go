package verify

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// BenchmarkParseSmall measures parser-only throughput on a small in-memory
// docx. Useful for catching regressions in the XML / zip pipeline.
func BenchmarkParseSmall(b *testing.B) {
	docxPath := buildBenchDoc(b, smallBody())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := docx.Open(docxPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseLarge exercises the parser on a 500-paragraph doc — the
// hot path for real-world long documents.
func BenchmarkParseLarge(b *testing.B) {
	docxPath := buildBenchDoc(b, largeBody(500))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := docx.Open(docxPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvertSmall measures the full parse-and-render pipeline on a
// small doc. A typical regression catcher for the renderer side.
func BenchmarkConvertSmall(b *testing.B) {
	font := findFontForBench(b)
	docxPath := buildBenchDoc(b, smallBody())
	outDir := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(outDir, fmt.Sprintf("o%d.pdf", i))
		if err := convert.Convert(docxPath, out, convert.Options{
			FontRegular:     font,
			DefaultFontSize: 11,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConvert500 measures the full pipeline at scale.
func BenchmarkConvert500(b *testing.B) {
	font := findFontForBench(b)
	docxPath := buildBenchDoc(b, largeBody(500))
	outDir := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(outDir, fmt.Sprintf("o%d.pdf", i))
		if err := convert.Convert(docxPath, out, convert.Options{
			FontRegular:     font,
			DefaultFontSize: 11,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func buildBenchDoc(tb testing.TB, body string) string {
	tb.Helper()
	dir := tb.TempDir()
	path := filepath.Join(dir, "b.docx")
	d := newDocxFromXML(wrapBody(body))
	if err := writeBenchDocx(path, d); err != nil {
		tb.Fatal(err)
	}
	return path
}

// newDocxFromXML / writeBenchDocx are slim adapters so benchmarks avoid the
// testing.TB-only Write path on docxBuilder.
func newDocxFromXML(docXML string) map[string]string {
	return map[string]string{"word/document.xml": docXML}
}

func writeBenchDocx(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func smallBody() string {
	return `<w:p><w:r><w:t>Hello bench world.</w:t></w:r></w:p>
<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>second bold paragraph</w:t></w:r></w:p>`
}

func largeBody(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<w:p><w:r><w:t>paragraph %d in bench run.</w:t></w:r></w:p>`, i)
	}
	return b.String()
}

func findFontForBench(b *testing.B) string {
	b.Helper()
	for _, p := range []string{"../../testdata/font.ttf", "../../../testdata/font.ttf"} {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	b.Skip("testdata/font.ttf not found")
	return ""
}
