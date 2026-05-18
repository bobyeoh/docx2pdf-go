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
	// RelTargets is rId → internal package target (e.g.
	// "subDocument/sub1.docx", "media/image1.png"). Populated for
	// every non-external relationship so callers can resolve referents
	// (subDoc, INCLUDETEXT/INCLUDEPICTURE, hyperlink anchors). External
	// targets stay in Hyperlink to preserve the existing convention.
	RelTargets map[string]string
	Defaults     RunProps                  // default run properties from styles.xml docDefaults/rPrDefault
	ParaDefaults ParaDefaults              // default paragraph properties from styles.xml docDefaults/pPrDefault
	Styles       map[string]ParagraphStyle // styleId → resolved paragraph style (basedOn already flattened)
	CharStyles   map[string]RunProps       // styleId → resolved character style (basedOn flattened)
	LatentStyles LatentStylesInfo          // w:latentStyles + w:lsdException

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
	// Charts maps relationship id → flattened text extracted from a
	// referenced chart part (word/charts/chartN.xml). We can't render
	// the data graphic but we surface titles, axis labels, and data
	// labels so the prose around the chart still makes sense.
	Charts map[string]string
	// ChartsData captures the parsed series + categories for chart parts
	// where the renderer can produce a real graphic (bar / column / pie /
	// line). When the chart type isn't recognized the entry is omitted and
	// the renderer falls back to the flat text in Charts.
	ChartsData map[string]ChartData
	// Diagrams maps the dgm:relIds "r:dm" relationship id → flattened
	// text extracted from the SmartArt data part
	// (word/diagrams/dataN.xml). Each diagram surfaces as a list of
	// node texts joined with " → " so the conceptual structure
	// survives in the PDF text stream even though we don't render the
	// graphical shapes.
	Diagrams map[string]string
	// DiagramShapes holds the parsed visual shape tree for each SmartArt
	// diagram, keyed by the data part's relationship id (same key as
	// Diagrams). When set, the renderer paints the actual shape tree;
	// otherwise it falls back to the labeled-box placeholder.
	DiagramShapes map[string]*VMLShape
	// DiagramLayouts records the SmartArt layout family per rid — one of
	// "cycle", "hierarchy", "pyramid", "list", "matrix", "radial",
	// "process". Used by the renderer's synthesizer when DiagramShapes
	// is empty for that rid.
	DiagramLayouts map[string]string
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
	// FootnoteSeparators captures any custom w:type="separator" /
	// "continuationSeparator" / "continuationNotice" footnote bodies.
	// Keys are the OOXML w:type values; absence means "use renderer
	// default" (a thin horizontal rule).
	FootnoteSeparators map[string][]Block
	// EndnoteSeparators is the endnote equivalent.
	EndnoteSeparators map[string][]Block
	// DocVars are document variables from settings.xml w:docVars. Used by
	// the DOCVARIABLE field code.
	DocVars map[string]string
	// CustomProperties are extra metadata properties from docProps/custom.xml.
	CustomProperties map[string]string
	// AltChunks maps a relationship id to text extracted from an
	// AlternativeFormatInputPart (HTML / RTF / text).
	AltChunks map[string][]Block
	// Bibliography is the parsed b:Sources from customXml/itemN.xml.
	Bibliography map[string]BibSource
	// CustomXMLRoots maps a custom-xml part name to its root element snapshot
	// — used to evaluate w:dataBinding XPaths inside SDTs.
	CustomXMLRoots []CustomXMLPart
	// OpenDoPEXPaths is the id→xpath table parsed from any customXml part
	// that follows the OpenDoPE schema (<od:xpaths> root). Empty when the
	// document doesn't use OpenDoPE bindings.
	OpenDoPEXPaths map[string]string
	// PermissionRanges captures w:permStart / w:permEnd pairs by their
	// shared id. The PDF surface has no native edit-permission marker,
	// but AST consumers (auditing tools, redaction exporters) can use this
	// to surface which ranges Word would have locked. Keyed by w:id.
	PermissionRanges map[string]PermissionRange
	// MailMerge is non-empty when settings.xml declared <w:mailMerge>.
	MailMerge string
	// HasGlossary is true when the package contains word/glossary/document.xml.
	HasGlossary bool
	// Glossary maps a docPart's gallery/name (the keys an AUTOTEXT or
	// GLOSSARY field references) to the plain-text run sequence carried
	// by its <w:docPart>/<w:docPartBody>. Word's docParts can be rich
	// (paragraphs, tables, images); we keep just the text payload so
	// fields can expand. Empty when the package has no glossary.
	Glossary map[string]string
	// EmbeddedFonts holds deobfuscated TTF byte streams for fonts the
	// package embeds via word/fontTable.xml + w:embedRegular / Bold /
	// Italic / BoldItalic.
	EmbeddedFonts map[string]EmbeddedFont
	// PeopleByID maps the durable id from word/people.xml to the
	// author's display name + email.
	PeopleByID map[string]Person
	// OLEEmbeds maps an OLEObject relationship id to its decoded
	// content. Only Excel (Excel.Sheet.*) packages are currently
	// extracted; other ProgIDs leave the entry absent and the renderer
	// falls back to the preview image.
	OLEEmbeds map[string]ExcelEmbed
	// CommentsExtended holds the threaded / "marked done" state of each
	// w:id, parsed from word/commentsExtended.xml.
	CommentsExtended map[string]CommentExtended
	// UnsupportedMedia maps a relationship id whose target file is a
	// media format the renderer cannot draw (EMF, WMF) to its format
	// label ("EMF", "WMF").
	UnsupportedMedia map[string]string
	// CommentMeta indexes w:author / w:date / w:initials per comment id
	// from word/comments.xml.
	CommentMeta map[string]CommentMeta
}

// BibSource is one parsed entry from a bibliography custom XML store.
type BibSource struct {
	Tag         string
	SourceType  string
	Title       string
	Authors     []string
	Year        string
	Publisher   string
	City        string
	JournalName string
	Pages       string
	URL         string
}

// CustomXMLPart is one custom-xml store referenced from customXml/itemN.xml.
type CustomXMLPart struct {
	PartName string
	Data     []byte
	// StoreItemID is the GUID pulled from the companion
	// customXml/itemPropsN.xml (<ds:datastoreItem ds:itemID="{…}">).
	// SDTs reference a store explicitly via <w:storeItemID>; honoring
	// this lets us pick the right store when a document carries multiple
	// data sources with overlapping element names.
	StoreItemID string
}

// EmbeddedFont carries the deobfuscated TTF/OTF binary for each of the
// four font-style variants Word stores.
type EmbeddedFont struct {
	Name       string
	AltName    string
	Regular    []byte
	Bold       []byte
	Italic     []byte
	BoldItalic []byte
	// --- metadata for font substitution (Word's RunFontSelector inputs) ---
	// Panose1 is the 10-byte font classification string (w:panose1).
	Panose1 string
	// Charset is the Win32 charset id (e.g. "00" Western, "86" GB2312,
	// "80" ShiftJIS, "B1" Hebrew).
	Charset string
	// Family classification: auto/decorative/modern/roman/script/swiss.
	Family string
	// Pitch: default/fixed/variable.
	Pitch string
	// Sig holds w:sig — usb0..usb3 + csb0..csb1 bitfields that say which
	// Unicode subranges and codepages the font claims to cover. Used for
	// fallback selection when a glyph is missing.
	Sig FontSig
	// NotTrueType is set when w:notTrueType="1" — distinguishes bitmap /
	// vector fonts from TTF when picking fallbacks.
	NotTrueType bool
}

// FontSig mirrors w:sig: the Unicode subrange / codepage coverage bitfields.
type FontSig struct {
	USB0, USB1, USB2, USB3 uint32
	CSB0, CSB1             uint32
}

