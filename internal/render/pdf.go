// Package render walks the docx AST and writes a PDF via github.com/signintech/gopdf.
//
// Design parallels docx4j's FOExporterVisitor: a visitor walks blocks and
// emits drawing operations. Unlike docx4j we do not go through an
// intermediate XSL-FO document — we draw to PDF directly.
//
// File map:
//
//	pdf.go        — entry points, Options, renderer struct, RenderWriter
//	page.go       — page decorations, headers/footers, page break, footnotes
//	paragraph.go  — drawParagraph + list marker resolution
//	text.go       — atom model, line layout, runs→atoms
//	table.go      — drawTable, drawRow, borders, cell measurement
//	image.go      — image fit/crop/draw
//	fonts.go      — font registration, CJK fallback, color resolution
//	fields.go     — w:fldChar / w:instrText flattening, field codes
//	util.go       — twips/hex/file helpers
package render

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

// Options controls font selection, page numbering, fields, and tracing.
type Options struct {
	// SourceFilename and Author are surfaced to FILENAME / AUTHOR fields.
	// Empty values cause those fields to fall through to their cached text.
	SourceFilename string
	Author         string
	// Logger receives one-line progress messages (instead of Verbose stdout
	// printf). When nil and Verbose is true, falls back to stdout.
	Logger func(string)
	// OnProgress is called with a fraction in [0,1] after each section and
	// at the start of each page-decoration pass.
	OnProgress func(fraction float64, stage string)
	// Lenient: keep going past per-paragraph errors and log them. Useful
	// for crawling corpora of files of unknown quality.
	Lenient bool
	// ctx is set internally by RenderWithContext / RenderWriterWithContext.
	// External callers should use those entry points instead of poking ctx
	// directly. Public users get cancellation via convert.ConvertContext.
	ctx context.Context

	// FontRegular is the path to the TTF used for normal text. When
	// empty, resolution order is: $DOCX2PDF_FONT env var, then a list
	// of common system-font locations (Arial / Helvetica on macOS,
	// DejaVu / Liberation / Noto on Linux), then a small embedded Go
	// font that ships with the binary so scratch / distroless /
	// fontless containers still work. The embedded face is Latin only;
	// CJK documents still need an explicit FontFallback.
	FontRegular string
	FontBold    string // optional; falls back to FontRegular
	FontItalic  string // optional
	// FontHeading is an optional TTF used for runs that the theme tags with
	// a "major" font role (w:rFonts w:asciiTheme="majorHAnsi" etc.). When
	// empty, theme-major runs fall back to FontRegular — which means modern
	// Word templates render headings in the body face. Set this to e.g.
	// Cambria.ttf to get the visual distinction Office shows by default.
	FontHeading string
	// FontFallback is a TTF used for runes the regular font cannot render
	// (typically CJK). Recommended: Noto Sans CJK or similar. When empty,
	// $DOCX2PDF_FONT_CJK is consulted; missing it just means CJK glyphs
	// share the regular face (and likely render as boxes).
	FontFallback string
	// DefaultFontSize is the size in points used when the document does
	// not specify one. Word's default is 11pt.
	DefaultFontSize float64
	// PageNumbers, when true, draws "X / N" centered in the bottom margin
	// of every page after the body is rendered.
	PageNumbers bool
	Verbose     bool
}

// RenderWithContext is Render with cancellation.
func RenderWithContext(ctx context.Context, doc *docx.Document, outPath string, opts Options) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create pdf: %w", err)
	}
	if err := RenderWriterWithContext(ctx, doc, f, opts); err != nil {
		f.Close()
		_ = os.Remove(outPath)
		return err
	}
	return f.Close()
}

// RenderWriterWithContext is RenderWriter with cancellation.
func RenderWriterWithContext(ctx context.Context, doc *docx.Document, w io.Writer, opts Options) error {
	opts.ctx = ctx
	return RenderWriter(doc, w, opts)
}

// Render writes doc to outPath as a PDF.
func Render(doc *docx.Document, outPath string, opts Options) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create pdf: %w", err)
	}
	if err := RenderWriter(doc, f, opts); err != nil {
		f.Close()
		_ = os.Remove(outPath) // don't leave a half-written file behind
		return err
	}
	return f.Close()
}

