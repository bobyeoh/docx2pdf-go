// Package verify is an end-to-end verification harness. It generates synthetic
// docx files exercising specific features, runs them through the convert
// pipeline, extracts the resulting PDF text via pdftotext, asserts expected
// substrings, then renders each page to PNG via pdftoppm for visual review.
//
// Run with:
//
//	go test ./internal/verify/... -v
//
// PNG snapshots are written to internal/verify/out/<case>/page-N.png, plus
// an index.html that puts every page side-by-side with pass/fail badges.
package verify

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// docxBuilder constructs a minimal docx zip in memory. It exposes one method
// per package part we care about; unset parts are simply omitted from the zip.
type docxBuilder struct {
	files map[string]string
}

func newDocx() *docxBuilder {
	return &docxBuilder{files: map[string]string{}}
}

func (b *docxBuilder) Body(inner string) *docxBuilder {
	b.files["word/document.xml"] = wrapBody(inner)
	return b
}

func (b *docxBuilder) RawBody(xml string) *docxBuilder {
	b.files["word/document.xml"] = xml
	return b
}

func (b *docxBuilder) Numbering(xml string) *docxBuilder {
	b.files["word/numbering.xml"] = xml
	return b
}

func (b *docxBuilder) Styles(xml string) *docxBuilder {
	b.files["word/styles.xml"] = xml
	return b
}

func (b *docxBuilder) Part(name, xml string) *docxBuilder {
	b.files["word/"+name] = xml
	return b
}

// RawFile writes content at an arbitrary path inside the zip — bypassing the
// "word/" prefix. Used for docProps/core.xml and similar package-root files.
func (b *docxBuilder) RawFile(path, content string) *docxBuilder {
	b.files[path] = content
	return b
}

func (b *docxBuilder) Rels(xml string) *docxBuilder {
	b.files["word/_rels/document.xml.rels"] = xml
	return b
}

func (b *docxBuilder) Media(name string, content []byte) *docxBuilder {
	b.files["word/media/"+name] = string(content)
	return b
}

// Write produces the docx file at dir/test.docx and returns its path.
func (b *docxBuilder) Write(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	for name, content := range b.files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

const docHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
    xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
    xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>`

const docFooter = `  </w:body></w:document>`

func wrapBody(inner string) string {
	return docHeader + inner + docFooter
}

// makeSolidPNG returns a tiny solid-color PNG, used as test image data.
func makeSolidPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// makeSolidJPEG returns a tiny solid-color JPEG, used to exercise the JPEG
// decode path of the parser.
func makeSolidJPEG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

// makeTransparentPNG returns a PNG with a half-transparent rectangle so we
// stress the alpha-aware encode path inside the renderer.
func makeTransparentPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			alpha := uint8(0)
			if x > w/3 && x < 2*w/3 {
				alpha = 200
			}
			img.Set(x, y, color.RGBA{R: 50, G: 50, B: 200, A: alpha})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
