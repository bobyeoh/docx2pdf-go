// Package docx defines the parsed AST of a .docx (OpenXML WordprocessingML) document.
//
// We deliberately model only the subset we can render: paragraphs, runs with
// basic character formatting, tables (single-level), inline images, and page
// breaks. This mirrors the pipeline docx4j uses (DOM-like tree → renderer),
// but stays narrow enough to keep in one head.
package docx

import "image"

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

// Properties mirrors a slice of word/docProps/core.xml + app.xml. Word/Office
// computes some counts (Pages/Words/Characters) and saves them into app.xml;
// we surface them for the PDF /Info dictionary.
type Properties struct {
	Title      string
	Author     string
	Subject    string
	Company    string
	Pages      int
	Words      int
	Characters int
	Lines      int
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
	LinkURL    string // hyperlink rId → external URL (resolved by renderer)
	LinkAnchor string // hyperlink w:anchor → internal bookmark name target
	Bookmark   string // when set, this is a marker placing a named anchor here
	// Explicit image size in points (from wp:extent in EMU). Zero means
	// "use the image's native dimensions scaled to content width if too big."
	ImageWidthPt, ImageHeightPt float64
	// Image source-rect crop in PERCENT (a:srcRect attrs are 1/1000 of percent
	// from each edge). E.g. CropTop=10000 = 10%. Zero = no crop on that side.
	CropTopPct, CropBottomPct, CropLeftPct, CropRightPct float64
	// FootnoteID, when non-empty, tags this run as a footnote / endnote
	// reference site. The visible Text is still drawn (typically as a
	// superscript marker); the renderer also queues the corresponding note
	// body for the current page's bottom area.
	FootnoteID string
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
// "fall back to default thin black solid".
type CellBorders struct {
	Top, Bottom, Left, Right BorderEdge
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
