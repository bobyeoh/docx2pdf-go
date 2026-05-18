// Package docx defines the parsed AST of a .docx (OpenXML WordprocessingML) document.
//
// We deliberately model only the subset we can render: paragraphs, runs with
// basic character formatting, tables (single-level), inline images, and page
// breaks. This mirrors the pipeline docx4j uses (DOM-like tree → renderer),
// but stays narrow enough to keep in one head.
package docx

import (
	"image"
	"time"
)

// Document is the top-level parsed result of a .docx file.
type Document struct {
	Body         []Block
	Images       map[string]image.Image    // keyed by rId (relationship id)
	Hyperlink    map[string]string         // rId → external URL (from rels, TargetMode=External)
	Defaults     RunProps                  // default run properties from styles.xml docDefaults/rPrDefault
	ParaDefaults ParaDefaults              // default paragraph properties from styles.xml docDefaults/pPrDefault
	Styles       map[string]ParagraphStyle // styleId → resolved paragraph style (basedOn already flattened)
	CharStyles   map[string]RunProps       // styleId → resolved character style (basedOn flattened)
	PageSize     PageSize                  // from sectPr; falls back to A4
	Margins      Margins                   // from sectPr; falls back to defaults
	Numbering    Numbering                 // list definitions from numbering.xml
	HeaderBlocks []Block                   // default header content; rendered on every page
	FooterBlocks []Block                   // default footer content; rendered on every page
	Sections     []Section                 // body split at sectPr boundaries; len>=1
	// Footnotes / endnotes keyed by w:id, parsed from word/footnotes.xml and
	// word/endnotes.xml. The renderer optionally appends a "Footnotes" /
	// "Endnotes" trailing section at document end so the content survives.
	Footnotes map[string][]Block
	Endnotes  map[string][]Block
	// Comments are reviewer annotations from word/comments.xml, keyed by
	// w:id. The renderer appends a "Comments" trailing section so the
	// notes survive into the PDF rather than being silently dropped.
	Comments map[string][]Block
	// Charts maps relationship id → structured chart data extracted
	// from a referenced chart part (word/charts/chartN.xml). Bar /
	// line / pie charts are drawn with gopdf primitives by the
	// renderer; unsupported chart types fall back to ChartData.FlatText
	// rendered as a "[Chart: …]" placeholder.
	Charts map[string]ChartData
	// Theme is the parsed contents of word/theme/theme1.xml — color scheme +
	// font scheme — used to resolve w:themeColor / rFonts w:asciiTheme refs.
	Theme Theme
	// TableStyles maps a tblStyle ID to its flattened formatting block. The
	// renderer applies these when a w:tbl carries w:tblStyle.
	TableStyles map[string]TableStyle
	// Bookmarks captures the text body of each named bookmark so REF fields
	// can resolve to it. Populated as the parser walks paragraphs.
	Bookmarks map[string]string
	// Properties from core.xml / app.xml when present — used for AUTHOR /
	// TITLE fields and for /Info dictionary in the PDF.
	Properties Properties
	// Settings from word/settings.xml — doc-wide rendering knobs.
	Settings Settings
	// EmbeddedFonts maps the lower-cased w:font/@w:name to the bytes
	// of each embedded face declared in word/fontTable.xml. Faces are
	// optional — Regular is typically present, the others may be nil.
	// ODTTF-obfuscated parts have been deobfuscated, so byte slices
	// are ready to hand to a TTF parser.
	EmbeddedFonts map[string]EmbeddedFontSet
}

// EmbeddedFontSet groups the four w:embed* faces of one w:font entry
// in word/fontTable.xml. Any field may be nil if that variant isn't
// embedded — Word typically embeds Regular at minimum and the other
// three when "Embed only the characters used in the document" is off.
type EmbeddedFontSet struct {
	Regular    []byte
	Bold       []byte
	Italic     []byte
	BoldItalic []byte
}

// Theme holds the bits of theme1.xml we read.
type Theme struct {
	Colors map[string]string // name (accent1/dk1/lt1/...) → 6-hex
	Fonts  map[string]string // role (majorAscii/minorAscii) → font name
}

