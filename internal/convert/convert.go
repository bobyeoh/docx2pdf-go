// Package convert orchestrates docx → PDF: parse → render.
//
// This is the equivalent of docx4j's Docx4J.toPDF(): a single entry point
// that takes an input and produces a PDF. Both file-path and io.Reader/
// io.Writer signatures are provided so library callers can pick whichever
// fits their pipeline.
package convert

import (
	"context"
	"fmt"
	"io"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/bobyeoh/docx2pdf-go/internal/render"
)

type Options = render.Options

// Convert reads a docx file from inPath and writes the rendered PDF to
// outPath. The simplest entry point for users who already have file paths.
func Convert(inPath, outPath string, opts Options) error {
	if opts.SourceFilename == "" {
		opts.SourceFilename = inPath
	}
	doc, err := docx.Open(inPath)
	if err != nil {
		return fmt.Errorf("parse docx: %w", err)
	}
	if opts.Verbose {
		fmt.Printf("parsed: %d blocks, %d images, page %dx%d twips\n",
			len(doc.Body), len(doc.Images),
			doc.PageSize.WidthTwips, doc.PageSize.HeightTwips)
	}
	if err := render.Render(doc, outPath, opts); err != nil {
		return fmt.Errorf("render pdf: %w", err)
	}
	return nil
}

// ConvertReader is the streaming variant. It parses the docx from an
// io.ReaderAt (of the given size) and writes the PDF to w. Useful for
// in-memory pipelines, HTTP handlers, or any non-file source/sink.
func ConvertReader(r io.ReaderAt, size int64, w io.Writer, opts Options) error {
	doc, err := docx.Parse(r, size)
	if err != nil {
		return fmt.Errorf("parse docx: %w", err)
	}
	if err := render.RenderWriter(doc, w, opts); err != nil {
		return fmt.Errorf("render pdf: %w", err)
	}
	return nil
}

// ConvertContext is Convert with cancellation. Returns ctx.Err() promptly
// if the context is canceled before / between section boundaries.
func ConvertContext(ctx context.Context, inPath, outPath string, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.SourceFilename == "" {
		opts.SourceFilename = inPath
	}
	doc, err := docx.Open(inPath)
	if err != nil {
		return fmt.Errorf("parse docx: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := render.RenderWithContext(ctx, doc, outPath, opts); err != nil {
		return fmt.Errorf("render pdf: %w", err)
	}
	return nil
}

// ConvertReaderContext is ConvertReader with cancellation.
func ConvertReaderContext(ctx context.Context, r io.ReaderAt, size int64, w io.Writer, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	doc, err := docx.Parse(r, size)
	if err != nil {
		return fmt.Errorf("parse docx: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := render.RenderWriterWithContext(ctx, doc, w, opts); err != nil {
		return fmt.Errorf("render pdf: %w", err)
	}
	return nil
}
