package verify

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// combinedOutput is a tiny shim so the test file can compile without pulling
// os/exec into multiple places. Returned stdout+stderr; error if non-zero.
func combinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// TestCrashResistance feeds the parser a series of broken / pathological
// inputs and asserts the package never panics. An error return is fine —
// the requirement is "graceful failure," not "always succeed."
func TestCrashResistance(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{
			name: "empty_zip",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "empty.docx")
				f, err := os.Create(p)
				if err != nil {
					t.Fatal(err)
				}
				zw := zip.NewWriter(f)
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
		{
			name: "missing_document_xml",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "no_doc.docx")
				f, _ := os.Create(p)
				zw := zip.NewWriter(f)
				// Plenty of content but no word/document.xml.
				w, _ := zw.Create("word/styles.xml")
				_, _ = w.Write([]byte(`<?xml version="1.0"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`))
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
		{
			name: "malformed_xml",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "bad_xml.docx")
				f, _ := os.Create(p)
				zw := zip.NewWriter(f)
				w, _ := zw.Create("word/document.xml")
				_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>unclosed`))
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
		{
			name: "circular_basedOn",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "cycle.docx")
				f, _ := os.Create(p)
				zw := zip.NewWriter(f)
				addZipFile(t, zw, "word/document.xml", `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>hi</w:t></w:r></w:p></w:body>
</w:document>`)
				addZipFile(t, zw, "word/styles.xml", `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="A">
    <w:basedOn w:val="B"/></w:style>
  <w:style w:type="paragraph" w:styleId="B">
    <w:basedOn w:val="A"/></w:style>
</w:styles>`)
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
		{
			name: "corrupt_image_bytes",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "bad_img.docx")
				f, _ := os.Create(p)
				zw := zip.NewWriter(f)
				addZipFile(t, zw, "word/document.xml", `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
    xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
    xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
    <w:p><w:r><w:t>still text here</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
  </w:body>
</w:document>`)
				addZipFile(t, zw, "word/media/bad.png", "not a png — just garbage bytes")
				addZipFile(t, zw, "word/_rels/document.xml.rels", `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/bad.png"/>
</Relationships>`)
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
		{
			name: "deeply_nested_xml",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				p := filepath.Join(dir, "deep.docx")
				f, _ := os.Create(p)
				zw := zip.NewWriter(f)
				// 500 levels of nested w:hyperlink wrapping — paragraph
				// decoder must not blow up on deep recursion.
				var open, close bytes.Buffer
				for i := 0; i < 500; i++ {
					open.WriteString(`<w:hyperlink>`)
					close.WriteString(`</w:hyperlink>`)
				}
				body := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p>` + open.String() +
					`<w:r><w:t>deep</w:t></w:r>` +
					close.String() + `</w:p></w:body></w:document>`
				addZipFile(t, zw, "word/document.xml", body)
				_ = zw.Close()
				_ = f.Close()
				return p
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("docx.Open panicked on %s: %v", c.name, r)
				}
			}()
			path := c.setup(t)
			// We don't care whether Open returns an error — only that it
			// doesn't panic and doesn't hang. If it succeeds, we additionally
			// require the result to be usable enough not to nil-deref.
			doc, err := docx.Open(path)
			if err != nil {
				return // expected for many of these
			}
			if doc == nil {
				t.Fatalf("Open returned (nil, nil) for %s", c.name)
			}
			// Touch a few fields to make sure they're sane.
			_ = doc.Body
			_ = doc.Sections
			_ = doc.HeaderBlocks
			_ = doc.FooterBlocks
		})
	}
}

func addZipFile(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