// TableStyle is the flattened formatting for a named w:tblStyle.
type TableStyle struct {
	ID      string
	BasedOn string
	Run     RunProps
	// Table-level defaults applied to every cell unless the cell overrides.
	CellShading string
	Borders     CellBorders
	// TableBorders mirrors the style's <w:tblPr><w:tblBorders>. Used by
	// decodeTable to seed the table's effective tblBorders before the
	// table's own tblBorders override. Critical for the built-in
	// "TableGrid" style, which is how Word's default bordered tables
	// declare their grid lines — without applying this, those tables
	// render borderless.
	TableBorders TableBorders
	// Conditional formatting blocks keyed by Word's tblStylePr w:type.
	Conditional map[string]TableCondPr
}

// TableCondPr is one piece of conditional formatting (firstRow, lastRow,
// firstCol, lastCol, band1Horz, band2Horz, band1Vert, band2Vert, etc.).
type TableCondPr struct {
	Run         RunProps
	CellShading string
	Borders     CellBorders
}

// Settings captures the document-wide knobs from word/settings.xml that the
// renderer actually consumes. Everything in this struct is doc-level — per-
// section overrides live on Section. Unset (zero) values mean "fall back to
// the renderer's built-in default".
type Settings struct {
	// DefaultTabStopTwips is w:defaultTabStop — grid spacing for implicit
	// tabs when a paragraph defines no explicit w:tabs. Zero → renderer
	// uses 720 twips (half inch), the typical Word default.
	DefaultTabStopTwips int
	// EvenAndOddHeaders is w:evenAndOddHeaders — when true, sections may
	// supply distinct even-page header/footer references. When the setting
	// is absent, even-page references in sectPr should be ignored. We OR
	// this with the per-section flag so docs that only declare one of the
	// two still behave reasonably.
	EvenAndOddHeaders bool
	// DisplayBackgroundShape is w:displayBackgroundShape — the master
	// switch for w:background (page color). When absent, Word does NOT
	// paint the background even if a color is defined.
	DisplayBackgroundShape bool
	// DocVars mirrors w:docVars / w:docVar entries — string variables
	// referenced by DOCVARIABLE fields. Keys are stored case-insensitive
	// (lower-cased) so DOCVARIABLE lookup can be case-insensitive too.
	DocVars map[string]string
}

// Properties mirrors a slice of word/docProps/core.xml + app.xml. Word/Office
// computes some counts (Pages/Words/Characters) and saves them into app.xml;
// we surface them for the PDF /Info dictionary and for the DOCPROPERTY /
// MERGEFIELD / SAVEDATE / etc. field codes that look them up by name.
type Properties struct {
	// core.xml — dc:title / dc:creator / dc:subject / dc:description /
	// cp:keywords / cp:category / cp:lastModifiedBy / cp:revision; and
	// the three timestamps dc:created / dc:modified / cp:lastPrinted.
	// Description maps to Word's "Comments" doc property.
	Title          string
	Author         string
	Subject        string
	Description    string // = "Comments" doc-property
	Keywords       string
	Category       string
	LastModifiedBy string
	Revision       string
	Created        time.Time
	Modified       time.Time
	LastPrinted    time.Time

	// app.xml — Company / Manager / Application; TotalTime is edit
	// minutes used by EDITTIME. Page/Word/Char/Line counts are Word's
	// last-save snapshot.
	Company     string
	Manager     string
	Application string
	TotalTime   int // minutes
	Pages       int
	Words       int
	Characters  int
	Lines       int
}

// ChartData is the structured form of a c:chartSpace part. The
// parser extracts the first plot type it finds (bar/line/pie) along
// with categories and per-series values. Multi-plot charts and
// secondary axes are out of scope — the first plot wins.
type ChartData struct {
	// Type is one of "bar", "line", "pie", or "" when none of the
	// supported plot types were found. The renderer dispatches on
	// this; "" falls back to a "[Chart: FlatText]" placeholder.
	Type string
	// Title is the chart title text (c:title/c:tx descendants).
	Title string
	// BarDir = "col" for vertical bars, "bar" for horizontal. Only
	// meaningful when Type == "bar".
	BarDir string
	// Categories are the x-axis labels (from c:cat/c:strRef/c:strCache).
	// Length matches the longest series' Values slice for clean
	// alignment; shorter series are right-padded with NaN.
	Categories []string
	// Series carries one entry per c:ser inside the chart.
	Series []ChartSeries
	// FlatText is the legacy concatenation of all CharData. Used as
	// the fallback rendering for unsupported chart types and as the
	// human-readable surrogate when a chart is referenced from
	// text-extraction tooling (pdftotext, etc.).
	FlatText string
}

