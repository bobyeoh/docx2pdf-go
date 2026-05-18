package render

import (
	"io"
	"strconv"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TOC field rendering. Word documents that include a "TOC" field
// store a cached snapshot of the rendered table of contents — which
// works as-is when the document was saved by Word with current page
// numbers. Documents written by tools that don't refresh the cache
// (or that ship an empty TOC waiting for first render) need us to
// generate the entries ourselves.
//
// Approach: two-pass rendering.
//
//  1. Walk the doc, collect headings (Heading1..9 / Title style),
//     and locate any paragraph carrying a TOC field's begin/sep/end.
//  2. Replace the TOC paragraph(s) with auto-generated entries that
//     use placeholder page numbers. Render this to io.Discard with a
//     callback (Options.onHeadingPage) hooked in so each heading's
//     resolved page number gets recorded.
//  3. Re-substitute the TOC entries with the live page numbers and
//     render the final PDF to the real writer.
//
// The first-pass placeholders are the same line count as the final
// entries (one paragraph per heading, varying only by the digits in
// the page number), so the TOC occupies roughly the same vertical
// space and page numbers stay stable. For pathological cases (a
// page-boundary near a TOC entry whose page-number-width changes the
// wrap), the result may be off by one — acceptable for an MVP.
//
// What this does NOT do:
//   - Honor TOC switches (\o "1-3" depth, \h hyperlinks, \z hide-tab,
//     \u use-outline). We pick all heading paragraphs at every level.
//   - Indent entries by level beyond a single tab.
//   - Generate styled TOC entries (TOC1..TOC9 styles, dot leader
//     across the actual width to right margin). We use a plain
//     "Title <tab> 12" layout with dot-leader characters approximate
//     to fixed width.

// tocEntry is one row of the auto-generated table of contents.
type tocEntry struct {
	Level   int    // 1..9; 0 means "Title"
	Title   string // visible heading text
	StyleID string // raw w:pStyle/@w:val so we can disambiguate same-text headings
	Page    int    // 0 in pass 1 (placeholder)
}

// hasTOCField reports whether any paragraph in doc carries a TOC
// field begin marker. Cheap scan — used as the gate that switches
// RenderWriter to the two-pass path.
func hasTOCField(doc *docx.Document) bool {
	if doc == nil {
		return false
	}
	for _, sec := range doc.Sections {
		if blockSliceHasTOC(sec.Blocks) {
			return true
		}
	}
	return blockSliceHasTOC(doc.Body)
}

func blockSliceHasTOC(blocks []docx.Block) bool {
	for _, b := range blocks {
		if p, ok := b.(docx.Paragraph); ok && paragraphHasTOCField(p) {
			return true
		}
	}
	return false
}

// paragraphHasTOCField checks if p's runs contain a TOC instrText
// inside a fldChar begin/sep range.
func paragraphHasTOCField(p docx.Paragraph) bool {
	for _, r := range p.Runs {
		if r.InstrText != "" {
			code, _ := fieldCodeAndArgs(r.InstrText)
			if code == "TOC" {
				return true
			}
		}
	}
	return false
}

// collectHeadings walks the doc and returns each heading-styled
// paragraph's title + level + styleID in document order. Used to
// populate the auto-generated TOC entries.
func collectHeadings(doc *docx.Document) []tocEntry {
	var out []tocEntry
	visit := func(blocks []docx.Block) {
		for _, b := range blocks {
			p, ok := b.(docx.Paragraph)
			if !ok {
				continue
			}
			title := headingTitle(p)
			if title == "" {
				continue
			}
			out = append(out, tocEntry{
				Level:   headingLevel(p.StyleID),
				Title:   title,
				StyleID: p.StyleID,
			})
		}
	}
	if len(doc.Sections) == 0 {
		visit(doc.Body)
		return out
	}
	for _, sec := range doc.Sections {
		visit(sec.Blocks)
	}
	return out
}

// headingLevel pulls the level out of a heading styleID. "Heading1"
// → 1; "Heading 3" → 3; "Title" → 0; non-heading → -1.
func headingLevel(id string) int {
	low := strings.ToLower(strings.ReplaceAll(id, " ", ""))
	if low == "title" {
		return 0
	}
	if !strings.HasPrefix(low, "heading") {
		return -1
	}
	rest := low[len("heading"):]
	if rest == "" {
		return 1
	}
	if len(rest) == 1 && rest[0] >= '1' && rest[0] <= '9' {
		return int(rest[0] - '0')
	}
	return -1
}

// buildTOCBlocks turns a list of entries into a slice of paragraphs
// suitable for splicing in place of the original TOC field
// paragraph. Each entry becomes one paragraph: indented per its
// level, with the heading text, a tab, dot-leader characters, and
// the page number.
//
// We deliberately pre-fill the dot leader at construction time
// rather than relying on the renderer's tab-leader machinery,
// because tab leaders here need fixed-width filler that doesn't
// depend on knowing the precise right-margin tab stop position.
func buildTOCBlocks(entries []tocEntry) []docx.Block {
	out := make([]docx.Block, 0, len(entries))
	for _, e := range entries {
		out = append(out, buildTOCParagraph(e))
	}
	return out
}

// buildTOCParagraph constructs the visible paragraph for one entry.
// The layout is:
//
//	[indent per level]  Heading title ........ <page>
//
// The dot leader is a fixed-character pad sized so the paragraph
// reads as a TOC entry without us needing to compute the right-
// margin tab position. Real Word TOC styles do this properly via
// right-aligned tab stops; this is a workable shortcut.
func buildTOCParagraph(e tocEntry) docx.Paragraph {
	page := strconv.Itoa(e.Page)
	if e.Page <= 0 {
		page = ""
	}
	// Compute filler. Aim for ~70 visible columns total (heading +
	// dots + page). Bounded so very long titles don't produce empty
	// leaders or absurdly long dot strings.
	const totalCols = 72
	fillerLen := totalCols - len([]rune(e.Title)) - len(page) - 2 // 2 for leading spaces
	if fillerLen < 3 {
		fillerLen = 3
	}
	if fillerLen > 80 {
		fillerLen = 80
	}
	filler := " " + strings.Repeat(".", fillerLen) + " "

	// Per-level indent in points. Level 1 = 0; each deeper level
	// adds ~18pt (≈ Word's TOC1..TOC9 default progression).
	var leftIndentPt float64
	if e.Level > 1 {
		leftIndentPt = float64(e.Level-1) * 18
	}

	runs := []docx.Run{
		{Text: e.Title},
		{Text: filler},
		{Text: page},
	}
	return docx.Paragraph{
		Runs:         runs,
		IndentLeftPt: leftIndentPt,
	}
}

// replaceTOCField returns a doc clone (sections + body re-sliced)
// where every paragraph carrying a TOC field begin marker is
// replaced by the supplied block list. The original doc is NOT
// mutated; callers can safely run multiple passes with different
// entry slices.
//
// Block slices are re-allocated; the per-block values are reused by
// reference (Paragraph is a value type, so sharing is safe). Headers,
// footers, and other doc-level fields pass through unchanged.
func replaceTOCField(doc *docx.Document, replacement []docx.Block) *docx.Document {
	clone := *doc
	if len(doc.Sections) > 0 {
		clone.Sections = make([]docx.Section, len(doc.Sections))
		copy(clone.Sections, doc.Sections)
		for i := range clone.Sections {
			clone.Sections[i].Blocks = spliceTOCBlocks(clone.Sections[i].Blocks, replacement)
		}
	}
	clone.Body = spliceTOCBlocks(doc.Body, replacement)
	return &clone
}

// spliceTOCBlocks returns a fresh slice where each paragraph that
// carries a TOC field is replaced by the entries in `replacement`.
// Non-TOC blocks pass through unchanged.
func spliceTOCBlocks(blocks []docx.Block, replacement []docx.Block) []docx.Block {
	out := make([]docx.Block, 0, len(blocks)+len(replacement))
	for _, b := range blocks {
		if p, ok := b.(docx.Paragraph); ok && paragraphHasTOCField(p) {
			out = append(out, replacement...)
			continue
		}
		out = append(out, b)
	}
	return out
}

// renderWithTOC implements the two-pass TOC orchestration. Called
// from RenderWriter when the doc contains a TOC field.
func renderWithTOC(doc *docx.Document, w io.Writer, opts Options) error {
	entries := collectHeadings(doc)
	if len(entries) == 0 {
		// TOC field present but doc has no headings — just render
		// the original doc (the TOC field paragraph will be empty
		// content, which is what Word would show too).
		return renderImpl(doc, w, opts)
	}

	// First pass: render with placeholder pages so the TOC occupies
	// roughly the right vertical footprint. Discard the output.
	placeholders := make([]tocEntry, len(entries))
	copy(placeholders, entries)
	for i := range placeholders {
		placeholders[i].Page = 1 // 1-digit placeholder
	}
	firstDoc := replaceTOCField(doc, buildTOCBlocks(placeholders))

	pageMap := make(map[string]int)
	firstOpts := opts
	firstOpts.onHeadingPage = func(title, styleID string, page int) {
		pageMap[tocKey(title, styleID)] = page
	}
	if err := renderImpl(firstDoc, io.Discard, firstOpts); err != nil {
		return err
	}

	// Second pass: real page numbers from the map.
	for i := range entries {
		if p, ok := pageMap[tocKey(entries[i].Title, entries[i].StyleID)]; ok {
			entries[i].Page = p
		}
	}
	finalDoc := replaceTOCField(doc, buildTOCBlocks(entries))
	return renderImpl(finalDoc, w, opts)
}

// tocKey joins title and styleID into the lookup key used by the
// pageMap. styleID disambiguates duplicate titles at different
// heading levels (rare but real — two Heading2's both named
// "Overview" in a long doc).
func tocKey(title, styleID string) string {
	return styleID + "\x00" + title
}