// RenderWriter is the streaming variant — writes the produced PDF to w.
func RenderWriter(doc *docx.Document, w io.Writer, opts Options) error {
	if opts.FontRegular == "" {
		// Resolution order when no explicit font was passed:
		//   1. DOCX2PDF_FONT env var (set by our Docker image, also a
		//      convenient knob for containerized deployments).
		//   2. findSystemFont(): a list of common /usr/share/fonts/
		//      and macOS / Windows paths.
		//   3. Embedded Go font (~150 KB Latin face bundled into the
		//      binary) — last resort so scratch / distroless / fontless
		//      containers still produce output.
		opts.FontRegular = resolveFontFromEnv(envFontRegular)
		if opts.FontRegular == "" {
			opts.FontRegular = findSystemFont() // never empty: falls back to embedded
		}
	}
	// Symmetric env-var fallback for the CJK / symbol fallback font.
	// Resolution: explicit Options.FontFallback → $DOCX2PDF_FONT_CJK →
	// system-CJK auto-detection (Hiragino on macOS, WQY on Linux).
	// No final embedded fallback because the Go font is Latin only —
	// it wouldn't actually cover the glyphs callers need a fallback
	// FOR (CJK + Dingbats + arrows + etc.).
	if opts.FontFallback == "" {
		opts.FontFallback = resolveFontFromEnv(envFontFallback)
	}
	if opts.FontFallback == "" {
		opts.FontFallback = findSystemCJKFont()
	}
	if opts.DefaultFontSize == 0 {
		opts.DefaultFontSize = 11
	}

	sections := doc.Sections
	if len(sections) == 0 {
		sections = []docx.Section{{
			Blocks:       doc.Body,
			PageSize:     doc.PageSize,
			Margins:      doc.Margins,
			HeaderBlocks: doc.HeaderBlocks,
			FooterBlocks: doc.FooterBlocks,
		}}
		if sections[0].PageSize.WidthTwips == 0 {
			sections[0].PageSize = docx.A4Twips
		}
		if sections[0].Margins == (docx.Margins{}) {
			sections[0].Margins = docx.DefaultMarginsTwips
		}
	}

	pdf := gopdf.GoPdf{}
	firstW := twipsToPt(sections[0].PageSize.WidthTwips)
	firstH := twipsToPt(sections[0].PageSize.HeightTwips)
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: firstW, H: firstH}})

	pdf.SetInfo(gopdf.PdfInfo{
		Title:        doc.Properties.Title,
		Subject:      doc.Properties.Subject,
		Author:       firstNonEmpty(opts.Author, doc.Properties.Author),
		Creator:      "docx2pdf-go",
		Producer:     "docx2pdf-go (gopdf)",
		CreationDate: time.Now(),
	})

	r := &renderer{
		pdf:      &pdf,
		doc:      doc,
		opts:     opts,
		fonts:    map[string]bool{},
		counters: map[int]map[int]int{},
		fields: fieldVars{
			now:         time.Now(),
			filename:    filepath.Base(opts.SourceFilename),
			author:      firstNonEmpty(opts.Author, doc.Properties.Author),
			title:       doc.Properties.Title,
			subject:     doc.Properties.Subject,
			company:     doc.Properties.Company,
			seqCounters: map[string]int{},
			bookmarks:   doc.Bookmarks,
			docProperties: map[string]string{
				"Title":   doc.Properties.Title,
				"Author":  doc.Properties.Author,
				"Subject": doc.Properties.Subject,
				"Company": doc.Properties.Company,
				"Pages":   strconv.Itoa(doc.Properties.Pages),
				"Words":   strconv.Itoa(doc.Properties.Words),
				"Lines":   strconv.Itoa(doc.Properties.Lines),
			},
		},
	}
	if err := r.registerFonts(); err != nil {
		return err
	}

	// Track which sections each PDF page belongs to so stampPageDecorations
	// can look up the right header/footer per page.
	sectionPageStart := make([]int, len(sections))

	logFn := opts.Logger
	if logFn == nil && opts.Verbose {
		logFn = func(s string) { fmt.Println(s) }
	}
	if logFn == nil {
		logFn = func(string) {}
	}
	progressFn := opts.OnProgress
	if progressFn == nil {
		progressFn = func(float64, string) {}
	}

	for i, sec := range sections {
		if opts.ctx != nil {
			if err := opts.ctx.Err(); err != nil {
				return err
			}
		}
		progressFn(float64(i)/float64(len(sections)), fmt.Sprintf("section %d/%d", i+1, len(sections)))
		r.pageW = twipsToPt(sec.PageSize.WidthTwips)
		r.pageH = twipsToPt(sec.PageSize.HeightTwips)
		marL := twipsToPt(sec.Margins.Left)
		marR := twipsToPt(sec.Margins.Right)
		marT := twipsToPt(sec.Margins.Top)
		marB := twipsToPt(sec.Margins.Bottom)
		marL += twipsToPt(sec.GutterTwips)
		r.marL, r.marR, r.marT, r.marB = marL, marR, marT, marB
		r.contentW = r.pageW - r.marL - r.marR
		r.lineNumCounter = sec.LineNumbering.Start
		if r.lineNumCounter < 1 {
			r.lineNumCounter = 1
		}

		// Section break TYPE is recorded on the section that's ENDING (it
		// describes how the NEXT section starts), so the decision for
		// whether section[i] starts on a new page comes from section[i-1].
		startsNewPage := true
		if i == 0 {
			startsNewPage = false
		} else if sections[i-1].Type == "continuous" {
			startsNewPage = false
		}
		switch {
		case i == 0:
			pdf.AddPage()
			r.cursorY = r.marT
			primeContentStream(&pdf)
		case !startsNewPage:
			// Continuous: stay on the same page, adopt new geometry.
		default:
			pdf.AddPageWithOption(gopdf.PageOption{
				PageSize: &gopdf.Rect{W: r.pageW, H: r.pageH},
			})
			r.cursorY = r.marT
			primeContentStream(&pdf)
		}
		sectionPageStart[i] = pdf.GetNumberOfPages()

		r.numColumns = float64(sec.Columns)
		if r.numColumns < 1 {
			r.numColumns = 1
		}
		r.colGap = twipsToPt(sec.ColumnSpaceTwips)
		if r.numColumns > 1 {
			full := r.pageW - r.marL - r.marR
			r.colW = (full - r.colGap*(r.numColumns-1)) / r.numColumns
			r.contentW = r.colW
			r.colBaseX = r.marL
			r.colIdx = 0
		} else {
			r.colW = 0
			r.colBaseX = r.marL
			r.colIdx = 0
		}

		for _, b := range sec.Blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				if err := r.drawParagraph(v); err != nil {
					if opts.Lenient {
						logFn(fmt.Sprintf("lenient: skip paragraph: %v", err))
						continue
					}
					return err
				}
			case docx.Table:
				if err := r.drawTable(v); err != nil {
					if opts.Lenient {
						logFn(fmt.Sprintf("lenient: skip table: %v", err))
						continue
					}
					return err
				}
			}
		}
	}
	r.drawFootnotesAtBottom()

	progressFn(1.0, "done")

	// Endnotes always go at document end as a trailer (Word puts them
	// there too). Footnotes were already rendered at each page's bottom.
	if err := r.appendNotesSection(doc.Endnotes, "Endnotes"); err != nil {
		return err
	}
	// Comments are reviewer markup; they're not part of the visible body
	// in Word's default print view, but dropping them silently loses
	// content. Surface them as a trailing section after endnotes so a
	// human can still see them in the produced PDF.
	if err := r.appendNotesSection(doc.Comments, "Comments"); err != nil {
		return err
	}

	if err := r.stampPageDecorations(sections, sectionPageStart); err != nil {
		return err
	}
	if opts.PageNumbers {
		if err := r.stampPageNumbers(); err != nil {
			return err
		}
	}

	if _, err := pdf.WriteTo(w); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}
	return nil
}