// Person is one author entry from word/people.xml.
type Person struct {
	ID         string
	Name       string
	ProviderID string
	Email      string
}

// CommentExtended carries word/commentsExtended.xml metadata.
type CommentExtended struct {
	ParaID       string
	ParentParaID string
	Done         bool
	// DurableID is w16cid:durableId from commentsIds.xml — a stable
	// identifier Word uses to round-trip comment IDs through revisions.
	DurableID string
}

// CommentMeta is the cluster of attributes Word stores on each w:comment
// element. The body content is in Document.Comments.
type CommentMeta struct {
	Author   string
	Date     string
	Initials string
	ParaID   string
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
	// DocumentProtection captures w:documentProtection. Zero-value means
	// no protection. Edit is one of "readOnly", "comments", "trackedChanges",
	// "forms", "none". Enforcement is true when w:enforcement="1" and the
	// document protection is actually active.
	DocumentProtection DocProtection
	// WriteProtection mirrors w:writeProtection (separate element); when
	// Enforcement is true the document is meant to open read-only unless
	// the password is supplied.
	WriteProtection WriteProtection
	// AutoHyphenation is w:autoHyphenation — when true, Word automatically
	// hyphenates long words at line breaks. The renderer treats this as
	// advisory metadata only (it currently does not perform hyphenation).
	AutoHyphenation bool
	// DoNotHyphenateCaps is w:doNotHyphenateCaps — suppress hyphenation
	// for all-caps words even when AutoHyphenation is on.
	DoNotHyphenateCaps bool
	// HyphenationZoneTwips is the maximum allowed line-end whitespace in
	// twips before Word inserts a hyphen.
	HyphenationZoneTwips int
	// ConsecutiveHyphenLimit caps the number of consecutive hyphenated
	// lines (0 = no limit).
	ConsecutiveHyphenLimit int
	// CharacterSpacingControl is w:characterSpacingControl — one of "",
	// "doNotCompress", "compressPunctuation", "compressPunctuationAndJapaneseKana".
	// Drives Word's CJK punctuation compression rules.
	CharacterSpacingControl string
	// DecimalSymbol is w:decimalSymbol — the locale-specific decimal point
	// used by Word when rendering numeric fields (`\#` switches, MERGEFIELD
	// values). Defaults to "." when absent; "," is the typical European
	// override.
	DecimalSymbol string
	// TrackChangeNumbering is w:trackChangeNumbering — when true Word
	// numbers tracked-change records continuously across sessions for
	// merge-aware review workflows. The renderer doesn't number changes
	// (it tags runs with author+type only), so the flag is recorded for
	// round-tripping and surfaced via Options for callers that integrate
	// with external review pipelines.
	TrackChangeNumbering bool
	// ListSeparator is w:listSeparator — the locale-specific thousands /
	// argument separator. Defaults to "," when absent; ";" is the typical
	// European override (matches Excel function-argument convention).
	ListSeparator string
	// Compat captures the subset of w:compat options we surface so callers
	// can branch on Word version differences.
	Compat CompatOptions
	// MathProps mirrors w:settings/m:mathPr — the document-level OMML
	// formatting defaults (default math font, n-ary limit location,
	// break-binary policy, etc.). Empty when the doc carries no math.
	MathProps MathProps
	// TrackChanges is w:trackChanges — the document-level "Track Changes"
	// toggle. When true Word records every new edit. Renderers use the
	// flag to decide whether to draw revision markup by default.
	TrackChanges bool
	// RevisionView mirrors w:revisionView — a set of toggles that
	// determine which revision categories the user wants to *see*. Word
	// writes inverted booleans (default 0 = show), so the parser inverts
	// them on load: true means "show this revision class".
	RevisionView RevisionView
	// StrictFirstAndLastChars is w:strictFirstAndLastChars — when true
	// Word uses the strict (full) JIS X 4051 kinsoku set for line-end
	// punctuation. Otherwise the relaxed default set applies.
	StrictFirstAndLastChars bool
	// NoLineBreaksAfter / Before map lang → custom kinsoku character
	// strings supplied by the user (w:noLineBreaksAfter / Before).
	NoLineBreaksAfter  map[string]string
	NoLineBreaksBefore map[string]string
}

// MathProps mirrors m:mathPr inside settings.xml — global OMML defaults.
// Renderers consult it to pick a math font (Cambria Math by default),
// decide where n-ary limits go (sub/super vs under/over), and how
// fractions/breaks are laid out.
type MathProps struct {
	MathFont    string // m:mathFont val — default "Cambria Math"
	BrkBin      string // m:brkBin val (before/after/repeat)
	BrkBinSub   string // m:brkBinSub val (--/-+/+-)
	SmallFrac   bool   // m:smallFrac
	DispDef     bool   // m:dispDef
	LMargin     int    // m:lMargin (twips)
	RMargin     int    // m:rMargin (twips)
	DefJc       string // m:defJc val (left/right/center/centerGroup)
	WrapIndent  int    // m:wrapIndent (twips)
	WrapRight   bool   // m:wrapRight
	IntLim      string // m:intLim val (subSup/undOvr)
	NaryLim     string // m:naryLim val (subSup/undOvr)
	PreSp       int    // m:preSp (twips)
	PostSp      int    // m:postSp (twips)
	InterSp     int    // m:interSp (twips)
	IntraSp     int    // m:intraSp (twips)
}

// RevisionView mirrors w:revisionView in settings.xml. Each flag tells the
// renderer whether to display that category of revision. Word writes
// inverted booleans (the attribute defaults to 0, meaning "show"), so the
// parser converts to positive booleans for consumer ergonomics.
type RevisionView struct {
	Markup          bool // markup balloons / change bars
	Comments        bool
	InsDel          bool // insertions/deletions
	Formatting      bool // formatting changes (w:rPrChange/w:pPrChange)
	InkAnnotations  bool
}

// CompatOptions mirrors the most consequential w:compat children. Each
// flag is *bool so callers can tell "absent" (defer to renderer default)
// from "explicitly false". We surface only options that affect rendering;
// the long tail of revisionable knobs (printerMetrics, etc.) is ignored.
type CompatOptions struct {
	DoNotExpandShiftReturn       *bool
	UseSingleBorderForContiguousCells *bool
	GrowAutofit                  *bool
	NoLeading                    *bool
	SpacingInWholePoints         *bool
	BalanceSingleByteDoubleByteWidth *bool
	DoNotUseEastAsianBreakRules  *bool
	SuppressTopSpacing           *bool
	UlTrailSpace                 *bool
	DoNotLeaveBackslashAlone     *bool
	UseFELayout                  *bool
	// SpaceForUL: pad underline runs with a trailing space's width so the
	// underline appears uniform (Word 6 / 95 compatibility quirk).
	SpaceForUL *bool
	// UnderlineTabInNumList: extend list-marker underlines to cover the
	// follow-on tab; common in older legal-doc converters.
	UnderlineTabInNumList *bool
	// DoNotBreakWrappedTables: prevent floating tables from breaking
	// across pages. Honored by the renderer as a layout hint.
	DoNotBreakWrappedTables *bool
	// DoNotUseHTMLParagraphAutoSpacing: disable HTML's auto-paragraph
	// spacing rules even when individual paragraphs declared them.
	DoNotUseHTMLParagraphAutoSpacing *bool
	// AdjustLineHeightInTable: revert to Word 97's table line-height
	// algorithm, which adds extra leading.
	AdjustLineHeightInTable *bool
}

// DocProtection captures the bits of w:documentProtection we surface.
// Hash / salt fields are deliberately omitted — we don't enforce, just
// record the policy.
type DocProtection struct {
	Edit             string
	Enforcement      bool
	FormatLockdown   bool
	AlgorithmName    string
	CryptProviderTyp string
}

