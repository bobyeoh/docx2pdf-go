// Package docx2pdf is a pure-Go library that converts Microsoft Word .docx
// files to PDF. It is the public surface of github.com/bobyeoh/docx2pdf-go;
// the internal packages remain unexported so we can refactor them freely
// without breaking importers.
//
// Three entry points cover most workflows:
//
//   - Convert(inPath, outPath, opts)    — file path in, file path out
//   - ConvertReader(r, size, w, opts)   — io.ReaderAt → io.Writer (streaming)
//   - Open + Render                     — parse once, render N times, or
//     inspect/modify the AST in between
//
// Minimal example:
//
//	err := docx2pdf.Convert("in.docx", "out.pdf", docx2pdf.Options{
//	    FontRegular:  "/path/to/regular.ttf",
//	    FontFallback: "/path/to/NotoSansCJK.ttc", // optional, for CJK
//	})
//
// In-memory pipeline (e.g. an HTTP handler):
//
//	data, _ := io.ReadAll(req.Body)
//	var pdf bytes.Buffer
//	err := docx2pdf.ConvertReader(bytes.NewReader(data), int64(len(data)),
//	    &pdf, docx2pdf.Options{FontRegular: fontPath})
//
// Two-step (parse → inspect → render):
//
//	doc, err := docx2pdf.Open("in.docx")
//	// inspect doc.Body / doc.Sections / doc.Styles / doc.Numbering ...
//	err = docx2pdf.Render(doc, "out.pdf", docx2pdf.Options{FontRegular: ...})
package docx2pdf

import (
	"context"
	"io"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/bobyeoh/docx2pdf-go/internal/render"
)

// Options controls font selection, page numbering, and verbose tracing.
// The zero value is invalid — FontRegular must be set to a TTF/TTC path.
type Options = convert.Options

// Document is the parsed in-memory representation of a .docx file. Callers
// can introspect or modify its Body / Sections / Styles / Numbering / Images
// fields before handing it to Render.
type Document = docx.Document

// --- AST type aliases (so external callers can type-assert against the
//     contents of Document.Body / Section.Blocks) ----------------------

type (
	Block          = docx.Block
	Paragraph      = docx.Paragraph
	Run            = docx.Run
	RunProps       = docx.RunProps
	Alignment      = docx.Alignment
	Table          = docx.Table
	TableRow       = docx.TableRow
	TableCell      = docx.TableCell
	ListInfo       = docx.ListInfo
	Numbering      = docx.Numbering
	AbstractNum    = docx.AbstractNum
	NumLevel       = docx.NumLevel
	ParagraphStyle = docx.ParagraphStyle
	ParaDefaults   = docx.ParaDefaults
	LineHeight     = docx.LineHeight
	Section        = docx.Section
	PageSize       = docx.PageSize
	Margins        = docx.Margins
)

// Alignment values.
const (
	AlignLeft    = docx.AlignLeft
	AlignCenter  = docx.AlignCenter
	AlignRight   = docx.AlignRight
	AlignJustify = docx.AlignJustify
)

// A4Twips is the standard A4 page size in twips.
var A4Twips = docx.A4Twips

// DefaultMarginsTwips is the typical 1-inch margin (1440 twips) on each side.
var DefaultMarginsTwips = docx.DefaultMarginsTwips

// Convert reads a docx at inPath and writes the rendered PDF to outPath.
// Most callers want this.
func Convert(inPath, outPath string, opts Options) error {
	return convert.Convert(inPath, outPath, opts)
}

// ConvertReader is the streaming variant: parse the docx from r (size bytes
// total) and write the resulting PDF to w. Use this for in-memory pipelines,
// HTTP handlers, or any non-file source/sink.
func ConvertReader(r io.ReaderAt, size int64, w io.Writer, opts Options) error {
	return convert.ConvertReader(r, size, w, opts)
}

// Open parses a docx file at path without rendering. The returned Document
// may be inspected, mutated, then passed to Render.
func Open(path string) (*Document, error) {
	return docx.Open(path)
}

// Parse is the streaming variant of Open. The reader must support random
// access (io.ReaderAt) because the docx format is a ZIP archive.
func Parse(r io.ReaderAt, size int64) (*Document, error) {
	return docx.Parse(r, size)
}

// Render writes a parsed Document to outPath as PDF.
func Render(doc *Document, outPath string, opts Options) error {
	return render.Render(doc, outPath, opts)
}

// RenderWriter writes a parsed Document as PDF to w.
func RenderWriter(doc *Document, w io.Writer, opts Options) error {
	return render.RenderWriter(doc, w, opts)
}

// ConvertContext is Convert with cancellation. The context is checked at
// each section boundary, so cancellation interrupts within a few hundred
// milliseconds even for long documents.
func ConvertContext(ctx context.Context, inPath, outPath string, opts Options) error {
	return convert.ConvertContext(ctx, inPath, outPath, opts)
}

// ConvertReaderContext is ConvertReader with cancellation.
func ConvertReaderContext(ctx context.Context, r io.ReaderAt, size int64, w io.Writer, opts Options) error {
	return convert.ConvertReaderContext(ctx, r, size, w, opts)
}