// ChartSeries is one data series — a name, a fill color (hex), and
// numeric values aligned with ChartData.Categories.
type ChartSeries struct {
	Name   string
	Color  string // 6-hex, no leading "#"; "" → renderer picks a default palette slot
	Values []float64
}

// HasData reports whether this chart has at least one numeric value
// across its series. Used to gate the "draw real chart" path against
// truly empty charts that should render as the textual placeholder.
func (c ChartData) HasData() bool {
	if c.Type == "" {
		return false
	}
	for _, s := range c.Series {
		if len(s.Values) > 0 {
			return true
		}
	}
	return false
}

// ParaDefaults seeds every paragraph before its own pPr is applied. Modern
// Office writes spacing-after=8pt, line=1.08 here so unstyled paragraphs
// inherit the "Office 2010+ defaults" look.
type ParaDefaults struct {
	SpacingBefore float64
	SpacingAfter  float64
	LineHeight    LineHeight
}

// PageNumberType encodes w:pgNumType: starting value + numeric format.
type PageNumberType struct {
	Start int    // 0 = use natural (1, 2, 3 ...)
	Fmt   string // "decimal" (default), "upperRoman", "lowerRoman", "upperLetter", "lowerLetter"
}

// PageBorders encodes w:pgBorders — colored frame around the page.
type PageBorders struct {
	Top, Bottom, Left, Right BorderEdge
}

// FrameInfo encodes w:framePr's positioning attributes for a paragraph
// that should render as a floating frame rather than in the normal flow.
//
// Drop-cap framing is a special case handled separately on Paragraph
// (DropCap / DropCapLines); this struct is only populated when the frame
// has at least one positioning attribute (w:w, w:x, w:y, w:xAlign, ...).
type FrameInfo struct {
	WidthTwips  int    // w:w — frame width
	HeightTwips int    // w:h — frame height (0 = fit content)
	XTwips      int    // w:x — absolute horizontal offset from HAnchor
	YTwips      int    // w:y — absolute vertical offset from VAnchor
	HAnchor     string // "margin" (default), "page", "text"
	VAnchor     string // "margin", "page" (default), "text"
	XAlign      string // "", "left", "center", "right", "inside", "outside"
	YAlign      string // "", "top", "center", "bottom", "inside", "outside"
	Wrap        string // "auto" (default), "around", "tight", "through", "none", "notBeside"
	HRule       string // "auto", "exact", "atLeast" — applies to HeightTwips
}

// LineNumbering encodes w:lnNumType. The renderer paints at a fixed
// horizontal inset, so w:distance is intentionally not modeled — a per-doc
// value would drift from the actual draw position.
type LineNumbering struct {
	Start   int    // first line number (default 1)
	CountBy int    // every Nth line shown (default 1)
	Restart string // "newPage", "newSection", "continuous"
}

// Section represents one continuous range of body blocks that share the same
// page setup and header/footer references. A doc has at least one section; a
// new section starts wherever the body had an inline sectPr.
//
// Headers/footers come in three flavors per Word's titlePg / evenAndOddHeaders
// settings: "default" applies to every page where a more specific one isn't
// set; "first" overrides on page 1 (when TitlePg is true); "even" overrides
// on even pages (when EvenAndOddHeaders is true).
type Section struct {
	// Type is one of "nextPage" (default), "continuous", "evenPage",
	// "oddPage", "nextColumn". Only nextPage and continuous are honored;
	// the others fall back to nextPage.
	Type              string
	Blocks            []Block
	PageSize          PageSize
	Margins           Margins
	HeaderBlocks      []Block // default header
	FooterBlocks      []Block // default footer
	HeaderFirstBlocks []Block // first-page header (when TitlePg=true)
	FooterFirstBlocks []Block
	HeaderEvenBlocks  []Block // even-page header (when EvenAndOddHeaders=true)
	FooterEvenBlocks  []Block
	TitlePg           bool // honor HeaderFirstBlocks / FooterFirstBlocks on page 1
	EvenAndOddHeaders bool // honor *Even* blocks on even pages
	PageNumber        PageNumberType
	Borders           PageBorders   // page-perimeter frame
	BackgroundColor   string        // w:background w:color (hex)
	MirrorMargins     bool          // mirror left/right on facing pages
	GutterTwips       int           // additional inside margin (binding)
	LineNumbering     LineNumbering // w:lnNumType
	Columns           int           // w:cols w:num (1 = no columns)
	ColumnSpaceTwips  int           // w:cols w:space between columns
}