// WriteProtection mirrors w:writeProtection.
type WriteProtection struct {
	Recommended      bool
	Enforcement      bool
	AlgorithmName    string
	CryptProviderTyp string
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
	// TotalTime is the total document editing time in minutes — surfaced by
	// the EDITTIME field. Parsed from docProps/app.xml `<TotalTime>`.
	TotalTime int
	// Keywords and Comments come from docProps/core.xml.
	Keywords string
	Comments string
	// CreateDate / ModifyDate / PrintDate are RFC-3339 timestamps; surfaced
	// by the matching CREATEDATE / SAVEDATE / PRINTDATE fields. Empty when
	// the doc didn't record them.
	CreateDate string
	ModifyDate string
	PrintDate  string
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
	// ChapStyle is the heading style level (1..9) prefixed before the
	// page number when w:chapStyle is set; ChapSep is the separator
	// character ("hyphen", "period", "colon", "emDash", "enDash").
	ChapStyle int
	ChapSep   string
}

// PageBorders encodes w:pgBorders — colored frame around the page.
type PageBorders struct {
	Top, Bottom, Left, Right BorderEdge
	// OffsetFromText is w:offsetFrom — "page" (default) measures the inset
	// from the page edge; "text" measures it from the text edge (margin).
	OffsetFromText bool
	// OffsetTopPt / Bottom / Left / Right encode w:pgBorders edge offsets
	// (Word stores in points directly here, NOT twips). Default 24pt when
	// unset, matching Word's typical 24pt page border inset.
	OffsetTopPt, OffsetBottomPt, OffsetLeftPt, OffsetRightPt float64
	// Display is the w:display attribute: "" / "allPages" (default),
	// "firstPage" (only on the cover), "notFirstPage" (every page except
	// the first). The renderer consults this in drawPageBorders so cover
	// pages don't carry a frame that should only appear inside.
	Display string
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
	// "oddPage", "nextColumn". All five are now honored at render time:
	//   continuous → flow into existing page with new geometry
	//   evenPage / oddPage → insert a blank page if needed to land on
	//                        the requested parity
	//   nextColumn → advance to next column when columns are active;
	//                otherwise fall back to a normal page break
	//   nextPage (default) → start on a new page
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
	// ColumnSeparator is w:cols w:sep — when true, the renderer draws a
	// thin vertical rule between adjacent columns.
	ColumnSeparator bool
	// ColumnEqualWidth mirrors w:cols w:equalWidth. When false, ColumnSpecs
	// carries per-column widths; otherwise the renderer derives them
	// uniformly from page width.
	ColumnEqualWidth bool
	// ColumnSpecs lists per-column widths + trailing space (the space
	// between this column and the next) in twips. Empty when equalWidth
	// is true.
	ColumnSpecs []ColumnSpec
	// VAlign is w:vAlign — vertical page alignment for the section:
	// "" (default top), "center", "both" (justify), "bottom". Cover pages
	// often set this to "center" for the title.
	VAlign string
	// DocGrid is w:docGrid — the CJK line/character grid. When type is
	// "lines" or "linesAndChars" the renderer enforces an exact line height
	// derived from linePitch (1/20 pt), giving the per-page line-count look
	// that East-Asian docs expect.
	DocGrid DocGrid
	// FormProt is w:formProt — section is form-protected.
	FormProt bool
	// RtlGutter mirrors the gutter onto the right side for RTL languages.
	RtlGutter bool
	// FootnotePr / EndnotePr override doc-level note configuration for
	// this section: position, numbering format, restart policy, start at N.
	FootnotePr *NoteConfig
	EndnotePr  *NoteConfig
	// PrChange records w:sectPrChange — tracked-change of section
	// properties (page size/margins/columns adjusted under revision).
	PrChange *PrChange
}

// ColumnSpec is one column of an unequal-width multi-column section
// (w:col inside w:cols). WidthTwips is the column body width; SpaceTwips
// is the gap to the next column (zero on the last).
type ColumnSpec struct {
	WidthTwips int
	SpaceTwips int
}

// DocGrid captures w:docGrid's three knobs.
type DocGrid struct {
	Type      string // "", "lines", "linesAndChars", "snapToChars", "default"
	LinePitch int    // 1/20 pt per line
	CharSpace int    // 1/100 pt added per char (only for linesAndChars)
}

// NoteConfig captures w:footnotePr / w:endnotePr settings.
type NoteConfig struct {
	Pos      string // "pageBottom", "beneathText", "sectEnd", "docEnd"
	NumFmt   string // "decimal", "upperRoman", ...
	NumStart int    // start at N
	Restart  string // "continuous", "eachSect", "eachPage"
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
	// LinkedCharStyle is w:link — when a paragraph style is "linked" to a
	// character style, applying the paragraph style should auto-apply the
	// linked character style to every run that doesn't otherwise override.
	LinkedCharStyle string
	// NumPr captures w:pPr/w:numPr inside the style definition: any
	// paragraph that adopts this style and doesn't supply its own numPr
	// should inherit the list it references. NumID 0 means "no list".
	NumPr ListInfo
	// --- metadata (round-trip / UI hints) ---
	Name           string // human-readable name (w:name)
	Aliases        string // comma-separated alternate names
	NextStyleID    string // w:next — style applied to the paragraph after
	IsDefault      bool   // w:default — Word's "default" attribute
	IsCustom       bool   // w:customStyle="1"
	UIPriority     int    // sort key for Word's Style gallery
	Hidden         bool   // Word's UI hides this style
	SemiHidden     bool
	UnhideWhenUsed bool
	QFormat        bool
	Locked         bool
}

// LatentStyle captures one w:lsdException entry from styles.xml.
// Consumers use this to round-trip Word's UI gallery metadata.
type LatentStyle struct {
	Name           string
	UIPriority     int
	SemiHidden     bool
	UnhideWhenUsed bool
	QFormat        bool
	Locked         bool
	PrimaryStyle   bool
}

// LatentStylesInfo is the docDefaults companion: defaults applied to every
// lsdException that doesn't override the attribute.
type LatentStylesInfo struct {
	DefLockedState    bool
	DefUIPriority     int
	DefSemiHidden     bool
	DefUnhideWhenUsed bool
	DefQFormat        bool
	Count             int
	Exceptions        map[string]LatentStyle // keyed by Name
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
	// WidowControl — preserve at least 2 lines of the paragraph on a page
	// (default true in Word). Parsed but not yet honored at layout time.
	WidowControl *bool
	// MirrorIndents flips IndentLeftPt/Right on mirrored (verso) pages.
	MirrorIndents bool
	// AdjustRightInd — auto-adjust the right indent for East-Asian wrap.
	AdjustRightInd bool
	// SnapToGrid — when false, the paragraph opts out of the section's
	// docGrid line snapping.
	SnapToGrid *bool
	// OutlineLvl is w:outlineLvl (0-9). When >= 0 this paragraph contributes
	// to the PDF outline even if its style is not Heading*.
	OutlineLvl int
	// TextDirection is one of "lrTb" (default), "tbRl", "btLr", "lrTbV",
	// "tbRlV", "tbLrV". When non-default the renderer rotates the
	// paragraph's drawing region (vertical CJK text).
	TextDirection string
	// TextAlignment is the vertical baseline anchor inside a line:
	// "top", "center", "baseline", "bottom", "auto" (default "auto").
	TextAlignment string
	// CJK line-break / spacing controls.
	Kinsoku       *bool // w:kinsoku — honor line-break rules for CJK
	WordWrap      *bool // w:wordWrap — allow word break for Latin
	OverflowPunct *bool // w:overflowPunct — punctuation may hang outside text area
	TopLinePunct  *bool // w:topLinePunct — leading punctuation compression
	AutoSpaceDE   *bool // w:autoSpaceDE — auto-space CJK/Latin
	AutoSpaceDN   *bool // w:autoSpaceDN — auto-space CJK/numeric
	// SuppressLineNumbers is w:suppressLineNumbers — when true, this
	// paragraph's lines are excluded from the section's line-numbering
	// count even if the section enables w:lnNumType. Common on legal-
	// caption paragraphs that should stay un-numbered amid numbered text.
	SuppressLineNumbers bool
	// SuppressAutoHyphens is w:suppressAutoHyphens — when true, the
	// paragraph opts out of section-level auto-hyphenation. Recorded for
	// docx round-tripping; this renderer doesn't auto-hyphenate so the
	// flag has no visible effect today.
	SuppressAutoHyphens bool

	// endsSection is set when this paragraph's pPr contained an inline sectPr.
	// Internal-only: the parser uses it to know when to close out a section.
	endsSection bool

	// PrChange records a tracked change of paragraph or section properties.
	// Set when w:pPrChange or w:sectPrChange appears inside this paragraph's
	// pPr; the renderer paints a left-margin change bar when ShowRevisions
	// is on.
	PrChange *PrChange
}

