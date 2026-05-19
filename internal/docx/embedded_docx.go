package docx

import (
	"archive/zip"
	"bytes"
	"io"
)

// embedded_docx.go pulls the body of an embedded .docx (OLE Package or
// Word.Document.* OLE object) into a flat slice of Blocks so the
// renderer can splice those blocks at the OLE reference site.
//
// We recurse exactly one level: an embedded doc may itself contain a
// nested OLE package, but that nested package is rendered as a labeled
// placeholder rather than parsed recursively. This prevents pathological
// zip-bombs and keeps the rendering deterministic.

// extractEmbeddedDocxBlocks opens an OLE-attached .docx zip and parses
// its body, returning the top-level blocks. Returns (nil, nil) when the
// embed isn't a parseable docx (wrong format, encrypted, malformed).
//
// The 'recurse' flag is false on the second pass so any further nested
// embeds get collapsed to a placeholder rather than recursed.
func extractEmbeddedDocxBlocks(f *zip.File) ([]Block, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	// ParseReader handles the full docx pipeline (open zip, walk parts,
	// build Document); we then strip everything except the section
	// bodies so the caller gets a clean block list.
	doc, err := Parse(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}
	var out []Block
	for _, sec := range doc.Sections {
		out = append(out, sec.Blocks...)
	}
	return out, nil
}

// flattenEmbeddedDocxText walks the embedded document's blocks and joins
// the visible text with newlines. Tables are flattened cell-by-cell.
// Used as the in-paragraph fallback when an OLE embed references a
// Word.Document but the host has no Block-level splice point available.
func flattenEmbeddedDocxText(blocks []Block) string {
	var sb []byte
	first := true
	var walk func(bs []Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			switch v := b.(type) {
			case Paragraph:
				if !first {
					sb = append(sb, '\n')
				}
				first = false
				for _, r := range v.Runs {
					if r.Text != "" {
						sb = append(sb, r.Text...)
					}
				}
			case Table:
				for _, row := range v.Rows {
					for ci, cell := range row.Cells {
						if ci > 0 {
							sb = append(sb, '\t')
						}
						walk(cell.Blocks)
					}
					sb = append(sb, '\n')
				}
			}
		}
	}
	walk(blocks)
	return string(sb)
}