// ParagraphStyle is a flattened paragraph-style definition from styles.xml.
// `basedOn` chains are resolved at load time so consumers never have to walk
// the inheritance graph themselves.
type ParagraphStyle struct {
	ID            string
	BasedOn       string
	Run           RunProps
	Alignment     Alignment
	HasAlignment  bool // discriminates AlignLeft default from explicit-left
	SpacingBefore float64
	SpacingAfter  float64
	LineHeight    LineHeight
}

// Block is either a Paragraph or a Table.
type Block interface{ isBlock() }

// Paragraph is a w:p element.
type Paragraph struct {
	Runs      []Run
	Alignment Alignment
	// SpacingBefore / SpacingAfter are extra vertical space in points.
	SpacingBefore float64
	SpacingAfter  float64
	PageBreak     bool      // a leading page break (w:br w:type="page" in first run)
	List          *ListInfo // non-nil if paragraph is a list item
	// IndentLeftPt is the body-text left indent in points (w:ind w:left).
	IndentLeftPt float64
	// IndentFirstLinePt is the first-line offset relative to IndentLeftPt
	// (w:ind w:firstLine for positive, w:ind w:hanging for negative).
	IndentFirstLinePt float64
	// LineHeight encodes w:spacing w:line + w:lineRule. Zero value means
	// "fall back to the renderer's default" (single-spacing with the natural
	// font line height).
	LineHeight LineHeight
	// KeepNext: prefer keeping this paragraph on the same page as the next.
	KeepNext bool
	// KeepLines: prefer not breaking lines of this paragraph across pages.
	KeepLines bool
	// ContextualSpacing: suppress SpacingBefore/After if the adjacent
	// paragraph uses the same style (typical for list items).
	ContextualSpacing bool
	// Bidi: paragraph reads right-to-left. We currently mark it but don't
	// perform RTL line layout — text is preserved in source order.
	Bidi bool
	// StyleID is the w:pStyle reference (resolved at decode time but kept
	// here so the renderer can apply contextualSpacing per-style sibling.)
	StyleID string
	// Tabs is the parsed w:tabs list — sorted by Pos.
	Tabs []TabStop
	// DropCap is "drop" or "margin" when w:framePr declares drop-cap on the
	// paragraph; "" otherwise. We render the first character at ~3× size as
	// an approximation (real wrap-around layout is out of scope).
	DropCap string
	// DropCapLines is the number of body lines the drop-cap visually spans.
	// Pulled from w:framePr w:lines; defaults to 3 when DropCap is set.
	DropCapLines int
	// Frame, when non-nil, declares this paragraph is a positioned frame
	// (w:framePr with placement attributes — distinct from the drop-cap
	// variant). The renderer draws at the anchored position without
	// advancing the document cursor; surrounding body text is NOT
	// reflowed around the frame, so wrapping with `wrap="around"` may
	// visually overlap.
	Frame *FrameInfo
	// Borders holds the four edges of <w:pBdr>. Markdown-style "---"
	// thematic breaks are commonly encoded as an empty paragraph with
	// only the bottom edge set. We reuse CellBorders because the shape
	// (Top / Bottom / Left / Right BorderEdge) is identical.
	Borders CellBorders

	// endsSection is set when this paragraph's pPr contained an inline sectPr.
	// Internal-only: the parser uses it to know when to close out a section.
	endsSection bool
}

// TabStop is one entry of a paragraph's w:tabs definition.
//
//	Pos is the absolute position in points from the paragraph's left edge.
//	Val is one of "left" (default), "center", "right", "decimal", "clear".
//	Leader is "", "dot", "hyphen", "underscore", or "middleDot".
type TabStop struct {
	Pos    float64
	Val    string
	Leader string
}