// PrChange records that a paragraph / run / section / table / row / cell
// had its properties changed by a tracked-revision author. Renderer uses
// this to paint a change bar in the margin or attach a hover note in the
// PDF outline. Author / Date / ID match the corresponding w:*Change attrs.
type PrChange struct {
	Kind   string // "pPr" / "rPr" / "sectPr" / "tblPr" / "tcPr" / "trPr" / "tblGrid"
	ID     string
	Author string
	Date   string
	// SnapshotXML, when non-empty, carries the OOXML sub-tree of the
	// pre-change properties (the children of w:pPrChange / w:rPrChange /
	// etc.). AST consumers that need to render a "before" view — diff
	// tools, audit exporters — can re-parse this string. Empty when the
	// snapshot wasn't captured at parse time. The renderer itself does
	// not consult it — the change-bar gutter is sufficient for PDF.
	SnapshotXML string
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
	// LinkTooltip mirrors w:hyperlink w:tooltip — text Word shows when
	// the user hovers the link. PDF has no native tooltip surface, but
	// we preserve the metadata so AST consumers can introspect it.
	LinkTooltip string
	Bookmark   string // when set, this is a marker placing a named anchor here
	// Explicit image size in points (from wp:extent in EMU). Zero means
	// "use the image's native dimensions scaled to content width if too big."
	ImageWidthPt, ImageHeightPt float64
	// ImageRotationDeg / ImageFlipH / ImageFlipV mirror a:xfrm on a
	// drawing's <pic:pic> sub-tree. Applied at render time around the
	// image's bounding box.
	ImageRotationDeg float64
	ImageFlipH       bool
	ImageFlipV       bool
	// Image source-rect crop in PERCENT (a:srcRect attrs are 1/1000 of percent
	// from each edge). E.g. CropTop=10000 = 10%. Zero = no crop on that side.
	CropTopPct, CropBottomPct, CropLeftPct, CropRightPct float64
	// ImageEffects, when non-nil, lists DrawingML pixel-effect filters that
	// should be applied to the image before placement. See ImageEffect.
	ImageEffects []ImageEffect
	// ImageAnchored is true if the run comes from a wp:anchor (floating
	// image) rather than wp:inline. Renderer still draws inline as a
	// best-effort fallback; AnchorAlignH/V capture the requested anchor
	// alignment ("left", "center", "right", "inside", "outside") so the
	// inline placement can at least approximate the source location.
	ImageAnchored                    bool
	AnchorAlignH, AnchorAlignV       string
	AnchorOffsetXPt, AnchorOffsetYPt float64
	AnchorWrap                       string // "", "none", "square", "tight", "through", "topAndBottom"
	// AnchorWrapPolygon carries the wp:wrapPolygon path on wp:wrapTight /
	// wp:wrapThrough. Coordinates are unscaled integers in the polygon's
	// own coordinate space; the renderer scales them by 21600 to obtain
	// PDF points relative to the image's bounding box. Empty when wrap is
	// rectangular (square / topAndBottom) or absent.
	AnchorWrapPolygon []WrapPathPoint
	// AnchorSimplePos carries wp:simplePos when wp:anchor was emitted with
	// simplePos="1" — legacy Word 2003 positioning where the anchor's
	// x/y are absolute EMUs from the page top-left. Non-zero
	// AnchorSimplePosUsed flips the renderer onto that placement path.
	AnchorSimplePosUsed                bool
	AnchorSimplePosXPt, AnchorSimplePosYPt float64
	// FootnoteID, when non-empty, tags this run as a footnote / endnote
	// reference site. The visible Text is still drawn (typically as a
	// superscript marker); the renderer also queues the corresponding note
	// body for the current page's bottom area.
	FootnoteID string
	// CustomRefMark, when non-empty, is the literal mark (e.g. "*", "†")
	// the author supplied for this footnote/endnote reference site via
	// w:footnoteReference/@w:customMarkFollows="1". When set, the
	// renderer should suppress the auto-number and render the next
	// character of the run as the visible marker instead.
	CustomRefMark string
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

	// InkPlaceholder marks a w14:ink run whose glyph stream we couldn't
	// rasterize.
	InkPlaceholder bool

	// Math, when non-nil, carries the structural OMML tree for this run.
	// The renderer prefers it over Text and lays out a 2D math expression
	// (fraction bars, sized brackets, radicals with vinculum, etc.).
	// Text is set in parallel as the searchable fallback.
	Math *MathNode

	// FormField, when non-nil, marks this run as a legacy form field
	// (w:fldChar w:fldCharType="begin" wrapping a w:ffData). The renderer
	// turns the form's default/checked/selected state into visible glyph
	// content when Word didn't cache a result region.
	FormField *FormFieldInfo

	// AltText, when non-empty, supplies an accessibility description for
	// this run's image / shape (wp:docPr w:descr or w:title). Surfaced into
	// PDF /Alt entries when the underlying writer supports them.
	AltText string

	// Ruby, when non-nil, decorates this run with an interlinear ruby
	// annotation drawn above the base text.
	Ruby *RubyInfo

	// Ptab, when non-nil, marks this run as a w:ptab — a positional
	// tab whose stop is computed from the surrounding margin or page
	// instead of the paragraph's w:tabs list. Text is "\t" so the
	// renderer's normal tab path picks it up; the renderer then
	// substitutes the alignment / leader / anchor from this struct.
	Ptab *PtabInfo

	// DirOverride, when non-empty, carries a forced text direction
	// (one of "ltr" / "rtl") inherited from a wrapping w:bdo / w:dir
	// element. Renderer treats the run as if it lives inside a
	// directional override at bidi reordering time.
	DirOverride string

	// InlineDateKind, when non-empty, marks this run as Word's run-
	// internal date/page placeholder: "pgNum", "dayLong", "dayShort",
	// "monthLong", "monthShort", "yearLong", "yearShort". The
	// renderer expands it on draw using the current locale and
	// document page state — semantically equivalent to a one-token
	// DATE / PAGE field, but emitted by Word as a direct run child
	// rather than as a field code.
	InlineDateKind string

	// VMLShape, when non-nil, describes a legacy VML shape (v:rect /
	// v:line / v:oval / v:roundrect) that should render as a geometric
	// primitive in place of an image.
	VMLShape *VMLShape

	// RevisionType records that this run is inside a tracked-change
	// wrapper: "ins", "del", "moveTo", "moveFrom", or "" (none). The
	// renderer either drops del/moveFrom runs (accept mode, the default)
	// or decorates them visually (show-revisions mode). Nested wrappers
	// keep the innermost tag so display reflects the final intent.
	RevisionType string
	// RevisionAuthor is the w:author attribute on the parent ins/del
	// wrapper. Surfaced for accessibility and for show-revisions color
	// derivation.
	RevisionAuthor string
	// PrChange records w:rPrChange — tracked-change of run properties
	// (a run whose rPr was edited under revision). The renderer prints
	// a change bar in the margin when ShowRevisions is on.
	PrChange *PrChange
}

