package convert

import (
	"archive/zip"
	"os"
)

// writeMinimalDocx writes a syntactically valid docx (a zip with a
// well-formed word/document.xml containing one paragraph) at path. Used by
// tests that need a parseable input but don't care about content.
func writeMinimalDocx(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	w, err := zw.Create("word/document.xml")
	if err != nil {
		return err
	}
	const body = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>hello</w:t></w:r></w:p>
  </w:body>
</w:document>`
	_, err = w.Write([]byte(body))
	return err
}