// LineHeight encodes the w:spacing/@w:line attribute together with its
// @w:lineRule discriminator.
//
//   - Rule = "auto" (the default in Word): Mul is the multiplier; 1.0 = single,
//     1.5 = one-and-a-half, 2.0 = double. Pt is unused.
//   - Rule = "exact": Pt is the line height in points. Mul is unused.
//   - Rule = "atLeast": Pt is a minimum line height in points; the renderer
//     uses max(Pt, natural).
//   - Empty Rule = unset; the renderer keeps its default.
type LineHeight struct {
	Rule string
	Pt   float64
	Mul  float64
}

// ListInfo is a reference into Document.Numbering for a paragraph.
type ListInfo struct {
	NumID int
	Level int
}

func (Paragraph) isBlock() {}

// Run is one inline atom inside a paragraph. The decoder emits Runs in
// document order; a Run carries exactly one piece of content (text, image,
// break) OR exactly one field-structure marker. The renderer collapses field
// markers into resolved text via a small state machine — keeping field state
// out of the AST lets the parser stay stateless.
type Run struct {
	Text       string
	IsBreak    bool   // soft line break (w:br without page type)
	ImageID    string // rId if this run is a w:drawing/pic image
	ChartID    string // rId if this run is a w:drawing/c:chart reference
	LinkURL    string // hyperlink rId → external URL (resolved by renderer)
	LinkAnchor string // hyperlink w:anchor → internal bookmark name target
	Bookmark   string // when set, this is a marker placing a named anchor here
	// Explicit image size in points (from wp:extent in EMU). Zero means
	// "use the image's native dimensions scaled to content width if too big."
	ImageWidthPt, ImageHeightPt float64
	// WrapMode mirrors wp:anchor/wp:wrap*: "" (inline) / "topAndBottom" /
	// "square" / "tight" / "through" / "none". The reader.go decoder
	// inserts soft breaks around topAndBottom images so they sit alone;
	// other wrap modes are preserved as metadata for future layout work
	// but currently render inline.
	WrapMode string
	// Image source-rect crop in PERCENT (a:srcRect attrs are 1/1000 of percent
	// from each edge). E.g. CropTop=10000 = 10%. Zero = no crop on that side.
	CropTopPct, CropBottomPct, CropLeftPct, CropRightPct float64
	// FootnoteID, when non-empty, tags this run as a footnote / endnote
	// reference site. The visible Text is still drawn (typically as a
	// superscript marker); the renderer also queues the corresponding note
	// body for the current page's bottom area.
	FootnoteID string
	// HorizontalRule marks a run that should render as a horizontal
	// separator line at the paragraph's vertical position. Word emits
	// these as <w:pict><v:rect o:hr="t"/></w:pict> — Office's HTML-
	// compatibility way of representing markdown's "---" thematic break.
	HorizontalRule bool
	// IsEndnote distinguishes endnote refs from footnote refs (different
	// lookup map on the renderer side).
	IsEndnote bool
	Props     RunProps

	// --- Field structure (w:fldChar / w:instrText) ---
	// Exactly one of FieldBegin/FieldSep/FieldEnd is set on a marker run, OR
	// InstrText is non-empty for an instruction-text run. Marker runs carry
	// no visible content.
	FieldBegin bool
	FieldSep   bool // w:fldChar w:fldCharType="separate" — code → result boundary
	FieldEnd   bool
	InstrText  string
}