// PtabInfo describes a w:ptab (positional tab). docx4j calls these
// "positional tabs": the stop is computed relative to a layout anchor
// rather than from the paragraph's w:tabs list.
type PtabInfo struct {
	// Alignment is one of "left" (default), "center", "right".
	Alignment string
	// RelativeTo is one of "margin" (default), "indent", "page".
	// "margin" anchors the stop to the right margin of the
	// content area; "indent" to the paragraph's right indent;
	// "page" to the page's right edge.
	RelativeTo string
	// Leader is one of "none" (default), "dot", "hyphen",
	// "underscore", "middleDot".
	Leader string
}

// RubyInfo decorates a run with interlinear ruby annotation.
type RubyInfo struct {
	Text     string
	HpsRaise float64
	Hps      float64
	Align    string
	LangAttr string
}

// ChartData is the renderable shape of a parsed chartN.xml part. We
// only model the bits the renderer can paint: chart family, series, and
// category labels. Stylistic flourishes (3D, gradients, glow effects)
// are deliberately dropped.
type ChartData struct {
	// Kind is one of "bar", "column", "pie", "doughnut", "line", "scatter".
	// Empty when the chart part used an unrecognized chart type — callers
	// should fall back to the flat text in Document.Charts in that case.
	Kind   string
	Title  string
	Series []ChartSeries
	// Categories are the X-axis labels (or pie slice labels). May be
	// shorter than series.Values; missing entries default to "1", "2", ...
	Categories []string
	// Grouping captures the c:grouping value: "clustered" (default for
	// bar/column), "stacked", "percentStacked", or "standard"/"stacked"/
	// "percentStacked" for area/line. Empty means clustered.
	Grouping string
	// XAxisTitle / YAxisTitle hold the axis labels from c:catAx / c:valAx
	// titles. Empty when the chart part doesn't supply them.
	XAxisTitle string
	YAxisTitle string
	// CategoryAxisDeleted / ValueAxisDeleted mirror c:catAx@delete /
	// c:valAx@delete — when true, hide the axis line + tick labels.
	CategoryAxisDeleted bool
	ValueAxisDeleted    bool
	// DataLabels captures c:dLbls flags. Renderers use these to know
	// whether each datapoint should print its value, category name,
	// series name, or percentage (pie/doughnut).
	DataLabels DataLabelOptions
}

// DataLabelOptions mirrors c:dLbls children that toggle per-point label
// visibility. ECMA-376 §21.2.2.61.
type DataLabelOptions struct {
	ShowVal      bool
	ShowCatName  bool
	ShowSerName  bool
	ShowPercent  bool
	ShowLegendKey bool
	ShowBubbleSize bool
}

// ChartSeries is one plotted line / bar group / pie ring.
type ChartSeries struct {
	Name   string
	Values []float64
	// Color, when non-empty, is the explicit 6-hex series color from the
	// chart part. Empty entries fall back to a palette picked by index.
	Color string
}

// VMLShape captures the rendering knobs of a legacy VML primitive.
type VMLShape struct {
	Kind           string // "rect", "roundrect", "oval", "line", "polyline", "group", or a DrawingML prstGeom name
	WidthPt        float64
	HeightPt       float64
	FillColor      string
	StrokeColor    string
	StrokeWeightPt float64
	Points         string // for v:polyline
	CornerArc      float64
	TextBox        string
	// TextBoxBlocks, when non-nil, holds the parsed paragraph/table tree
	// from the shape's <w:txbxContent>. The renderer prefers this over
	// TextBox when present so bold/italic/lists/nested-tables inside the
	// text box survive into the PDF instead of being flattened to one line.
	TextBoxBlocks []Block
	// Chart, when non-nil, declares this shape is a chart placeholder
	// the renderer should paint as a real bar/pie/line graphic. Set on
	// shapes synthesized from a chart drawing reference (c:chart r:id).
	Chart *ChartData
	// Children, when non-empty, lists nested shapes whose coordinates are
	// in the parent's local space (origin at parent's top-left). Used for
	// v:group recursion.
	Children []VMLShape
	// OffsetXPt / OffsetYPt position a child shape within its parent
	// group's coordinate space. Both default to 0. The renderer reads
	// these on group children so SmartArt nodes / explicit dsp:sp
	// shapes land where the source XML asked instead of getting
	// auto-tiled.
	OffsetXPt, OffsetYPt float64
	// CoordSizeW / CoordSizeH define the "world" coordinate space of a
	// group: child OffsetX/Y/Width/Height are interpreted as points in
	// this world and projected into the group's bounding rect. Zero
	// means "fall back to the group's own width/height", which gives
	// 1:1 mapping for shapes synthesized with absolute coordinates.
	CoordSizeW, CoordSizeH float64
	// CustomPath, when non-empty, is a DrawingML <a:custGeom> path-list as
	// a flat token list ("M 0 0 L 1 0 L 1 1 Z" style — m/l/c/q/z plus
	// numeric tokens). Coordinates are in the shape's local 0..1 space.
	CustomPath string
	// GradientKind is "linear" / "radial" / "" (no gradient — fall back to
	// FillColor). When set, GradientStops drives the gradient color ramp.
	GradientKind string
	// GradientAngle is the linear-gradient angle in DEGREES (0 = left→
	// right, 90 = top→bottom, increasing clockwise). Unused for radial.
	GradientAngle float64
	GradientStops []GradientStop
	// HeadEnd / TailEnd carry a:ln/a:headEnd / a:tailEnd attributes — the
	// arrow-style decoration painted at the line's start / end. Type is
	// one of "none" (default), "triangle", "stealth", "arrow", "oval",
	// "diamond". The renderer only honors these on "line"-kind shapes.
	HeadEnd string
	TailEnd string
	// Shadow, when non-nil, adds an outer drop shadow drawn before the
	// shape itself.
	Shadow *ShadowEffect
	// Pattern, when non-nil, names a DrawingML pattern preset (dkHorz /
	// pct25 / wave / etc.) plus its fg/bg colors. The renderer paints a
	// repeating tile inside the shape bounds before stroking the outline.
	Pattern *PatternFill
	// RotationDeg is the clockwise rotation in degrees applied around the
	// shape's center at draw time. Sourced from a:xfrm rot (Word stores
	// 60000ths of a degree there; the parser converts). 0 = no rotation.
	RotationDeg float64
	// FlipH / FlipV mirror the shape along its vertical / horizontal axis.
	// gopdf has no native scale-with-sign primitive, so we approximate
	// flipH+flipV as a 180° rotation (visually identical for symmetric
	// rects/ovals) and flag-only flips as a rotate(±180) on the appropriate
	// axis. Asymmetric shapes (polylines, custGeoms) won't survive this
	// approximation cleanly, but the alternative is a custom CTM emit
	// against the raw page stream which is outside our gopdf-only scope.
	FlipH bool
	FlipV bool
	// InnerShadow / Glow / Reflection are best-effort approximations of
	// the DrawingML a:innerShdw / a:glow / a:reflection effects. We
	// stroke an inset thin line for inner shadow, paint a faint halo for
	// glow, and draw a clipped mirrored copy at 60% opacity for
	// reflection. Zero values mean "no effect of this kind".
	InnerShadow *ShadowEffect
	Glow        *GlowEffect
	Reflection  *ReflectionEffect
	// TextAnchor mirrors a:bodyPr/@anchor ("t" / "ctr" / "b" / "just" /
	// "dist") — vertical alignment of the text inside the shape. Default
	// "t" (top). The renderer maps "ctr"→center and "b"→bottom; "just"
	// and "dist" fall back to top.
	TextAnchor string
	// TextInsets are a:bodyPr/@lIns / tIns / rIns / bIns in points.
	// Zero means "use Word's default 7.2pt × 3.6pt × 7.2pt × 3.6pt"
	// (the values Office actually ships).
	TextLeftInsetPt, TextTopInsetPt, TextRightInsetPt, TextBottomInsetPt float64
	// TextVertical mirrors a:bodyPr/@vert: "" (default horizontal),
	// "eaVert" / "wordArtVert" (rotate 90°), "vert270" (rotate -90°),
	// "horz" (explicit horizontal). The renderer rotates the text run
	// inside the shape's local rect at draw time.
	TextVertical string
	// TextAutoFit captures a:bodyPr's child element: "" (none), "normAutofit"
	// (shrink text to fit), or "spAutoFit" (grow shape to fit text — Word
	// resizes the shape; we apply a font-shrink factor when this is set
	// since we can't grow the parent shape mid-layout).
	TextAutoFit string
}