// renderer carries the drawing state through one Render call. Methods on
// renderer live in the topic-specific files (page.go, paragraph.go, ...).
type renderer struct {
	pdf         *gopdf.GoPdf
	doc         *docx.Document
	opts        Options
	pageW       float64
	pageH       float64
	marL        float64
	marR        float64
	marT        float64
	marB        float64
	contentW    float64
	cursorY     float64
	fonts       map[string]bool     // registered font names
	counters    map[int]map[int]int // numId → level → next counter value
	noPageBreak bool                // when true, ensureRoom never adds pages
	// Multi-column layout (w:cols).
	numColumns float64
	colW       float64
	colGap     float64
	colBaseX   float64
	colIdx     int
	// Line numbering state: counter advances per visible body line; reset
	// to LineNumbering.Start at each section.
	lineNumCounter int
	// croppedCache stores cropped image instances keyed by "<origID>:crop".
	croppedCache map[string]image.Image
	// pendingFootnotes holds IDs queued for page-bottom render. ensureRoom
	// (and the end-of-body finalizer) drains this list before a page break.
	pendingFootnotes []pendingNote
	// drawingFootnotes prevents the page-bottom draw from re-triggering
	// itself when ensureRoom calls into the same code path.
	drawingFootnotes bool
	fields           fieldVars
	lineHeight       docx.LineHeight
	// prevStyleID is the StyleID of the paragraph just drawn — used by
	// contextualSpacing to detect "same style as previous sibling".
	prevStyleID string
	// pendingMarker, if non-nil, is drawn at the first line's baseline
	// during layoutLine.flush() — used for hanging list markers.
	pendingMarker *pendingMarker
	// firstLineHangPt, when > 0, outdents the first physical line of the
	// active paragraph by that many points (Word's w:ind w:hanging). Cleared
	// after the first flush so subsequent lines wrap at the normal margin.
	firstLineHangPt float64
	// paragraphRTL is set while drawing a right-to-left paragraph.
	// layoutLine consults it to reverse line-internal atom order before
	// drawing; runsToAtoms uses it to reverse the rune sequence inside
	// RTL word atoms. Cleared at paragraph end.
	paragraphRTL bool
	// activeTabs is the active paragraph's tab stops, used by layoutLine
	// to snap atomTab atoms to the next stop.
	activeTabs []docx.TabStop
}

// pendingNote is one queued note awaiting page-bottom render.
type pendingNote struct {
	id      string
	endnote bool
}

// pendingMarker carries the next list marker to be drawn at the start of
// the first physical line of a paragraph.
type pendingMarker struct {
	text  string      // text marker (decimal/bullet/letter/roman)
	image image.Image // picture-bullet marker (alternative to text)
	x     float64
	props docx.RunProps
}