// RunProps captures character-level formatting we honor.
type RunProps struct {
	Bold       bool
	Italic     bool
	Underline  bool
	Strike     bool    // w:strike — single-line strikethrough
	Caps       bool    // w:caps — render as uppercase
	SmallCaps  bool    // w:smallCaps — lowercase rendered as small upper-case
	FontSize   float64 // half-points in docx; we store points
	FontFamily string
	Color      string // hex without leading '#', e.g. "FF0000"
	// Highlight is one of Word's predefined names (yellow, green, cyan, ...).
	// When non-empty the renderer paints a colored rect under the run.
	Highlight string
	// Shading is a 6-hex background color (run-level w:shd w:fill).
	Shading string
	// VertAlign is "superscript", "subscript", or "" (normal). Renderer
	// shifts the y baseline and reduces the font size to ~60%.
	VertAlign string
	// StyleID points at a named character style (w:rStyle val). Resolved at
	// run-construction time by merging the style's props underneath this run.
	StyleID string
	// Vanish suppresses the run from rendering (w:vanish). The text is still
	// kept in the AST so callers can introspect it.
	Vanish bool
	// PositionPt is w:position in points — raises/lowers baseline. Positive
	// = up. Distinct from VertAlign which also changes size.
	PositionPt float64
	// CharacterScale is w:w as a fraction (1.0 = 100% width). Applied at
	// draw time as text-matrix horizontal scale.
	CharacterScale float64
	// ThemeColor names the theme color slot (e.g. "accent1", "text1"). When
	// non-empty the renderer resolves it through Document.Theme.Colors.
	ThemeColor string
	// LumMod / LumOff are Word's HSL luminance adjustments derived from
	// w:themeShade and w:themeTint. LumMod < 1 darkens; LumOff > 0 lightens
	// toward white. Both in [0, 1].
	LumMod, LumOff float64
	// ThemeFontRole is "majorAscii", "minorAscii", "majorEastAsia", ... .
	// Resolved at draw time via Document.Theme.Fonts.
	ThemeFontRole string
	// LetterSpacingPt widens every glyph's advance by this many points
	// (w:spacing in rPr — Word stores 1/20 pt; we convert).
	LetterSpacingPt float64
	// TextEffect is "emboss", "imprint", "outline", or "". Renderer draws
	// emboss/imprint with a faint highlight stroke, outline with text fill
	// none + a stroke. These are approximations.
	TextEffect string
}

// Alignment maps w:jc values.
type Alignment int

const (
	AlignLeft Alignment = iota
	AlignCenter
	AlignRight
	AlignJustify
)

// Table is a w:tbl element.
type Table struct {
	Rows []TableRow
	// ColumnWidthsTwips: column widths in twentieths of a point (twips).
	// 1440 twips = 1 inch.
	ColumnWidthsTwips []int
	// StyleID points at a named table style (w:tblPr/w:tblStyle val). The
	// parser flattens that style's tblPr / tcPr defaults into the table's
	// own properties at decode time.
	StyleID string
	// Look encodes w:tblLook flags from tblPr — which conditional formatting
	// blocks should apply (firstRow / lastRow / firstColumn / lastColumn,
	// banding etc.).
	Look TableLook
	// Borders carries <w:tblBorders> straight from tblPr. After parsing
	// is complete these are propagated into each cell's CellBorders (with
	// outer rows/columns taking the outer edge and interior cells taking
	// insideH/insideV) so the renderer only has to read CellBorders.
	Borders TableBorders
}

// TableLook is the parsed w:tblLook bitfield.
type TableLook struct {
	FirstRow    bool
	LastRow     bool
	FirstColumn bool
	LastColumn  bool
	NoHBand     bool // suppress horizontal banding
	NoVBand     bool // suppress vertical banding
}

// Block interface for Table.
// (already satisfied below; declared here for clarity at the type definition.)

func (Table) isBlock() {}

type TableRow struct {
	Cells []TableCell
	// IsHeader marks a row with w:trPr/w:tblHeader. Headers repeat on every
	// page the table spans across.
	IsHeader bool
	// HeightTwips is the minimum row height from w:trHeight (rule="atLeast").
	// Zero means "natural" — use content height.
	HeightTwips int
	// HeightRuleExact means w:trHeight rule="exact" — render at exactly the
	// given height, clipping if content overflows.
	HeightRuleExact bool
	// CantSplit means the row must be drawn intact — if it won't fit on the
	// current page, push it to the next page first.
	CantSplit bool
}

type TableCell struct {
	// A cell may contain paragraphs OR nested tables, in document order.
	// We use the same Block interface as the body so nesting Just Works.
	Blocks []Block
	// GridSpan is the number of columns this cell spans (default 1).
	GridSpan int
	// VMerge is "restart", "continue", or "" (no vertical merge).
	VMerge string
	// Shading is the 6-hex background fill color (w:shd w:fill).
	Shading string
	// VAlign is "top", "center", "bottom", or "" (default top).
	VAlign string
	// Borders, when set, override the default thin black per-edge borders.
	Borders CellBorders
	// Margins (w:tcMar) in points, defaulting to {Top: 0, Bottom: 0, Left: 4, Right: 4}
	// when zero. We only honor symmetric defaults; per-cell overrides take
	// precedence.
	MarginTopPt, MarginBottomPt, MarginLeftPt, MarginRightPt float64
}