// ImageEffect describes a per-pixel filter that lives under a:blip — the
// renderer composites them onto the raster before placement. Kind is the
// element name (alphaModFix / lum / biLevel / duotone / clrChange /
// grayscl / blur). The remaining fields carry the operands; unused fields
// are zero.
type ImageEffect struct {
	Kind string
	// AmountPct is 0..100 — used by alphaModFix.Amount and lum's
	// bright/contrast deltas (signed for those: -100..100).
	Amount float64
	// Bright / Contrast deltas (a:lum), 1/1000 of percent so we normalize
	// to fraction. Range: roughly -1..1 each.
	Bright   float64
	Contrast float64
	// Threshold for a:biLevel, 0..1 (default 0.5 when zero).
	Threshold float64
	// FgHex / BgHex carry duotone's two end-points or clrChange's from→to.
	FgHex string
	BgHex string
	// BlurRadiusPx for a:blur — captured but the renderer typically maps
	// this to a softer fade rather than a true gaussian.
	BlurRadiusPx float64
	// HueDeg / Saturation / Lum carry the a:hsl effect's hue (signed
	// degrees), and saturation / luminance deltas (fractional, -1..1).
	// Renderer applies in linear HSL space, no gamma correction.
	HueDeg     float64
	Saturation float64
	Lum        float64
}

// GlowEffect captures <a:glow> attributes.
type GlowEffect struct {
	RadiusPt float64
	Color    string
	Alpha    float64
}

// ReflectionEffect captures the <a:reflection> attributes the renderer
// can approximate. StartA / EndA control opacity at the top / bottom of
// the mirror; DistPt is the gap between shape and reflection; FadeDirDg
// is the gradient angle (rarely deviates from 90°, i.e. straight down).
type ReflectionEffect struct {
	BlurPt    float64
	Opacity   float64 // legacy single-opacity for older renderers
	StartA    float64 // start alpha (0..1) — top edge
	EndA      float64 // end alpha — bottom edge (typically 0)
	DistPt    float64 // distance from shape
	FadeDirDg float64 // gradient direction in degrees
}

// GradientStop is one entry of a DrawingML <a:gsLst> gradient.
//
//	Pos is the position along the gradient axis as a fraction in [0,1]
//	(Word stores it as a number 0..100000 = 0%..100% which the parser
//	converts).
//	Color is the resolved 6-hex color at that position.
//	Alpha is in [0,1] — 1.0 = fully opaque (default).
type GradientStop struct {
	Pos   float64
	Color string
	Alpha float64
}

// WrapPathPoint is a single vertex of wp:wrapPolygon. Coordinates X/Y are
// unscaled integers in the wrap path's own coordinate space — the OOXML
// convention is that values are 1/21600 of the shape's bounding box. The
// renderer scales by 21600 (or the actual polygon-bounds maximum when
// it exceeds 21600) to obtain PDF points.
type WrapPathPoint struct {
	X int
	Y int
}

// ShadowEffect captures <a:outerShdw> attributes.
//
//	OffsetXPt / OffsetYPt: shadow displacement in points (Y > 0 = below).
//	BlurPt: blur radius approximated as additional stroked outline
//	  thickness (the gopdf backend doesn't have a true blur primitive).
//	Color: 6-hex shadow color (defaults to black).
//	Alpha: 0..1 transparency (1 = opaque).
type ShadowEffect struct {
	OffsetXPt float64
	OffsetYPt float64
	BlurPt    float64
	Color     string
	Alpha     float64
}

// FormFieldInfo carries the parsed bits of w:ffData (legacy form fields).
// All form types share this struct; Kind disambiguates: "text", "checkbox",
// "dropdown".
type FormFieldInfo struct {
	Kind     string   // "text", "checkbox", "dropdown"
	Default  string   // text default value
	Checked  bool     // checkbox state
	Choices  []string // dropdown options
	Selected int      // dropdown selected index (0-based)
	Name     string   // w:name — form field name
}

// RunProps captures character-level formatting we honor.
type RunProps struct {
	Bold       bool
	Italic     bool
	Underline  bool
	// UnderlineStyle captures w:u w:val when set. Empty falls back to
	// "single" (the default single solid line). Renderer honors:
	// "single" / "double" / "dotted" / "dottedHeavy" / "dash" /
	// "dashedHeavy" / "dashLong" / "dashLongHeavy" / "dashDotDotHeavy" /
	// "wave" / "wavyHeavy" / "wavyDouble" / "thick" / "words" / "none".
	UnderlineStyle string
	Strike         bool // w:strike — single-line strikethrough
	// DStrike is w:dstrike — double strikethrough.
	DStrike bool
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
	// Em is the CJK emphasis mark (w:em) — "dot" / "circle" / "underDot" /
	// "comma" etc. The renderer draws a small mark above each glyph of the
	// run. Empty = no emphasis mark.
	Em string
	// Lang carries explicit language hints (w:lang) for the latin / CJK /
	// complex-script halves of the run. The renderer uses Lang.EastAsia
	// to bias the CJK fallback font selection even when the character itself
	// is ambiguous (a full-width digit is "0030 ZERO" — its language tells
	// us whether to draw it with the latin or the CJK face).
	Lang RunLang
	// RTL = w:rtl: the run reads right-to-left (Hebrew/Arabic). When
	// combined with Bidi on the paragraph the renderer reverses glyph
	// order for the run.
	RTL bool
	// CS = w:cs: this run is a complex-script run (Arabic/Hebrew/Thai). The
	// complex-script bold/italic/size attributes (BCs/ICs/SzCs) override the
	// regular B/I/Sz when CS is set.
	CS       bool
	BCs, ICs bool
	SzCs     float64
	// NoProof suppresses spellcheck flags; render-noop, but parsed for
	// completeness so consumers can introspect it.
	NoProof bool
	// WebHidden mirrors Word's "hide in web view" toggle. We treat it like
	// w:vanish for print output (web-hidden text shouldn't appear in PDF).
	WebHidden bool
	// KernThresholdPt is w:kern in half-points → points (font kerning
	// activates above this size threshold). Stored for completeness; the
	// underlying gopdf doesn't expose a kerning toggle so render is a no-op.
	KernThresholdPt float64
	// FitTextID + FitTextWidthPt are w:fitText — squeeze N runs into a
	// fixed width. We don't currently implement the squeeze; stored so the
	// caller can detect it.
	FitTextID      int
	FitTextWidthPt float64
	// TextBorder mirrors w:bdr (a border around the run's text — distinct
	// from paragraph or table borders). Empty Style means "no border".
	TextBorder BorderEdge
	// W14Ligatures captures the requested OpenType ligature mode.
	W14Ligatures    string
	W14ShadowColor  string
	W14OutlineColor string
	// W14NumForm is "lining" (default) or "oldStyle" — selects between
	// figure styles in OpenType fonts that ship both.
	W14NumForm string
	// W14NumSpacing is "default", "proportional" (variable-width digits),
	// or "tabular" (fixed-width digits). Tabular is the better choice
	// for tables/columns; proportional reads more naturally in prose.
	W14NumSpacing string
	// W14CntxtAlts enables contextual alternates ("true"/"false"/""=default).
	W14CntxtAlts string
	// W14Has3D is set when w14:scene3d or w14:props3d are present on the
	// run. Word renders these as extruded/embossed 3D glyphs; we can't
	// do a real projection in 2D, so the renderer paints a layered
	// depth-shadow approximation that at least visually distinguishes
	// "3D text" from flat text.
	W14Has3D bool

	// EALayout is set when w:eastAsianLayout was present on this run.
	EALayout *EALayoutInfo
}

// EALayoutInfo carries w:eastAsianLayout attributes.
type EALayoutInfo struct {
	Combine         bool
	CombineBrackets string
	Vert            bool
	VertCompress    bool
}

// RunLang carries the language hints from w:lang.
type RunLang struct {
	Latin    string // w:val — e.g. "en-US"
	EastAsia string // w:eastAsia — e.g. "zh-CN", "ja-JP"
	Bidi     string // w:bidi — e.g. "ar-SA", "he-IL"
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
	// Layout is "auto" (default — column widths adjust to content) or
	// "fixed" (column widths are strictly the values in ColumnWidthsTwips
	// / tcW). The renderer is currently fixed-only; we record this so it
	// can be honored if/when an auto-fit pass lands.
	Layout string
	// TableWidthTwips / TableWidthType captures w:tblW (table total width).
	// Type is one of "", "auto", "dxa", "pct", "nil". Twips holds the dxa
	// value when Type=="dxa", or the pct value (0..5000 = 0..100%) when
	// Type=="pct".
	TableWidthTwips int
	TableWidthType  string
	// FloatPos carries w:tblPr/w:tblpPr — the floating-table anchor. When
	// non-nil the table is positioned absolutely; the renderer currently
	// still draws it in-flow (no text wrap), but the anchor is preserved
	// for callers that introspect the AST.
	FloatPos *TableFloatPos
	// Overlap is w:tblOverlap: "" or "never".
	Overlap string
	// Caption and Description mirror w:tblCaption / w:tblDescription —
	// accessibility metadata that surfaces in tagged PDFs.
	Caption     string
	Description string
	// DefaultCellMargins captures w:tblCellMar — default margins applied
	// to every cell unless that cell sets w:tcMar.
	DefaultCellMargins CellMargins
	// CellSpacingTwips is w:tblCellSpacing at the table level.
	CellSpacingTwips int
	// IndentTwips is w:tblInd — extra left indent applied to the entire
	// table. Renderer shifts the table's starting X by this amount.
	IndentTwips int
	// Alignment is w:tblPr/w:jc — "", "left", "center", "right" — table
	// alignment within contentW. Rendered by shifting marL like a
	// floating table with XAlign.
	Alignment string
	// BidiVisual is w:tblPr/w:bidiVisual — when true, columns render
	// right-to-left for the entire table.
	BidiVisual bool
	// Shading is w:tblPr/w:shd fill — background color painted behind
	// every cell that doesn't set its own w:shd. 6-hex; "" means none.
	Shading string
	// RowBandSize / ColBandSize are w:tblPr/w:tblStyleRowBandSize /
	// w:tblStyleColBandSize. Default 1 (alternate every row/column);
	// the conditional banding logic uses (ri / RowBandSize) parity.
	RowBandSize int
	ColBandSize int
	// PrChange records w:tblPrChange / w:tblGridChange — tracked-change
	// of table properties / grid. Used for change-bar margin markers.
	PrChange *PrChange
}

// PermissionRange captures a w:permStart / w:permEnd pair. EdGrp is one
// of "everyone" / "current" / "administrators" / "contributors" /
// "editors" / "owners" or the literal w:ed user/group name. The
// renderer ignores this — purely metadata for AST consumers.
type PermissionRange struct {
	ID         string
	EditorGroup string // w:edGrp
	Editor      string // w:ed
}

// CellMargins in points.
type CellMargins struct {
	Top, Bottom, Left, Right float64
}

// TableFloatPos captures w:tblpPr — anchor coordinates and text-wrap
// behavior for a floating table. Mirrors the FrameInfo shape used for
// paragraph frames.
type TableFloatPos struct {
	HAnchor                                                                      string // "margin", "page", "text"
	VAnchor                                                                      string // "margin", "page", "text"
	XAlign                                                                       string // "left", "center", "right", "inside", "outside"
	YAlign                                                                       string // "top", "center", "bottom", "inside", "outside"
	XTwips                                                                       int
	YTwips                                                                       int
	LeftFromTextTwips, RightFromTextTwips, TopFromTextTwips, BottomFromTextTwips int
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
	// HeightRule captures the raw w:trHeight hRule attribute: "auto"
	// (height is content-driven; HeightTwips is a hint), "atLeast"
	// (HeightTwips is a minimum), or "exact". Empty is treated as
	// "auto" per ECMA-376. The HeightRuleExact bool is a cached copy
	// of (HeightRule == "exact") for callers that only need that case.
	HeightRule string
	// CantSplit means the row must be drawn intact — if it won't fit on the
	// current page, push it to the next page first.
	CantSplit bool
	// WBeforeTwips / WAfterTwips are w:wBefore / w:wAfter — extra leading
	// or trailing column space rendered as a blank spacer column.
	WBeforeTwips, WAfterTwips int
	// GridBefore / GridAfter are w:gridBefore / w:gridAfter — number of
	// phantom grid-grid columns inserted at the leading/trailing edge of
	// the row, soaking widths from the table's column grid.
	GridBefore, GridAfter int
	// CellSpacingTwips is w:tblCellSpacing — space between cells. Row-level
	// override of the table-level value.
	CellSpacingTwips int
	// Alignment is w:trPr/w:jc — row-level horizontal alignment override.
	// Same values as Table.Alignment.
	Alignment string
	// Hidden is w:trPr/w:hidden — the row is suppressed entirely.
	Hidden bool
	// CnfStyle is w:trPr/w:cnfStyle — explicit conditional-formatting flags
	// for this row, used in addition to the tblLook computed flags.
	CnfStyle CnfStyle
	// PrChange records w:trPrChange — tracked-change of row properties.
	PrChange *PrChange
	// TblPrEx captures w:tblPrEx — table-level property exceptions
	// scoped to this single row. Word writes these when a row gets
	// pasted from a differently-styled table; without honoring them the
	// row inherits the surrounding tbl's borders/margins. Nil when the
	// row is in its native context.
	TblPrEx *TblPrEx
}

// TblPrEx mirrors w:tblPrEx — the per-row override block. Only the
// children that meaningfully affect rendering are captured; the rest is
// preserved in PrChange for round-trip via revision history.
type TblPrEx struct {
	Borders          TableBorders
	HasBorders       bool
	Shading          string
	CellMargins      CellMargins
	HasCellMargins   bool
	CellSpacingTwips int
	IndentTwips      int
	Layout           string
	Look             TableLook
	HasLook          bool
}

// CnfStyle is the parsed w:cnfStyle bitfield used on rows / cells to mark
// that this row or cell should receive the named conditional-formatting
// slot from the table style, regardless of position in the table.
type CnfStyle struct {
	FirstRow    bool
	LastRow     bool
	FirstColumn bool
	LastColumn  bool
	Band1Horz   bool
	Band2Horz   bool
	Band1Vert   bool
	Band2Vert   bool
	NWCell      bool
	NECell      bool
	SWCell      bool
	SECell      bool
}