// Paragraphs returns the paragraph-typed blocks in document order — kept as
// a convenience method so existing callers that iterated cell.Paragraphs
// can still do so (they now do `for _, p := range cell.Paragraphs() { ... }`).
func (c TableCell) Paragraphs() []Paragraph {
	out := make([]Paragraph, 0, len(c.Blocks))
	for _, b := range c.Blocks {
		if p, ok := b.(Paragraph); ok {
			out = append(out, p)
		}
	}
	return out
}

// CellBorders carries the four per-edge border specs. A zero Edge means
// "no border on that edge" — table-level borders are propagated into
// cells at parse time (see propagateTableBorders), so by the time the
// renderer sees a cell every meaningful edge is filled in.
type CellBorders struct {
	Top, Bottom, Left, Right BorderEdge
}

// TableBorders mirrors <w:tblBorders>. It carries the four outer edges
// plus the two "inside" edges that apply between cells (insideH between
// rows, insideV between columns).
type TableBorders struct {
	Top, Bottom, Left, Right BorderEdge
	InsideH, InsideV         BorderEdge
}

// Has reports whether any of the six edges is non-zero.
func (b TableBorders) Has() bool {
	return b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has() ||
		b.InsideH.Has() || b.InsideV.Has()
}

// BorderEdge holds the style, width (points), and color (hex) for one edge.
//
//	Style examples Word writes: "single" (default), "double", "dashed",
//	"dotted", "thick", "none".
type BorderEdge struct {
	Style string
	Sz    float64 // line thickness in points (Word stores 1/8 pt; we convert)
	Color string  // 6-hex; empty = auto/black
}

// Has reports whether the edge carries any styling info.
func (e BorderEdge) Has() bool { return e.Style != "" || e.Sz != 0 || e.Color != "" }

// PageSize in twips. 1 pt = 20 twips.
type PageSize struct {
	WidthTwips  int
	HeightTwips int
}

// Margins in twips.
type Margins struct {
	Top    int
	Bottom int
	Left   int
	Right  int
}

// A4Twips is the standard A4 page size in twips. Used as a fallback when the
// document does not declare a w:sectPr/w:pgSz.
var A4Twips = PageSize{WidthTwips: 11906, HeightTwips: 16838}

// DefaultMarginsTwips is the typical 1-inch margin (1440 twips).
var DefaultMarginsTwips = Margins{Top: 1440, Bottom: 1440, Left: 1440, Right: 1440}

// Numbering is the parsed contents of word/numbering.xml.
//
// Word stores list definitions in two layers:
//   - abstractNum: a reusable template with one Level per indent depth.
//   - num: a concrete list instance pointing at an abstractNumId. Multiple
//     w:num entries can share an abstractNum (e.g. when a doc has many
//     separate bullet lists that all look the same).
//
// A paragraph references a list via w:numId + w:ilvl.
type Numbering struct {
	Abstract map[int]AbstractNum // abstractNumId → definition
	NumToAbs map[int]int         // numId → abstractNumId
	// PicBullets maps w:numPicBulletId → image rId. A level whose
	// w:lvlPicBulletId names one of these renders the image as its marker.
	PicBullets map[int]string
}

type AbstractNum struct {
	Levels map[int]NumLevel // ilvl → level definition
}

// NumLevel describes how one indent level is rendered.
type NumLevel struct {
	Format       string
	Text         string
	Start        int
	LeftTwips    int
	HangingTwips int
	// IsLgl is w:isLgl — Word forces all lvlText substitutions for THIS
	// level (and below) to render in decimal, regardless of their original
	// numFmt. Used for "1.2.3.4" legal-style numbering.
	IsLgl bool
	// PicBulletID, when > 0, names a w:numPicBullet whose image should be
	// used as this level's bullet marker. Resolved via Numbering.PicBullets.
	PicBulletID int
}