// Any reports whether any flag is set.
func (c CnfStyle) Any() bool {
	return c.FirstRow || c.LastRow || c.FirstColumn || c.LastColumn ||
		c.Band1Horz || c.Band2Horz || c.Band1Vert || c.Band2Vert ||
		c.NWCell || c.NECell || c.SWCell || c.SECell
}

type TableCell struct {
	// A cell may contain paragraphs OR nested tables, in document order.
	// We use the same Block interface as the body so nesting Just Works.
	Blocks []Block
	// GridSpan is the number of columns this cell spans (default 1).
	GridSpan int
	// VMerge is "restart", "continue", or "" (no vertical merge).
	VMerge string
	// HMerge is "restart" or "continue" — Word's deprecated horizontal
	// merge predates GridSpan. Parsed for completeness; the renderer
	// resolves it the same way as GridSpan continuation cells (consumed
	// by the preceding cell's span).
	HMerge string
	// Shading is the 6-hex background fill color (w:shd w:fill).
	Shading string
	// VAlign is "top", "center", "bottom", or "" (default top).
	VAlign string
	// TextDirection rotates the cell's text. One of "" (default lrTb),
	// "tbRl" (90° clockwise, top-to-bottom right-to-left — Chinese/Japanese
	// vertical table headers), "btLr" (270° / 90° counter-clockwise — the
	// classic English rotated header), "lrTbV", "tbRlV", "tbLrV".
	TextDirection string
	// NoWrap suppresses line breaks: cell content stays on a single line
	// even if it overflows the column. Word uses this for narrow numeric
	// columns.
	NoWrap bool
	// HideMark: hide the paragraph-end mark inside this cell so it doesn't
	// contribute to row height. Parsed but unused by the renderer.
	HideMark bool
	// FitText: scale text horizontally to fit the column width.
	FitText bool
	// CellWidthType is "", "auto", "dxa", "pct", "nil"; CellWidthTwips
	// carries the dxa or pct value (pct stored as twips-equivalent).
	CellWidthType  string
	CellWidthTwips int
	// Borders, when set, override the default thin black per-edge borders.
	Borders CellBorders
	// Margins (w:tcMar) in points, defaulting to {Top: 0, Bottom: 0, Left: 4, Right: 4}
	// when zero. We only honor symmetric defaults; per-cell overrides take
	// precedence.
	MarginTopPt, MarginBottomPt, MarginLeftPt, MarginRightPt float64

	// SuppressBottomBorder is set by the renderer's vMerge resolver when
	// this cell's bottom edge falls inside a vertical-merge group (i.e.
	// the row below has a VMerge="continue" cell in the same logical
	// column). Drawing the bottom edge would print a horizontal divider
	// through the merged region.
	SuppressBottomBorder bool
	// MergedHeightPt is set on a VMerge="restart" cell to the cumulative
	// pixel height of every row in the merge group (itself plus every
	// VMerge="continue" cell that follows in the same column). The
	// renderer uses this for vAlign math so the content centers within
	// the visual merged region instead of just the first row.
	MergedHeightPt float64
	// CnfStyle is w:tcPr/w:cnfStyle — explicit conditional-formatting
	// flags for this cell, additive with the tblLook computation.
	CnfStyle CnfStyle
	// PrChange records w:tcPrChange — tracked-change of cell properties.
	PrChange *PrChange
	// CellRevision records cell-level tracked-change markers — w:cellIns,
	// w:cellDel, w:cellMerge. RevisionKind is "ins", "del", or "merge";
	// Author/Date are the change metadata. The renderer (when ShowRevisions
	// is on) draws a corresponding marker on the cell — left bar for ins,
	// strikeout shading for del, a small "M" pin for merge.
	CellRevision *CellRevision
}

// CellRevision carries a single w:cellIns / w:cellDel / w:cellMerge entry.
//
//	Kind: "ins", "del", or "merge".
//	Author / Date / ID: w:author / w:date / w:id attributes (ID kept for
//	  parity with run-level revisions; nothing in the renderer dispatches on
//	  it yet).
//	Vertical: only set for "merge" — Word writes vMerge="rest"/"cont" on
//	  cellMerge to describe the merge direction (vertical vs. horizontal).
type CellRevision struct {
	Kind     string
	Author   string
	Date     string
	ID       string
	Vertical string
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
//
// TL2BR / TR2BL are diagonal lines drawn from corner to corner. Word uses
// them to "cross out" header cells (e.g. the empty corner in a row-+col-
// labeled matrix). They render alongside the rectangular edges; the
// straight edges still take precedence visually.
type CellBorders struct {
	Top, Bottom, Left, Right BorderEdge
	TL2BR, TR2BL             BorderEdge
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
	// Overrides keyed by numId → ilvl → NumOverride. Captures
	// w:lvlOverride inside a w:num: per-numId level swaps and start
	// overrides. Empty map is fine.
	Overrides map[int]map[int]NumOverride
}

type AbstractNum struct {
	Levels       map[int]NumLevel // ilvl → level definition
	StyleLink    string           // w:styleLink — id this abstractNum implements
	NumStyleLink string           // w:numStyleLink — link to canonical abstractNum
	// MultiLevelType is w:multiLevelType: "singleLevel", "multilevel", or
	// "hybridMultilevel". Informational; helps consumers decide whether to
	// show parent counters when lvlText omits %N placeholders.
	MultiLevelType string
	// Tmpl is w:tmpl — the template GUID Word uses to recognise built-in
	// list presets. Round-trip metadata only.
	Tmpl string
	// Nsid is w:nsid — a stable identity Word writes for change-tracking
	// across save cycles. Round-trip metadata only.
	Nsid string
	// Name is w:name — a human-readable label (rarely set; usually empty).
	Name string
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
	// Suff is w:suff — what comes between the marker and the body:
	// "tab" (default), "space", or "nothing".
	Suff string
	// LvlRestart is w:lvlRestart — when set, this level restarts whenever
	// the level value is reached. 0 means "never restart"; negative means
	// "use default". Zero default = renderer uses Word's rule.
	LvlRestart int
	// PStyleLink is w:pStyle — a paragraph style that's linked to this
	// level. Paragraphs carrying that style number from this level
	// automatically.
	PStyleLink string
	// MarkerFontFamily, when non-empty, is the w:rFonts hint pulled from
	// the level's w:rPr — typically "Symbol" / "Wingdings" for bullet
	// glyphs whose code points only render correctly in the source font.
	// The renderer uses it to select an appropriate font when painting
	// the marker.
	MarkerFontFamily string
	// MarkerJc is w:lvlJc: the marker's alignment inside its column —
	// "left" (default), "center", "right", "start", "end". Used by the
	// renderer to push the marker glyph to the right edge of the hanging
	// indent column rather than the left.
	MarkerJc string
	// HideParent is w:lvlText/@w:hideParent (deprecated location) or
	// w:hideParent: when true, parent-level counters are suppressed in
	// lvlText substitution. Rare but valid for hybrid multi-level lists.
	HideParent bool
	// LegacyIndent captures w:legacy / w:legacySpace / w:legacyIndent —
	// the Word 6/95 compat hint for marker placement. We store the indent
	// number; non-zero means "in legacy mode".
	LegacyIndent int
	// CustomFmt holds the format name when w:numFmt has val="custom" with
	// a w:format attribute pointing at a named custom numeric scheme.
	CustomFmt string
}

// NumOverride captures w:lvlOverride for a concrete num. We attach this to
// the Numbering map by storing overrides keyed by (numId, ilvl).
type NumOverride struct {
	// StartOverride > 0 forces a different starting value for this level
	// on this specific numId.
	StartOverride int
	// LvlReplace optionally swaps in a brand-new level definition.
	LvlReplace *NumLevel
}
