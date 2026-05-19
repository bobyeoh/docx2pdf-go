package render

import (
	"image"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// firstNonEmpty returns the first non-empty string among its args.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// formatPageNumber returns the page-number string in the requested format
// after applying the section's "start at N" offset.
func formatPageNumber(page int, fmt string) string {
	if page < 1 {
		page = 1
	}
	switch fmt {
	case "upperRoman":
		return roman(page, true)
	case "lowerRoman":
		return roman(page, false)
	case "upperLetter":
		return alphaLabel(page, true)
	case "lowerLetter":
		return alphaLabel(page, false)
	}
	return strconv.Itoa(page)
}

// fieldVars supplies values for w:instrText codes. The zero value means
// "use the docx-cached field result as-is" — the body's default behavior
// since Word keeps a snapshot of the rendered text so even unsupported
// fields look right enough. Header/footer rendering overrides these per page.
type fieldVars struct {
	page     int
	numPages int
	pageFmt  string
	// chapStyle is the section's w:pgNumType@w:chapStyle (1..9). When >0,
	// PAGE/NUMPAGES field output is prefixed by the chapter number
	// (derived from the most recent heading at chapStyle level) and the
	// chapSep glyph.
	chapStyle int
	// chapSep is one of "hyphen","period","colon","emDash","enDash".
	chapSep string
	// chapNumber is the resolved chapter number string for the current
	// page, computed by the dry pass and seeded at the start of each
	// page's stamp. Empty when no chapStyle heading precedes the page.
	chapNumber string
	// numSectionPages is the page count for the section the current page
	// belongs to. Surfaced by the SECTIONPAGES field. Zero when unknown.
	numSectionPages int
	// section is the 1-based section number, surfaced by the SECTION field.
	section int
	// decimalSymbol / listSeparator come from settings.xml. Empty means
	// fall back to "." / "," respectively (US default). Used by numeric
	// pictures so European-locale templates render with the right glyphs.
	decimalSymbol string
	listSeparator string

	now          time.Time
	filename     string
	filenameFull string // full path (for FILENAME \p); empty falls back to filename
	author       string
	title        string
	subject      string
	keywords     string
	comments     string
	company      string
	username     string

	// Document-level metadata used by NUMWORDS / NUMCHARS / EDITTIME.
	numWords     int
	numChars     int
	totalMinutes int
	createDate   time.Time
	saveDate     time.Time
	printDate    time.Time

	seqCounters map[string]int
	// seqResetMarkers tracks heading-fingerprints per SEQ name × level
	// for the \s switch. Stored on vars so all flattenFields calls
	// across paragraphs share the same scoreboard.
	seqResetMarkers map[string]int
	bookmarks       map[string]string
	// bookmarkPages maps bookmark name → 1-based PDF page number where it
	// landed. Populated as the renderer walks bookmark markers; used by
	// PAGEREF for cross-references that fall after the body has been
	// laid out (i.e., during page-decoration stamping).
	bookmarkPages map[string]int
	// bookmarkParaNums maps bookmark name → its enclosing paragraph's
	// formatted list marker (e.g. "1.2.3"). Populated when bookmarks
	// land inside a numbered paragraph. Used by REF's \r / \w / \p
	// switches for paragraph-number cross-references.
	bookmarkParaNums map[string]string
	// docProperties indexes custom + standard doc properties so the
	// DOCPROPERTY field can resolve `{ DOCPROPERTY "AppVersion" }`.
	docProperties map[string]string
	// docVars indexes settings.xml/w:docVars entries.
	docVars map[string]string
	// bibliography exposes parsed b:Source entries.
	bibliography map[string]docx.BibSource
	// headings carries every Heading 1-9 / Title paragraph for TOC.
	headings []tocEntry
	// setVars carries values that SET fields have assigned.
	setVars map[string]string
	// listNumCounters tracks per-LISTNUM-list counters.
	listNumCounters map[string]int
	// tableCtx is non-nil while drawing inside a table cell. FORMULA uses
	// it to resolve =SUM(ABOVE) / explicit A1 refs.
	tableCtx *tableContext
	// styleParagraphs indexes body paragraphs by their StyleID — used by
	// the STYLEREF field to surface "the current chapter" text.
	styleParagraphs map[string][]string
	// footnoteRefs maps bookmark name → footnote ID. Used by NOTEREF.
	footnoteRefs map[string]string
	// mergeData supplies MERGEFIELD values for the implicit (single-record)
	// merge mode. When mergeRecords is non-empty, mergeData is ignored;
	// the active record drives lookup instead.
	mergeData map[string]string
	// mergeRecords is the ordered list of records for catalog-mode merge.
	// nil/empty falls back to mergeData.
	mergeRecords []map[string]string
	// mergeState is the shared cursor that NEXT/NEXTIF/SKIPIF advance.
	// Pointer-shared so the value-copied fieldVars all see the same
	// iteration position.
	mergeState *mergeIterState
	// glossary maps the docPart names from word/glossary/document.xml to
	// their plain-text payload. AUTOTEXT / GLOSSARY fields resolve their
	// first argument against this table.
	glossary map[string]string
	// tcEntries collects TC field markers — explicit TOC entries that the
	// document author placed outside heading styles.
	tcEntries []tocEntry
	// xeEntries collects XE field markers — explicit Index entries.
	xeEntries []string
	// xePages, when non-nil, accumulates page numbers per XE title
	// during the dry-run / live pass. INDEX uses these to emit a page
	// column alongside each entry. Pointer-shared across the value
	// copies of fieldVars so writes from any flattenFields call are
	// visible to the INDEX render.
	xePages *map[string][]int
	// taEntries collects TA (Table of Authorities Entry) field markers.
	// Each TA carries a long citation, an optional short form, and a
	// category id (1..16 by spec; 1=Cases by default). TOA groups them.
	taEntries []taEntry
	// altChunks maps a rels Id to the parsed Block tree of an
	// AlternativeFormatInputPart. Lets INCLUDETEXT "rId7" splice in
	// same-package HTML/RTF/Word2003-XML content that already came
	// through extras.loadAltChunks at parse time.
	altChunks map[string][]docx.Block
	// allImages and imageTargets give INCLUDEPICTURE access to the
	// host document's image registry: allImages by relationship id,
	// imageTargets by the source target (e.g. "media/image1.png").
	allImages    map[string]image.Image
	imageTargets map[string]string
	// autoNumCounter is the per-document AUTONUM* counter shared across
	// every AUTONUM, AUTONUMLGL, AUTONUMOUT instance in document order.
	// Pointer so a copy of fieldVars participates in the same counter.
	autoNumCounter *int
	// doc gives field handlers access to document-wide metadata
	// (CoreProperties, RawByteSize, etc.) for fields like REVNUM,
	// FILESIZE, USERADDRESS.
	doc *docx.Document
}

// blocksPlainText flattens a list of Blocks to a single newline-joined
// plain-text string. Used by INCLUDETEXT when an altChunk reference is
// resolved at field time — the field's text replaces the cached result.
func blocksPlainText(blocks []docx.Block) string {
	var b strings.Builder
	for i, blk := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch v := blk.(type) {
		case docx.Paragraph:
			for _, r := range v.Runs {
				b.WriteString(r.Text)
			}
		case docx.Table:
			for ri, row := range v.Rows {
				if ri > 0 {
					b.WriteByte('\n')
				}
				for ci, cell := range row.Cells {
					if ci > 0 {
						b.WriteByte('\t')
					}
					b.WriteString(blocksPlainText(cell.Blocks))
				}
			}
		}
	}
	return b.String()
}

// taEntry is one TA field marker: a citation that TOA aggregates.
type taEntry struct {
	// Long is the full citation text from `\l "…"`.
	Long string
	// Short is the abbreviated form from `\s "…"`. Empty when unset.
	Short string
	// Category is the 1-based group id from `\c N`. 1 by default.
	Category int
	// Bookmark anchors the entry's location for TOA page-number lookup.
	Bookmark string
	// PageNum is the 1-based page where the marker landed. Resolved on
	// the second pass.
	PageNum int
	// Bold / Italic mark the TA citation for bolded / italicized output in
	// the corresponding TOA entry. Sourced from \b and \i on the TA field.
	Bold, Italic bool
	// CategoryOnly is the \f switch: the citation contributes only to the
	// category total, never to the entry list. Honored by TOA's pass that
	// emits per-category header counts.
	CategoryOnly bool
}

// parseTCInstr parses a TC field instruction like
//
//	TC "My Custom Entry" \l 2 \f t
//
// returning the entry text and outline level (default 1). Returns ok=false
// when the instruction has no title.
func parseTCInstr(instrFull string) (tocEntry, bool) {
	s := strings.TrimSpace(instrFull)
	if !strings.HasPrefix(strings.ToUpper(s), "TC") {
		return tocEntry{}, false
	}
	s = strings.TrimSpace(s[2:])
	// First quoted token is the title; if unquoted, take up to the first \ switch.
	var title string
	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			title = s[1 : 1+end]
			s = s[1+end+1:]
		}
	} else {
		end := strings.Index(s, `\`)
		if end < 0 {
			title = strings.TrimSpace(s)
			s = ""
		} else {
			title = strings.TrimSpace(s[:end])
			s = s[end:]
		}
	}
	if title == "" {
		return tocEntry{}, false
	}
	level := 1
	seq := ""
	parts := strings.Fields(s)
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case `\l`:
			if n, err := strconv.Atoi(strings.Trim(parts[i+1], `"`)); err == nil && n >= 1 && n <= 9 {
				level = n
			}
		case `\f`:
			seq = strings.Trim(parts[i+1], `"`)
		}
	}
	return tocEntry{Level: level, Text: title, Seq: seq}, true
}

// prefixWithChapter prepends "chapter<sep>" to a page-number string when
// the active section requested chapter numbering via w:pgNumType@w:chapStyle.
// Returns s unchanged when no chapter context is set or no preceding
// heading at chapStyle level has been resolved for this page.
func prefixWithChapter(s string, vars fieldVars) string {
	if vars.chapStyle == 0 || vars.chapNumber == "" {
		return s
	}
	sep := "-"
	switch vars.chapSep {
	case "period":
		sep = "."
	case "colon":
		sep = ":"
	case "emDash":
		sep = "—"
	case "enDash":
		sep = "–"
	case "hyphen", "":
		sep = "-"
	}
	return vars.chapNumber + sep + s
}

// parseTAInstr extracts the long/short citation and category from a TA
// field instruction. Syntax:
//
//	TA \l "Long citation" \s "Short form" \c 2 \b \i
//
// `\l` is the long citation (also accepted as a positional arg).
// `\s` is the short form used for "subsequent" citations in TOA.
// `\c` is the category (1..16). When absent, category=1 (Cases).
// Returns ok=false when no usable citation can be extracted.
func parseTAInstr(instrFull string) (taEntry, bool) {
	s := strings.TrimSpace(instrFull)
	if !strings.HasPrefix(strings.ToUpper(s), "TA") {
		return taEntry{}, false
	}
	s = strings.TrimSpace(s[2:])
	entry := taEntry{Category: 1}
	toks := tokenizeFieldArgs(s)
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case strings.EqualFold(t, `\l`):
			if i+1 < len(toks) {
				entry.Long = strings.Trim(toks[i+1], `"`)
				i++
			}
		case strings.EqualFold(t, `\s`):
			if i+1 < len(toks) {
				entry.Short = strings.Trim(toks[i+1], `"`)
				i++
			}
		case strings.EqualFold(t, `\c`):
			if i+1 < len(toks) {
				if n, err := strconv.Atoi(strings.Trim(toks[i+1], `"`)); err == nil {
					entry.Category = n
					i++
				}
			}
		case strings.EqualFold(t, `\b`):
			entry.Bold = true
		case strings.EqualFold(t, `\i`):
			entry.Italic = true
		case strings.EqualFold(t, `\f`):
			entry.CategoryOnly = true
		case strings.EqualFold(t, `\r`):
			// Range filter — out of scope (we'd need bookmark span info).
		default:
			if !strings.HasPrefix(t, `\`) && entry.Long == "" {
				entry.Long = strings.Trim(t, `"`)
			}
		}
	}
	if entry.Long == "" {
		return taEntry{}, false
	}
	return entry, true
}

// parseTOASwitches decodes the relevant TOA field switches:
//
//	\c <category>   restrict output to one category (1..16)
//	\f              omit the category header (just the entries)
//	\h              include category header (default unless \f)
//	\l "leader"     leader between entry text and page (default dot)
//	\p              print page numbers (the default)
//	\d "sep"        page-range separator (rare; we accept but ignore)
//	\s "identifier" sequence identifier — out of scope; cached result wins
//	\e "separator"  entry/page separator (in place of leader); accept the
//	                literal as-is between text and page
type toaSwitches struct {
	Category   int
	OmitHeader bool
	Leader     string
	NoPage     bool
}

func parseTOASwitches(instrFull string) toaSwitches {
	var sw toaSwitches
	sw.Leader = "."
	toks := tokenizeFieldArgs(strings.TrimSpace(instrFull))
	if len(toks) > 0 && strings.EqualFold(toks[0], "TOA") {
		toks = toks[1:]
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case strings.EqualFold(t, `\c`):
			if i+1 < len(toks) {
				if n, err := strconv.Atoi(strings.Trim(toks[i+1], `"`)); err == nil {
					sw.Category = n
					i++
				}
			}
		case strings.EqualFold(t, `\f`):
			sw.OmitHeader = true
		case strings.EqualFold(t, `\l`), strings.EqualFold(t, `\e`):
			if i+1 < len(toks) {
				sw.Leader = strings.Trim(toks[i+1], `"`)
				i++
			}
		case strings.EqualFold(t, `\n`):
			sw.NoPage = true
		}
	}
	return sw
}

// taCategoryNames maps the spec-defined category ids to their default
// localized header strings. Word lets users customize this list per
// document; we surface the spec defaults.
var taCategoryNames = map[int]string{
	1:  "Cases",
	2:  "Statutes",
	3:  "Other Authorities",
	4:  "Rules",
	5:  "Treatises",
	6:  "Regulations",
	7:  "Constitutional Provisions",
	8:  "Category 8",
	9:  "Category 9",
	10: "Category 10",
	11: "Category 11",
	12: "Category 12",
	13: "Category 13",
	14: "Category 14",
	15: "Category 15",
	16: "Category 16",
}

// formatTOA produces a Table of Authorities listing. Entries are grouped
// by category and sorted alphabetically within each group. When a long
// citation appears multiple times the page numbers are concatenated.
func formatTOA(entries []taEntry, sw toaSwitches) string {
	if len(entries) == 0 {
		return ""
	}
	// Group + dedupe by category × long citation.
	type acc struct {
		long  string
		pages []int
	}
	groups := map[int]map[string]*acc{}
	categories := []int{}
	for _, e := range entries {
		if sw.Category > 0 && e.Category != sw.Category {
			continue
		}
		if _, ok := groups[e.Category]; !ok {
			groups[e.Category] = map[string]*acc{}
			categories = append(categories, e.Category)
		}
		a, ok := groups[e.Category][e.Long]
		if !ok {
			a = &acc{long: e.Long}
			groups[e.Category][e.Long] = a
		}
		if e.PageNum > 0 {
			a.pages = append(a.pages, e.PageNum)
		}
	}
	if len(categories) == 0 {
		return ""
	}
	// Stable sort categories ascending.
	for i := 1; i < len(categories); i++ {
		for j := i; j > 0 && categories[j] < categories[j-1]; j-- {
			categories[j], categories[j-1] = categories[j-1], categories[j]
		}
	}
	var b strings.Builder
	for ci, cat := range categories {
		if ci > 0 {
			b.WriteByte('\n')
		}
		if !sw.OmitHeader {
			name := taCategoryNames[cat]
			if name == "" {
				name = "Category " + strconv.Itoa(cat)
			}
			b.WriteString(name)
			b.WriteByte('\n')
		}
		// Sort longs alphabetically.
		longs := make([]string, 0, len(groups[cat]))
		for k := range groups[cat] {
			longs = append(longs, k)
		}
		for i := 1; i < len(longs); i++ {
			for j := i; j > 0 && longs[j] < longs[j-1]; j-- {
				longs[j], longs[j-1] = longs[j-1], longs[j]
			}
		}
		for i, long := range longs {
			a := groups[cat][long]
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(long)
			if !sw.NoPage && len(a.pages) > 0 {
				// De-dupe + sort pages for a clean comma list.
				seen := map[int]bool{}
				pages := make([]int, 0, len(a.pages))
				for _, p := range a.pages {
					if !seen[p] {
						seen[p] = true
						pages = append(pages, p)
					}
				}
				for i := 1; i < len(pages); i++ {
					for j := i; j > 0 && pages[j] < pages[j-1]; j-- {
						pages[j], pages[j-1] = pages[j-1], pages[j]
					}
				}
				if sw.Leader != "" {
					b.WriteString(" ")
					// Render leader as a short tab-like fill.
					b.WriteString(strings.Repeat(sw.Leader, 3))
					b.WriteString(" ")
				} else {
					b.WriteString("\t")
				}
				for i, p := range pages {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(strconv.Itoa(p))
				}
			}
		}
	}
	return b.String()
}

// collectTAEntries gathers TA field markers from the document body.
func collectTAEntries(doc *docx.Document) []taEntry {
	var out []taEntry
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for _, r := range v.Runs {
					if r.InstrText == "" {
						continue
					}
					if e, ok := parseTAInstr(r.InstrText); ok {
						out = append(out, e)
					}
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	if len(doc.Sections) > 0 {
		for _, sec := range doc.Sections {
			walk(sec.Blocks)
		}
	} else {
		walk(doc.Body)
	}
	return out
}

// parseXEInstr extracts the visible title from an XE field instruction.
// Subentries separated by ':' are flattened to a single "Major:Minor" string.
func parseXEInstr(instrFull string) string {
	s := strings.TrimSpace(instrFull)
	if !strings.HasPrefix(strings.ToUpper(s), "XE") {
		return ""
	}
	s = strings.TrimSpace(s[2:])
	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return s[1 : 1+end]
		}
	}
	end := strings.Index(s, `\`)
	if end < 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:end])
}

// mergeIterState is shared mutable state for catalog mail-merge: NEXT
// directives advance Idx; MERGEFIELDs look up MergeRecords[Idx]. A pointer
// to one instance lives on every fieldVars copy so the cursor survives
// the value-copy through resolution layers.
type mergeIterState struct {
	Idx int
}

// tableContext locates the cell currently being drawn so FORMULA fields
// can reach into sibling cells. Row/Col are post-gridSpan logical coords.
type tableContext struct {
	table *docx.Table
	row   int
	col   int
}

// tocEntry is one heading + outline level for TOC synthesis.
type tocEntry struct {
	Level int
	Text  string
	// Style is the paragraph style ID that produced this entry (e.g.
	// "Heading1", "ChapterTitle"). Used by the TOC `\t` switch to filter
	// entries by an author-named style list. Empty for entries coming from
	// explicit TC fields unless `\f` SEQ context attached one.
	Style string
	// Seq is the SEQ identifier carried by a `\f` TC entry. Used by the
	// TOC `\f` switch to limit entries to a single SEQ stream.
	Seq string
	// PageNum, if non-zero, is the 1-based page where this entry's anchor
	// landed. Filled by the bookmark pre-pass; consumed by formatTOC.
	PageNum int
	// Bookmark, when non-empty, is the bookmark name (e.g. "_Toc12345")
	// that pins this entry's location. Used to resolve PageNum on the
	// second pass.
	Bookmark string
}

// tocSwitches captures the parsed switches from a TOC field instruction.
// All fields are optional — zero value means "default behavior" matching
// Word's `{ TOC }` with no switches.
type tocSwitches struct {
	// MinLvl/MaxLvl from `\o "1-3"`. 0 = use default (1..9).
	MinLvl, MaxLvl int
	// StyleMap from `\t "Heading 1,1,Heading 2,2"` — case-insensitive
	// style ID → outline level. Empty when `\t` not used.
	StyleMap map[string]int
	// UseStyleMap is true when `\t` was specified (even if empty).
	UseStyleMap bool
	// UseOutline is true when `\u` was specified — collect by w:outlineLvl
	// instead of by style.
	UseOutline bool
	// HidePageNums true when `\n` was specified with no level range, or
	// HideLvls populated when `\n 2-3` etc.
	HidePageNums bool
	HideMinLvl   int
	HideMaxLvl   int
	// Hyperlinks true when `\h` is present — emit clickable links.
	Hyperlinks bool
	// HideInWeb true when `\z`.
	HideInWeb bool
	// SeqName from `\f SEQID` — limit TC entries to this SEQ stream.
	SeqName string
	// Separator from `\d "char"` — between entry text and page number.
	// Empty falls back to dot leader.
	Separator string
	// Bookmark from `\b name` — restrict scope to a bookmark range.
	Bookmark string
	// Caption from `\c "label"` — generate a "table of figures" style TOC.
	Caption string
	// TabLeader from `\p "char"` — Word's `\p` overrides the default
	// dot leader. Same meaning as the per-style TOCStyle leader.
	TabLeader string
}

// parseTOCSwitches scans a TOC field instruction and returns the
// recognized switches. Unknown switches are ignored (Word's behavior).
// Examples:
//
//	{ TOC \o "1-3" \h \z \u }
//	{ TOC \t "MyHead,1,MyHead2,2" \n \p "—" }
func parseTOCSwitches(instr string) tocSwitches {
	var sw tocSwitches
	s := strings.TrimSpace(instr)
	// Drop leading "TOC" keyword.
	if up := strings.ToUpper(s); strings.HasPrefix(up, "TOC") {
		s = strings.TrimSpace(s[3:])
	}
	// Walk the instruction one switch at a time. A switch is "\X" optionally
	// followed by an argument (quoted or whitespace-delimited).
	for len(s) > 0 {
		i := strings.Index(s, `\`)
		if i < 0 {
			break
		}
		// Step past the backslash.
		s = s[i+1:]
		if len(s) == 0 {
			break
		}
		flag := s[0]
		s = strings.TrimLeft(s[1:], " \t")
		// Pull the optional argument: quoted "...", or up to whitespace,
		// or up to the next backslash.
		var arg string
		switch {
		case strings.HasPrefix(s, `"`):
			if end := strings.Index(s[1:], `"`); end >= 0 {
				arg = s[1 : 1+end]
				s = s[2+end:]
			} else {
				arg = s[1:]
				s = ""
			}
		case len(s) > 0 && s[0] != '\\':
			end := len(s)
			for j, r := range s {
				if r == ' ' || r == '\t' || r == '\\' {
					end = j
					break
				}
			}
			arg = s[:end]
			s = s[end:]
		}
		s = strings.TrimLeft(s, " \t")
		switch flag {
		case 'o', 'O':
			lo, hi := parseLvlRange(arg, 1, 9)
			sw.MinLvl, sw.MaxLvl = lo, hi
		case 't', 'T':
			sw.StyleMap = parseTOCStyleList(arg)
			sw.UseStyleMap = true
		case 'u', 'U':
			sw.UseOutline = true
		case 'n', 'N':
			if arg == "" {
				sw.HidePageNums = true
			} else {
				sw.HideMinLvl, sw.HideMaxLvl = parseLvlRange(arg, 1, 9)
				if sw.HideMaxLvl == 0 {
					sw.HidePageNums = true
				}
			}
		case 'h', 'H':
			sw.Hyperlinks = true
		case 'z', 'Z':
			sw.HideInWeb = true
		case 'f', 'F':
			sw.SeqName = strings.TrimSpace(arg)
		case 'd', 'D':
			sw.Separator = arg
		case 'b', 'B':
			sw.Bookmark = strings.TrimSpace(arg)
		case 'c', 'C':
			sw.Caption = strings.TrimSpace(arg)
		case 'p', 'P':
			sw.TabLeader = arg
		}
	}
	return sw
}

// parseLvlRange handles "1-3", "3", "1- 9", or "". Returns (0,0) when
// the input cannot be parsed.
func parseLvlRange(arg string, lo, hi int) (int, int) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, 0
	}
	if dash := strings.Index(arg, "-"); dash >= 0 {
		left := strings.TrimSpace(arg[:dash])
		right := strings.TrimSpace(arg[dash+1:])
		l, err1 := strconv.Atoi(left)
		r, err2 := strconv.Atoi(right)
		if err1 != nil {
			l = lo
		}
		if err2 != nil {
			r = hi
		}
		if l < 1 || r < l {
			return 0, 0
		}
		return l, r
	}
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 && n <= 9 {
		return n, n
	}
	return 0, 0
}

// parseTOCStyleList parses "Heading 1,1,Heading 2,2,App,5" into a map
// of lower-cased style → level. Order is insertion-tolerant — Word also
// accepts no level (defaulting to 1) and just a bare list of names.
func parseTOCStyleList(arg string) map[string]int {
	out := map[string]int{}
	if arg == "" {
		return out
	}
	parts := strings.Split(arg, ",")
	for i := 0; i < len(parts); i++ {
		name := strings.TrimSpace(parts[i])
		if name == "" {
			continue
		}
		level := 1
		if i+1 < len(parts) {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[i+1])); err == nil && n >= 1 && n <= 9 {
				level = n
				i++ // consume the level token
			}
		}
		key := strings.ToLower(strings.ReplaceAll(name, " ", ""))
		out[key] = level
	}
	return out
}

// needsForwardPageRefPass reports whether the document contains any PAGEREF
// (or TOC) field whose body emits page numbers that depend on layout. When
// true, RenderWriter does an initial dry pass to populate the
// bookmark→page map so the real pass can substitute resolved values.
func needsForwardPageRefPass(doc *docx.Document) bool {
	if doc == nil {
		return false
	}
	var scan func(blocks []docx.Block) bool
	scan = func(blocks []docx.Block) bool {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for _, r := range v.Runs {
					if r.InstrText == "" {
						continue
					}
					instr := strings.ToUpper(r.InstrText)
					// PAGEREF: explicit forward ref. TOC: synthesizes a
					// PAGEREF list internally; same need.
					if strings.Contains(instr, "PAGEREF") || strings.Contains(instr, " TOC ") || strings.HasPrefix(strings.TrimSpace(instr), "TOC") {
						return true
					}
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						if scan(cell.Blocks) {
							return true
						}
					}
				}
			}
		}
		return false
	}
	for _, sec := range doc.Sections {
		if scan(sec.Blocks) {
			return true
		}
		if scan(sec.HeaderBlocks) || scan(sec.FooterBlocks) ||
			scan(sec.HeaderFirstBlocks) || scan(sec.FooterFirstBlocks) ||
			scan(sec.HeaderEvenBlocks) || scan(sec.FooterEvenBlocks) {
			return true
		}
	}
	if scan(doc.Body) {
		return true
	}
	if scan(doc.HeaderBlocks) || scan(doc.FooterBlocks) {
		return true
	}
	return false
}

// fieldCodeAndArgs splits an instrText like ` SEQ Figure \* ARABIC ` into the
// code ("SEQ") and the first non-switch argument.
func fieldCodeAndArgs(s string) (code, primary string) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", ""
	}
	code = strings.ToUpper(parts[0])
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "\\") {
			continue
		}
		p = strings.Trim(p, `"`)
		if p != "" {
			primary = p
			break
		}
	}
	return code, primary
}

// hyperlinkFieldInstr decodes a HYPERLINK instrText into (target, isAnchor,
// tooltip). `\l` means the primary arg is an internal bookmark name; `\o`
// carries a screentip (PDF link annotation /Contents). `\m` (image-map
// coords) and `\n` (new-window flag) have no PDF surface so we drop them.
// `\t` carries a target-frame name — also dropped.
func hyperlinkFieldInstr(s string) (target string, isAnchor bool, tooltip string) {
	parts := splitFieldArgs(strings.TrimSpace(s))
	if len(parts) == 0 {
		return "", false, ""
	}
	// parts[0] is "HYPERLINK"; arguments follow. Walk and consume.
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		switch p {
		case "\\l":
			isAnchor = true
		case "\\o":
			if i+1 < len(parts) {
				tooltip = strings.Trim(parts[i+1], `"`)
				i++
			}
		case "\\t", "\\m", "\\n":
			// Skip the switch and its argument.
			if i+1 < len(parts) {
				i++
			}
		default:
			if strings.HasPrefix(p, "\\") {
				continue
			}
			if target == "" {
				target = strings.Trim(p, `"`)
			}
		}
	}
	return target, isAnchor, tooltip
}

// splitFieldArgs is a quote-aware tokenizer for Word field instrText.
// It preserves double-quoted spans (which may contain spaces) as a single
// token including the quotes; backslash-prefixed switches stay as their own
// token. Single-quotes are left as-is because Word numeric/date pictures
// use them to wrap literals.
func splitFieldArgs(s string) []string {
	var out []string
	var buf strings.Builder
	inQuote := false
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			buf.WriteByte(c)
			inQuote = !inQuote
			continue
		}
		if !inQuote && (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
			flush()
			continue
		}
		buf.WriteByte(c)
	}
	flush()
	return out
}

// flattenFields walks a paragraph's raw Run stream and resolves
// w:fldChar / w:instrText structure into plain text runs.
func flattenFields(runs []docx.Run, vars fieldVars) []docx.Run {
	type frame struct {
		instr       strings.Builder
		inResult    bool
		code        string
		arg         string
		instrFull   string
		substituted bool
		linkURL     string
		linkAnchor  string
		linkTooltip string
		suppress    bool
		// lockResult is set when the field instruction carries `\!` —
		// "lock result". The cached glyphs pass through verbatim; we
		// skip recomputation. Word uses this for content that should
		// never auto-update (frozen page numbers in printable forms,
		// stamped dates).
		lockResult bool
		formField  *docx.FormFieldInfo
	}
	var stack []*frame
	top := func() *frame {
		if len(stack) == 0 {
			return nil
		}
		return stack[len(stack)-1]
	}

	out := make([]docx.Run, 0, len(runs))
	for _, r := range runs {
		switch {
		case r.FieldBegin:
			stack = append(stack, &frame{formField: r.FormField})
		case r.FieldSep:
			if f := top(); f != nil {
				f.instrFull = f.instr.String()
				f.code, f.arg = fieldCodeAndArgs(f.instrFull)
				f.inResult = true
				// \! locks the cached result — skip field recomputation
				// and let the result region's runs flow through verbatim.
				if hasFlagSwitch(f.instrFull, "!") {
					f.lockResult = true
				}
				switch f.code {
				case "HYPERLINK":
					target, isAnchor, tip := hyperlinkFieldInstr(f.instrFull)
					if isAnchor {
						f.linkAnchor = target
					} else {
						f.linkURL = target
					}
					if tip != "" {
						f.linkTooltip = tip
					}
				case "REF", "PAGEREF", "NOTEREF":
					// \h turns the cross-reference into an internal link
					// that jumps to the named bookmark. Word's other ref
					// switches (\n paragraph number, \w full paragraph
					// number, \r relative number) reach into the numbering
					// engine and are out of scope; we surface the cached
					// result for those.
					if f.arg != "" && hasFlagSwitch(f.instrFull, "h") {
						f.linkAnchor = f.arg
					}
				case "SET":
					name, value := setFieldInstr(f.instrFull)
					if name != "" {
						if vars.setVars == nil {
							vars.setVars = map[string]string{}
						}
						vars.setVars[name] = value
					}
					f.suppress = true
				case "ADVANCE":
					f.suppress = true
				case "TC", "XE", "TA", "RD", "PRIVATE":
					// TC: TOC entry marker. XE: Index entry marker.
					// TA: Table of Authorities entry marker.
					// RD: Reference document. PRIVATE: app-specific data.
					// All have no visible result; recorded so the caller can
					// later mine them. We harvest below in vars.
					if vars.tcEntries == nil {
						vars.tcEntries = []tocEntry{}
					}
					if f.code == "TC" {
						if entry, ok := parseTCInstr(f.instrFull); ok {
							vars.tcEntries = append(vars.tcEntries, entry)
						}
					} else if f.code == "XE" {
						if title := parseXEInstr(f.instrFull); title != "" {
							vars.xeEntries = append(vars.xeEntries, title)
							// Capture page on every encounter so the
							// INDEX renderer can emit "term … p, q".
							if vars.xePages != nil && vars.page > 0 {
								m := *vars.xePages
								m[title] = append(m[title], vars.page)
							}
						}
					} else if f.code == "TA" {
						if entry, ok := parseTAInstr(f.instrFull); ok {
							vars.taEntries = append(vars.taEntries, entry)
						}
					}
					f.suppress = true
				case "FORMTEXT", "FORMCHECKBOX", "FORMDROPDOWN":
					// Form fields: synthesize visible output from the
					// parsed ffData, replacing whatever Word cached.
					if v, ok := formFieldOutput(f.formField, f.code); ok {
						f.substituted = false
						// Mark suppress=true so cached glyphs are dropped;
						// we then emit a single synthetic run after with a
						// visible control border so the placeholder reads
						// as an interactive form widget rather than text.
						f.suppress = true
						props := r.Props
						props.TextBorder = decorationBorderForField(f.code)
						out = append(out, docx.Run{Text: v, Props: props})
					}
				}
			}
		case r.FieldEnd:
			if n := len(stack); n > 0 {
				f := stack[n-1]
				// Form fields with no SEPARATE phase: synthesize here.
				if f.formField != nil && f.code == "" {
					_, fallbackCode := formFieldKindCode(f.formField)
					if v, ok := formFieldOutput(f.formField, fallbackCode); ok {
						props := r.Props
						props.TextBorder = decorationBorderForField(fallbackCode)
						out = append(out, docx.Run{Text: v, Props: props})
					}
				}
				stack = stack[:n-1]
			}
		case r.InstrText != "":
			if f := top(); f != nil && !f.inResult {
				f.instr.WriteString(r.InstrText)
			}
		default:
			f := top()
			if f == nil {
				out = append(out, r)
				continue
			}
			if !f.inResult {
				continue
			}
			if f.suppress {
				continue
			}
			if f.lockResult {
				// \! locks the cached result — emit the run unchanged.
				out = append(out, r)
				continue
			}
			if value, ok := lookupFieldValueFull(f.code, f.arg, f.instrFull, vars); ok {
				if !f.substituted {
					// Apply \* general-format switches (Upper/Lower/
					// roman/Hex/Ordinal/...) and SYMBOL \f font.
					value = applyGeneralFormatSwitch(value, f.instrFull)
					props := r.Props
					if f.code == "SYMBOL" {
						if fontName := symbolFontSwitch(f.instrFull); fontName != "" {
							props.FontFamily = fontName
						}
						if sz := symbolFontSizeSwitch(f.instrFull); sz > 0 {
							props.FontSize = sz
						}
					}
					if strings.Contains(value, "\n") {
						lines := strings.Split(value, "\n")
						for i, line := range lines {
							if i > 0 {
								out = append(out, docx.Run{IsBreak: true, Props: props})
							}
							rr := r
							rr.Text = line
							rr.Props = props
							out = append(out, rr)
						}
					} else {
						rr := r
						rr.Text = value
						rr.Props = props
						out = append(out, rr)
					}
					f.substituted = true
				}
				continue
			}
			if f.linkURL != "" || f.linkAnchor != "" {
				rr := r
				if f.linkURL != "" {
					rr.LinkURL = f.linkURL
				}
				if f.linkAnchor != "" {
					rr.LinkAnchor = f.linkAnchor
				}
				if f.linkTooltip != "" {
					rr.LinkTooltip = f.linkTooltip
				}
				out = append(out, rr)
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// lookupFieldValueWith is the legacy entry point that drops the full
// instrText. Kept for tests that don't need switches.
func lookupFieldValueWith(code, arg string, vars fieldVars) (string, bool) {
	return lookupFieldValueFull(code, arg, "", vars)
}

// lookupFieldValueFull resolves one field reference to its rendered value.
// instrFull is the entire instrText (e.g. "SYMBOL 61472 \\f Wingdings"); a
// few field codes (SYMBOL, FORMULA, REF) need switches beyond the primary
// arg. Returning (_, false) lets the caller fall back to the cached Word
// result.
func lookupFieldValueFull(code, arg, instrFull string, vars fieldVars) (string, bool) {
	switch code {
	case "PAGE":
		if vars.page > 0 {
			s := formatPageNumber(vars.page, vars.pageFmt)
			// `\# "0000"` (numeric picture switch) applies to PAGE/NUMPAGES
			// too — Word lets users zero-pad page numbers in TOC contexts.
			// Honors the doc's decimal/grouping symbols.
			if v := numericSwitchLocale(float64(vars.page), instrFull, vars); v != "" {
				return prefixWithChapter(v, vars), true
			}
			return prefixWithChapter(s, vars), true
		}
	case "NUMPAGES":
		if vars.numPages > 0 {
			s := formatPageNumber(vars.numPages, vars.pageFmt)
			if v := numericSwitchLocale(float64(vars.numPages), instrFull, vars); v != "" {
				return v, true
			}
			return s, true
		}
	case "SECTIONPAGES":
		// Total page count of the CURRENT section. Header/footer rendering
		// populates numSectionPages per page; outside that context the
		// field can't be resolved, so fall through to the cached result.
		if vars.numSectionPages > 0 {
			s := formatPageNumber(vars.numSectionPages, vars.pageFmt)
			if v := numericSwitchLocale(float64(vars.numSectionPages), instrFull, vars); v != "" {
				return v, true
			}
			return s, true
		}
		return "", false
	case "SECTION":
		// 1-based section number — typically rendered in a footer like
		// "Section 2 of 5". Falls through to cached result when unknown.
		if vars.section > 0 {
			return strconv.Itoa(vars.section), true
		}
		return "", false
	case "DATE":
		if !vars.now.IsZero() {
			return formatFieldDateTime(vars.now, instrFull, "2006-01-02"), true
		}
	case "TIME":
		if !vars.now.IsZero() {
			return formatFieldDateTime(vars.now, instrFull, "15:04"), true
		}
	case "CREATEDATE":
		when := vars.createDate
		if when.IsZero() {
			when = vars.now
		}
		if !when.IsZero() {
			return formatFieldDateTime(when, instrFull, "2006-01-02"), true
		}
	case "SAVEDATE":
		when := vars.saveDate
		if when.IsZero() {
			when = vars.now
		}
		if !when.IsZero() {
			return formatFieldDateTime(when, instrFull, "2006-01-02"), true
		}
	case "PRINTDATE":
		when := vars.printDate
		if when.IsZero() {
			when = vars.now
		}
		if !when.IsZero() {
			return formatFieldDateTime(when, instrFull, "2006-01-02"), true
		}
	case "EDITTIME":
		// w:TotalTime in minutes — surfaced from app.xml. Honor a
		// \# format switch when present (e.g. "h:mm").
		if vars.totalMinutes > 0 {
			h := vars.totalMinutes / 60
			m := vars.totalMinutes % 60
			if strings.Contains(instrFull, "\\#") {
				if v := formatNumericSwitch(float64(vars.totalMinutes), instrFull); v != "" {
					return v, true
				}
			}
			if h > 0 {
				return strconv.Itoa(h) + "h " + strconv.Itoa(m) + "m", true
			}
			return strconv.Itoa(vars.totalMinutes) + "m", true
		}
		return "", false
	case "NUMWORDS":
		if vars.numWords > 0 {
			return formatNumericValue(float64(vars.numWords), instrFull), true
		}
		return "", false
	case "NUMCHARS":
		if vars.numChars > 0 {
			return formatNumericValue(float64(vars.numChars), instrFull), true
		}
		return "", false
	case "FILENAME":
		// FILENAME \p — full path. We carry the full SourceFilename on
		// vars.filenameFull (callers set it; falls back to the basename
		// when unset). Without \p Word returns the file name without
		// the path, matching the legacy default.
		if hasFlagSwitch(instrFull, "p") {
			if vars.filenameFull != "" {
				return vars.filenameFull, true
			}
		}
		if vars.filename != "" {
			return vars.filename, true
		}
	case "USERNAME":
		if vars.username != "" {
			return vars.username, true
		}
		// Fall through to the author when USERNAME is unset — close enough
		// for most templates.
		if vars.author != "" {
			return vars.author, true
		}
	case "USERINITIALS":
		// Approximate initials from the username/author.
		if name := firstNonEmpty(vars.username, vars.author); name != "" {
			return initialsOf(name), true
		}
	case "AUTHOR":
		if vars.author != "" {
			return vars.author, true
		}
	case "LASTSAVEDBY":
		if vars.author != "" {
			return vars.author, true
		}
	case "SEQ":
		if arg != "" && vars.seqCounters != nil {
			// Switches:
			//   \r N   — reset counter to N (then return N)
			//   \s N   — reset when the most recent heading at level N
			//            advanced since the previous SEQ at this name
			//   \c     — repeat last value, do not increment
			//   \h     — increment but emit no visible text
			//   \n     — explicit "next" (default)
			//   \* fmt — formatted via applyGeneralFormatSwitch later
			if n, ok := seqResetSwitch(instrFull); ok {
				vars.seqCounters[arg] = n
				return strconv.Itoa(n), true
			}
			if lvl, ok := seqHeadingResetSwitch(instrFull); ok && vars.bookmarkParaNums != nil {
				// \s N — when a heading at level N has been seen since
				// the last SEQ-this-name, reset to 1. We approximate
				// "seen since last" by tracking a per-name heading
				// fingerprint (count of headings at that level so far).
				key := arg + "\x00seq_s_" + strconv.Itoa(lvl)
				h := vars.headingCountByLevel(lvl)
				if vars.seqResetMarkers == nil {
					vars.seqResetMarkers = map[string]int{}
				}
				if h != vars.seqResetMarkers[key] {
					vars.seqResetMarkers[key] = h
					vars.seqCounters[arg] = 1
					return strconv.Itoa(1), true
				}
			}
			if seqHasFlag(instrFull, "c") {
				v := vars.seqCounters[arg]
				if v == 0 {
					v = 1
				}
				return strconv.Itoa(v), true
			}
			vars.seqCounters[arg]++
			if seqHasFlag(instrFull, "h") {
				return "", true
			}
			return strconv.Itoa(vars.seqCounters[arg]), true
		}
	case "REF":
		// REF consults SET-assigned variables first, then bookmarks.
		if arg != "" {
			// Paragraph-number switches reach into the numbering engine.
			//   \r = relative paragraph number ("1.2.3", full path)
			//   \w = paragraph number with full context ("1.2.3", same path)
			//   \n = paragraph number with NO context (last segment only,
			//        e.g. "3" for "1.2.3" — the minimum unique component)
			//   \p = relative position word: "above" or "below" depending on
			//        whether the bookmark precedes or follows the field site.
			//        We approximate via bookmark page vs. current page; when
			//        unknown we default to "above" since most cross-references
			//        point backward to earlier numbered content.
			if hasFlagSwitch(instrFull, "p") {
				return refPositionWord(arg, vars), true
			}
			if hasFlagSwitch(instrFull, "r") || hasFlagSwitch(instrFull, "w") || hasFlagSwitch(instrFull, "n") {
				if vars.bookmarkParaNums != nil {
					if n, ok := vars.bookmarkParaNums[arg]; ok && n != "" {
						if hasFlagSwitch(instrFull, "n") {
							// "no context" = last segment only
							if idx := strings.LastIndex(n, "."); idx >= 0 {
								return n[idx+1:], true
							}
							return n, true
						}
						return n, true
					}
				}
			}
			if vars.setVars != nil {
				if v, ok := vars.setVars[arg]; ok && v != "" {
					return v, true
				}
			}
			if vars.bookmarks != nil {
				if text, ok := vars.bookmarks[arg]; ok && text != "" {
					return text, true
				}
			}
		}
	case "PAGEREF":
		// PAGEREF resolves to the page number of a bookmark. Prefer the
		// bookmarkPages index (populated as the body is laid out); the
		// `\h` switch makes it a hyperlink — the linking is handled by
		// the surrounding HYPERLINK or by a separate annotation, so we
		// just emit the number here.
		if arg != "" {
			// \p switch on PAGEREF means "above" / "below" relative
			// position; we don't track relative position so fall through
			// to absolute page number for sanity.
			if vars.bookmarkPages != nil {
				if pg, ok := vars.bookmarkPages[arg]; ok && pg > 0 {
					return strconv.Itoa(pg), true
				}
			}
			if vars.bookmarks != nil {
				if text, ok := vars.bookmarks[arg]; ok && text != "" {
					return text, true
				}
			}
		}
		return "", false
	case "NOTEREF":
		// NOTEREF resolves to a footnote/endnote reference number. We
		// surface the bookmark text when possible (the bookmark's
		// content typically IS the note ID), then fall back to the
		// cached result.
		if arg != "" {
			if vars.footnoteRefs != nil {
				if id, ok := vars.footnoteRefs[arg]; ok && id != "" {
					return id, true
				}
			}
			if vars.bookmarks != nil {
				if text, ok := vars.bookmarks[arg]; ok && text != "" {
					return text, true
				}
			}
		}
		return "", false
	case "STYLEREF":
		// STYLEREF prints the most-recent text styled with the named
		// style. The ideal implementation needs per-page state we don't
		// track; instead we return the FIRST paragraph that uses the
		// named style — the typical "current chapter" answer for headers
		// on a single-chapter section. Modifier switches honored:
		//   \l  Lowest — return the last matching paragraph (typical
		//        "current section" use case from chapter footers).
		//   \f  Footer/from-end — alias for \l.
		//   \t  Trim — strip leading numbering prefix ("1.2 ") from the
		//        result so STYLEREF "Heading 1" \t prints just the title.
		//   \n  Suppress non-delimiter text — keep only digits/punct.
		//   \w  Walk — include parent levels so "1.2" inherits chapter "1".
		//   \r  Relative — first text after this position; we fall back to
		//        \l (last) since we can't truly walk in reverse.
		//   \s  STYLEREF \s "headingStyle" — only paragraphs after the
		//        current page with the named style; renderer-state-less,
		//        falls back to first match.
		if arg != "" && vars.styleParagraphs != nil {
			if texts, ok := vars.styleParagraphs[arg]; ok && len(texts) > 0 {
				val := texts[0]
				if styleRefSwitch(instrFull, 'l') || styleRefSwitch(instrFull, 'f') ||
					styleRefSwitch(instrFull, 'r') {
					val = texts[len(texts)-1]
				}
				if styleRefSwitch(instrFull, 't') {
					val = trimStyleRefPrefix(val)
				}
				if styleRefSwitch(instrFull, 'n') {
					val = onlyDigitsAndPunct(val)
				}
				return val, true
			}
		}
		return "", false
	case "TITLE":
		if vars.title != "" {
			return vars.title, true
		}
	case "SUBJECT":
		if vars.subject != "" {
			return vars.subject, true
		}
	case "KEYWORDS":
		if vars.keywords != "" {
			return vars.keywords, true
		}
	case "COMMENTS":
		if vars.comments != "" {
			return vars.comments, true
		}
	case "COMPANY":
		if vars.company != "" {
			return vars.company, true
		}
	case "DOCPROPERTY":
		if arg != "" && vars.docProperties != nil {
			if v, ok := vars.docProperties[arg]; ok && v != "" {
				return applyValueFormatters(v, instrFull, vars), true
			}
		}
		return "", false
	case "DOCVARIABLE":
		if arg != "" && vars.docVars != nil {
			if v, ok := vars.docVars[arg]; ok && v != "" {
				return applyValueFormatters(v, instrFull, vars), true
			}
		}
		return "", false
	case "CITATION":
		if arg != "" && vars.bibliography != nil {
			if src, ok := vars.bibliography[arg]; ok {
				return formatCitation(src), true
			}
		}
		return "", false
	case "BIBLIOGRAPHY":
		if vars.bibliography != nil && len(vars.bibliography) > 0 {
			return formatBibliography(vars.bibliography), true
		}
		return "", false
	case "MERGEFIELD":
		// MERGEFIELD names a mail-merge column. When MergeRecords is set
		// (catalog mode), the active record — advanced by NEXT/NEXTIF/SKIPIF
		// — drives lookup; otherwise the implicit single-record MergeData
		// map applies. With nothing resolvable we fall through to Word's
		// cached result so already-merged templates render unchanged.
		if arg == "" {
			return "", false
		}
		active := activeMergeRecord(vars)
		if active == nil {
			return "", false
		}
		v, ok := mergeDataLookup(active, arg)
		if !ok {
			return "", false
		}
		pre, post := mergeFieldAffixes(instrFull)
		return pre + applyValueFormatters(v, instrFull, vars) + post, true
	case "FORMTEXT":
		// FORMTEXT shows the result region's content as-is — return ""
		// + false so the result region's text streams through normally.
		return "", false
	case "FORMCHECKBOX":
		// Checkbox: cached result is empty (Word draws the box from a
		// separate FFData blob). Surface ☐ as a visible placeholder.
		return "☐", true
	case "FORMDROPDOWN":
		// Dropdown: same situation as FORMCHECKBOX — surface ▾ as the
		// "selected value" placeholder when no result was cached.
		return "▾", true
	case "QUOTE":
		// QUOTE simply emits its argument as text.
		if arg != "" {
			return strings.Trim(arg, `"`), true
		}
	case "IF":
		// IF is a conditional expression of the shape
		//   IF <expr1> <op> <expr2> "trueText" "falseText"
		// where op is = / <> / < / > / <= / >=. Word also allows the wildcard
		// pattern "* / ?". We evaluate the comparison and return the chosen
		// branch text; if the instruction can't be parsed we fall back to the
		// cached result.
		if v, ok := evaluateIfField(instrFull); ok {
			return v, true
		}
		return "", false
	case "COMPARE":
		// COMPARE is IF without the branch texts: it just returns "1"
		// when the comparison is true, "0" otherwise. We rewrite the
		// instruction to an IF expression and reuse evaluateIfField.
		// Falls through to cached on parse failure.
		trimmed := strings.TrimSpace(instrFull)
		if !strings.HasPrefix(strings.ToUpper(trimmed), "COMPARE") {
			return "", false
		}
		rest := strings.TrimSpace(trimmed[len("COMPARE"):])
		rewritten := "IF " + rest + ` "1" "0"`
		if v, ok := evaluateIfField(rewritten); ok {
			return v, true
		}
		return "", false
	case "MERGEREC":
		// Current merge record number. With no merge data we always return
		// "1" (the implicit record); honest given we never iterate.
		return "1", true
	case "MERGESEQ":
		// Same shape as MERGEREC — sequence number across the run.
		return "1", true
	case "NEXT":
		// Unconditional: advance to the next merge record.
		advanceMergeCursor(vars)
		return "", true
	case "NEXTIF":
		// Evaluate the IF-style condition embedded in the instruction. The
		// instr looks like ` NEXTIF <expr1> <op> <expr2> ` — we reuse the
		// IF evaluator by rewriting to `IF <…> "1" "0"` and advancing on
		// the truthy branch.
		if evaluateMergeCondition(instrFull, "NEXTIF") {
			advanceMergeCursor(vars)
		}
		return "", true
	case "SKIPIF":
		// SKIPIF: when the condition is true, drop the CURRENT record and
		// move to the next. We advance the cursor and rewind output for the
		// current record by emitting nothing here — fields already resolved
		// upstream keep their values, but later MERGEFIELDs in this record
		// read from the advanced cursor too. Honest within our scope:
		// Word's record-rewind behavior isn't reproducible without a full
		// per-record re-layout pass.
		if evaluateMergeCondition(instrFull, "SKIPIF") {
			advanceMergeCursor(vars)
		}
		return "", true
	case "ASK", "FILLIN":
		// Interactive prompts: the cached result region carries whatever
		// the user typed last time, so fall through to it.
		return "", false
	case "DISPLAYBARCODE", "MERGEBARCODE":
		// Render barcode fields as a labeled placeholder. Real
		// scannable encoders (Code 128 / QR / EAN) would need bitmap
		// generation outside this package's current scope; the
		// placeholder at least preserves the encoded payload so a
		// human reader sees the intent.
		toks := tokenizeFieldArgs(arg)
		if len(toks) == 0 {
			return "[barcode]", true
		}
		data := strings.Trim(toks[0], `"`)
		kind := "barcode"
		// Second positional is the symbology (CODE128, QR, EAN13, etc.).
		if len(toks) >= 2 {
			t := strings.ToUpper(strings.Trim(toks[1], `"`))
			if t != "" {
				kind = t
			}
		}
		return "[" + kind + ": " + data + "]", true
	case "DATABASE":
		// DATABASE inlines an external query result. We never run the
		// query; cached result is the best surface.
		return "", false
	case "PRINT":
		// PRINT is a raw printer command; suppress.
		return "", true
	case "INCLUDETEXT":
		// INCLUDETEXT references an external file or rel target. We can't
		// safely read arbitrary host-filesystem paths from PDF rendering,
		// but we DO honor three local forms:
		//   1. INCLUDETEXT <path> <bookmark>  → vars.bookmarks lookup
		//   2. INCLUDETEXT "rId12"             → AltChunk by relationship id
		//   3. INCLUDETEXT "section://N"       → first paragraph of section N
		// Falls back to the cached result otherwise.
		toks := tokenizeFieldArgs(arg)
		if len(toks) >= 2 {
			if v, ok := vars.bookmarks[toks[1]]; ok {
				return v, true
			}
		}
		if len(toks) >= 1 {
			path := strings.Trim(toks[0], `"`)
			// Same-package altChunk reference by rId.
			if strings.HasPrefix(path, "rId") && vars.altChunks != nil {
				if blocks, ok := vars.altChunks[path]; ok {
					return blocksPlainText(blocks), true
				}
			}
		}
		return "", false
	case "INCLUDEPICTURE":
		// INCLUDEPICTURE forms we recognize:
		//   1. INCLUDEPICTURE "rIdN"          — same-package image rel
		//   2. INCLUDEPICTURE "media/foo.png" — package-relative path
		// In both cases the result we synthesize is a marker token
		// ("[Image rIdN]") that the renderer's run-decorator path turns
		// into an actual image atom by attaching the relationship via
		// the field cache. When the path doesn't resolve we leave the
		// cached result alone (the result region typically contains a
		// w:drawing already, so the image is shown anyway).
		toks := tokenizeFieldArgs(arg)
		if len(toks) == 0 {
			return "", false
		}
		path := strings.Trim(toks[0], `"`)
		if strings.HasPrefix(path, "rId") {
			if vars.allImages != nil {
				if _, ok := vars.allImages[path]; ok {
					return "[image:" + path + "]", true
				}
			}
		}
		// Path-relative form: scan known media file names to see if any
		// rel target ends with the same basename. Picks the first
		// match — Word renames freshly-embedded INCLUDEPICTURE files
		// to media/imageN.* anyway, so this matches typical exports.
		base := path
		if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
			base = base[i+1:]
		}
		if base != "" {
			for rid, target := range vars.imageTargets {
				if strings.EqualFold(target, base) || strings.HasSuffix(strings.ToLower(target), "/"+strings.ToLower(base)) {
					return "[image:" + rid + "]", true
				}
			}
		}
		return "", false
	case "TOC":
		sw := parseTOCSwitches(instrFull)
		entries := filterTOCEntries(vars.headings, vars.tcEntries, sw)
		// Resolve page numbers from the bookmark→page map populated by
		// the dry layout pass. Filled in-place onto the entry copies so
		// formatTOCWithSwitches sees them.
		for i := range entries {
			if entries[i].PageNum == 0 && entries[i].Bookmark != "" && vars.bookmarkPages != nil {
				if pg, ok := vars.bookmarkPages[entries[i].Bookmark]; ok {
					entries[i].PageNum = pg
				}
			}
		}
		if len(entries) > 0 {
			return formatTOCWithSwitches(entries, sw), true
		}
		return "", false
	case "INDEX":
		// INDEX synthesizes an index from XE entries. We emit a simple
		// alphabetical list when XE markers were found.
		if len(vars.xeEntries) > 0 {
			return formatIndexWithPages(vars.xeEntries, vars.xePages), true
		}
		return "", false
	case "TOA":
		if len(vars.taEntries) > 0 {
			return formatTOA(vars.taEntries, parseTOASwitches(instrFull)), true
		}
		return "", false
	case "AUTOTEXT", "GLOSSARY":
		// Both fields look up a docPart by name. AUTOTEXT and GLOSSARY
		// take the docPart name as the first arg; we hand back the
		// parsed plain-text body. Fall through to the cached result
		// when the name isn't in the glossary or the package shipped
		// without one.
		name := strings.TrimSpace(arg)
		name = strings.Trim(name, "\"")
		if name == "" || vars.glossary == nil {
			return "", false
		}
		if v, ok := vars.glossary[name]; ok {
			return v, true
		}
		return "", false
	case "MACROBUTTON":
		// Syntax: MACROBUTTON MacroName Display Text
		// We can't run the macro, but the display text after the macro
		// name is what Word actually paints. Strip the macro name token.
		toks := tokenizeFieldArgs(arg)
		if len(toks) >= 2 {
			return strings.Join(toks[1:], " "), true
		}
		return "", false
	case "AUTOTEXTLIST":
		// Syntax: AUTOTEXTLIST "Initial text" \s "style" \t "tip"
		// The initial text is rendered as the visible placeholder
		// when the list isn't expanded. Pull the first quoted token.
		toks := tokenizeFieldArgs(arg)
		if len(toks) > 0 && toks[0] != "" && !strings.HasPrefix(toks[0], "\\") {
			return strings.Trim(toks[0], "\""), true
		}
		return "", false
	case "ADDRESSBLOCK", "GREETINGLINE":
		// Mail-merge composite fields with cached display text. We don't
		// run the merge so we let the result region show through.
		return "", false
	case "EQ":
		// Legacy Word 6 equation field. The instruction encodes a
		// formula with backslash-prefixed builders:
		//   \f(num, den)        — fraction
		//   \r(n, x) / \r(x)    — nth root / square root
		//   \s\up(x) / \s\do(x) — super / subscript
		//   \i(lo, hi, expr)    — integral
		//   \b(\bc\[(expr))     — bracketed expression
		// We collapse the most common forms to a readable Unicode
		// string; Word's full EQ grammar is out of scope.
		if expanded := expandEQField(arg); expanded != "" {
			return expanded, true
		}
		return "", false
	case "HYPERLINK":
		return "", false
	case "USERADDRESS":
		// Mail-merge author address. Word substitutes the user's
		// Outlook address; we have no merge context. Show the AUTHOR
		// metadata as a best-effort fallback.
		if vars.doc != nil && vars.doc.Properties.Author != "" {
			return vars.doc.Properties.Author, true
		}
		return "", false
	case "REVNUM":
		// Document revision number from docProps/app.xml.
		if vars.doc != nil && vars.doc.Revision != "" {
			return vars.doc.Revision, true
		}
		return "1", true
	case "FILESIZE":
		// Whole-document byte count, if the loader recorded it.
		if vars.doc != nil && vars.doc.RawByteSize > 0 {
			return strconv.FormatInt(vars.doc.RawByteSize, 10), true
		}
		return "0", true
	case "AUTONUM", "AUTONUMLGL", "AUTONUMOUT":
		// Auto-numbering: each occurrence increments a private counter
		// and emits the resulting decimal. The LGL/OUT variants share
		// the counter — they only differ in punctuation, which is what
		// Word inserts before formatting. We expose the plain decimal;
		// callers can format around it.
		if vars.autoNumCounter == nil {
			vars.autoNumCounter = new(int)
		}
		*vars.autoNumCounter++
		return strconv.Itoa(*vars.autoNumCounter), true
	case "SYMBOL":
		// SYMBOL embeds a single glyph by code point + font.
		if cp, ok := parseSymbolCodePointWithSwitches(arg, instrFull); ok {
			return string(cp), true
		}
		return "", false
	case "LISTNUM":
		listName := arg
		if listName == "" {
			listName = "__default__"
		}
		start, hasStart := listNumStart(instrFull)
		if vars.listNumCounters == nil {
			vars.listNumCounters = map[string]int{}
		}
		if hasStart {
			vars.listNumCounters[listName] = start
		} else {
			vars.listNumCounters[listName]++
		}
		return strconv.Itoa(vars.listNumCounters[listName]) + ")", true
	}
	if isFormulaCode(code) {
		if vars.tableCtx == nil {
			// Pure arithmetic still works without a table context.
			expr := formulaExpression(code, arg, instrFull)
			if expr == "" {
				return "", false
			}
			v, ok := evalTableFormula(expr, nil)
			if !ok {
				return "", false
			}
			return formatFormulaNumber(v), true
		}
		expr := formulaExpression(code, arg, instrFull)
		if expr == "" {
			return "", false
		}
		v, ok := evalTableFormula(expr, vars.tableCtx)
		if !ok {
			return "", false
		}
		return formatFormulaNumber(v), true
	}
	return "", false
}

// setFieldInstr parses ` SET name "value" ` into its name/value pair.
func setFieldInstr(s string) (name, value string) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) == 0 || strings.ToUpper(parts[0]) != "SET" {
		return "", ""
	}
	if len(parts) >= 2 {
		name = parts[1]
	}
	if i := strings.Index(s, name); i >= 0 {
		rest := strings.TrimSpace(s[i+len(name):])
		if strings.HasPrefix(rest, `"`) {
			if j := strings.Index(rest[1:], `"`); j >= 0 {
				value = rest[1 : 1+j]
				return name, value
			}
		}
		if rest != "" {
			value = strings.Fields(rest)[0]
		}
	}
	return name, value
}

// parseSymbolCodePoint decodes a SYMBOL field's primary arg into a rune.
func parseSymbolCodePoint(arg string) (rune, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, false
	}
	base := 10
	if strings.HasPrefix(arg, "0x") || strings.HasPrefix(arg, "0X") {
		arg = arg[2:]
		base = 16
	}
	n, err := strconv.ParseInt(arg, base, 32)
	if err != nil || n <= 0 {
		return 0, false
	}
	r := rune(n)
	if !utf8.ValidRune(r) {
		return 0, false
	}
	return r, true
}

// parseSymbolCodePointWithSwitches is parseSymbolCodePoint but also consults
// instrFull for `\h` (force hex parse) and `\u` (force unicode interpretation
// — same as no switch since we already store runes as code points).
func parseSymbolCodePointWithSwitches(arg, instrFull string) (rune, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, false
	}
	// `\h` forces hex parse on a bare digit string (no 0x prefix).
	if hexSymbolSwitch(instrFull) && !(strings.HasPrefix(arg, "0x") || strings.HasPrefix(arg, "0X")) {
		n, err := strconv.ParseInt(arg, 16, 32)
		if err != nil || n <= 0 {
			return 0, false
		}
		r := rune(n)
		if !utf8.ValidRune(r) {
			return 0, false
		}
		return r, true
	}
	return parseSymbolCodePoint(arg)
}

// activeMergeRecord returns the record that MERGEFIELDs should consult,
// honoring the catalog cursor when MergeRecords is set. Returns nil when
// there's no merge data at all.
func activeMergeRecord(vars fieldVars) map[string]string {
	if len(vars.mergeRecords) > 0 {
		idx := 0
		if vars.mergeState != nil {
			idx = vars.mergeState.Idx
		}
		if idx >= len(vars.mergeRecords) {
			// Past the end: clamp to last record rather than returning nil,
			// which would otherwise resurface every later MERGEFIELD as
			// "unresolved" and reveal the cached Word value (jarring after
			// the catalog rendered fine for the first N records).
			idx = len(vars.mergeRecords) - 1
		}
		return vars.mergeRecords[idx]
	}
	return vars.mergeData
}

// advanceMergeCursor bumps the catalog mail-merge index by one. Clamps at
// len(mergeRecords) so we don't blow past the array on a stray NEXT.
func advanceMergeCursor(vars fieldVars) {
	if vars.mergeState == nil || len(vars.mergeRecords) == 0 {
		return
	}
	if vars.mergeState.Idx+1 < len(vars.mergeRecords) {
		vars.mergeState.Idx++
	}
}

// evaluateMergeCondition rewrites a NEXTIF/SKIPIF instruction into the
// equivalent IF body and consults evaluateIfField. Substitutes
// {MERGEFIELD x} placeholders against the active record so authors can
// write `NEXTIF «State» = "CA"` and have it work.
func evaluateMergeCondition(instrFull, keyword string) bool {
	trimmed := strings.TrimSpace(instrFull)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, keyword) {
		return false
	}
	rest := strings.TrimSpace(trimmed[len(keyword):])
	if rest == "" {
		return false
	}
	rewritten := "IF " + rest + ` "1" "0"`
	v, ok := evaluateIfField(rewritten)
	if !ok {
		return false
	}
	return v == "1"
}

// mergeDataLookup does a case-insensitive lookup on a string map.
func mergeDataLookup(m map[string]string, key string) (string, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	keyLow := strings.ToLower(key)
	for k, v := range m {
		if strings.ToLower(k) == keyLow {
			return v, true
		}
	}
	return "", false
}

// mergeFieldAffixes parses \b "prefix" and \f "suffix" from a MERGEFIELD
// instrText. These are added around the value ONLY when the value is
// non-empty (Word's "If field is not empty" rule).
func mergeFieldAffixes(instrFull string) (prefix, suffix string) {
	prefix = readQuotedSwitch(instrFull, `\b`)
	suffix = readQuotedSwitch(instrFull, `\f`)
	return
}

func readQuotedSwitch(instrFull, tag string) string {
	i := strings.Index(instrFull, tag)
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(instrFull[i+len(tag):], " \t")
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : 1+end]
		}
		return rest[1:]
	}
	// Unquoted: take to next whitespace.
	for j, c := range rest {
		if c == ' ' || c == '\t' || c == '\\' {
			return rest[:j]
		}
	}
	return rest
}

// seqResetSwitch parses ` SEQ Figure \r 4 ` and returns the reset value.
func seqResetSwitch(instrFull string) (int, bool) {
	parts := strings.Fields(instrFull)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == `\r` {
			if n, err := strconv.Atoi(parts[i+1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// seqHasFlag reports whether a SEQ field carries a no-argument switch
// like \h or \c. The check is case-sensitive (Word writes lowercase).
func seqHasFlag(instrFull, flag string) bool {
	return hasFlagSwitch(instrFull, flag)
}

// seqHeadingResetSwitch parses ` SEQ Figure \s 1 ` and returns the
// heading level (1..9) the counter should reset at. Returns ok=false
// when \s is absent or its argument isn't a valid level.
func seqHeadingResetSwitch(instrFull string) (int, bool) {
	parts := strings.Fields(instrFull)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == `\s` {
			if n, err := strconv.Atoi(parts[i+1]); err == nil && n >= 1 && n <= 9 {
				return n, true
			}
		}
	}
	return 0, false
}

// headingCountByLevel returns how many headings at the given outline
// level appear in vars.headings. Used by SEQ's \s switch to detect
// "another heading at this level appeared since the last increment".
func (v fieldVars) headingCountByLevel(level int) int {
	n := 0
	for _, h := range v.headings {
		if h.Level == level {
			n++
		}
	}
	return n
}

// hasFlagSwitch is the shared no-arg switch detector used by SEQ / REF /
// PAGEREF / NOTEREF. Word writes switches as lowercase tokens (\h \p \n)
// preceded by whitespace; we compare token-by-token so substrings inside
// quoted picture switches (e.g. `\@ "h:mm"`) don't false-match.
func hasFlagSwitch(instrFull, flag string) bool {
	target := `\` + flag
	for _, p := range strings.Fields(instrFull) {
		if p == target {
			return true
		}
	}
	return false
}

// collectStyleParagraphs indexes the document's body paragraphs by their
// w:pStyle ID so STYLEREF can surface "the first paragraph with style X".
func collectStyleParagraphs(doc *docx.Document) map[string][]string {
	out := map[string][]string{}
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				if v.StyleID == "" {
					continue
				}
				if txt := paragraphPlainText(v); txt != "" {
					out[v.StyleID] = append(out[v.StyleID], txt)
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	if len(doc.Sections) > 0 {
		for _, sec := range doc.Sections {
			walk(sec.Blocks)
		}
	} else {
		walk(doc.Body)
	}
	return out
}

// listNumStart returns the explicit start value from a LISTNUM field's
// \s switch when present.
func listNumStart(instrFull string) (int, bool) {
	parts := strings.Fields(instrFull)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "\\s" {
			if n, err := strconv.Atoi(parts[i+1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// buildDocProperties merges the standard core/app props with any custom
// properties from docProps/custom.xml.
func buildDocProperties(doc *docx.Document) map[string]string {
	out := map[string]string{
		"Title":   doc.Properties.Title,
		"Author":  doc.Properties.Author,
		"Subject": doc.Properties.Subject,
		"Company": doc.Properties.Company,
		"Pages":   strconv.Itoa(doc.Properties.Pages),
		"Words":   strconv.Itoa(doc.Properties.Words),
		"Lines":   strconv.Itoa(doc.Properties.Lines),
	}
	for k, v := range doc.CustomProperties {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// collectHeadings flattens the document body into a list of {level, text,
// style} entries for TOC synthesis. The Style field carries the paragraph
// style ID so the TOC `\t` switch can filter by it.
func collectHeadings(doc *docx.Document) []tocEntry {
	var out []tocEntry
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				lvl := headingLevel(v, doc)
				if lvl > 0 {
					txt := paragraphPlainText(v)
					if txt != "" {
						out = append(out, tocEntry{
							Level:    lvl,
							Text:     txt,
							Style:    v.StyleID,
							Bookmark: firstHeadingBookmark(v),
						})
					}
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	if len(doc.Sections) > 0 {
		for _, sec := range doc.Sections {
			walk(sec.Blocks)
		}
	} else {
		walk(doc.Body)
	}
	return out
}

// filterTOCEntries applies a TOC field's switches to a heading + TC list:
//   - `\o N-M` restricts entries to outline levels in that range.
//   - `\t "Style,N,..."` adds entries from author-named styles. When `\t`
//     is given WITHOUT `\u`, default Heading-1..9 entries are suppressed
//     unless they overlap the style map. When BOTH `\t` and `\u` are
//     given, the union is taken (Word's behavior).
//   - `\u` keeps entries whose paragraphs have an explicit outlineLvl.
//     Our headings list already includes those since headingLevel honors
//     OutlineLvl; `\u` therefore is a no-op extension for our model
//     beyond suppressing the implicit Title rule.
//   - `\f SEQID` restricts TC entries to a single SEQ stream.
//   - `\b name` filters to entries whose Bookmark falls inside the named
//     bookmark range — not modelled here (we lack bookmark span info);
//     entries pass through.
func filterTOCEntries(headings, tcs []tocEntry, sw tocSwitches) []tocEntry {
	combined := make([]tocEntry, 0, len(headings)+len(tcs))
	combined = append(combined, headings...)
	combined = append(combined, tcs...)
	if !sw.UseStyleMap && sw.MinLvl == 0 && sw.MaxLvl == 0 && sw.SeqName == "" {
		return combined
	}
	out := make([]tocEntry, 0, len(combined))
	minL := sw.MinLvl
	maxL := sw.MaxLvl
	if minL == 0 && maxL == 0 {
		minL, maxL = 1, 9
	}
	for _, e := range combined {
		level := e.Level
		if sw.UseStyleMap {
			key := strings.ToLower(strings.ReplaceAll(e.Style, " ", ""))
			if lv, ok := sw.StyleMap[key]; ok {
				level = lv
			} else if !sw.UseOutline {
				continue
			}
		}
		if level < minL || level > maxL {
			continue
		}
		if sw.SeqName != "" && e.Seq != "" && !strings.EqualFold(e.Seq, sw.SeqName) {
			continue
		}
		e.Level = level
		out = append(out, e)
	}
	return out
}

// collectTCEntries walks the body looking for TC field instruction runs
// and parses each into a tocEntry. Done as a pre-pass so the TOC field —
// which usually appears near the start of the doc — can include marks
// defined later.
func collectTCEntries(doc *docx.Document) []tocEntry {
	var out []tocEntry
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for _, r := range v.Runs {
					if r.InstrText == "" {
						continue
					}
					if entry, ok := parseTCInstr(r.InstrText); ok {
						out = append(out, entry)
					}
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	if len(doc.Sections) > 0 {
		for _, sec := range doc.Sections {
			walk(sec.Blocks)
		}
	} else {
		walk(doc.Body)
	}
	return out
}

// collectXEEntries gathers XE field titles for INDEX synthesis.
func collectXEEntries(doc *docx.Document) []string {
	var out []string
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for _, r := range v.Runs {
					if r.InstrText == "" {
						continue
					}
					if title := parseXEInstr(r.InstrText); title != "" {
						out = append(out, title)
					}
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	if len(doc.Sections) > 0 {
		for _, sec := range doc.Sections {
			walk(sec.Blocks)
		}
	} else {
		walk(doc.Body)
	}
	return out
}

// headingLevel returns 1..9 if p is a heading paragraph.
func headingLevel(p docx.Paragraph, doc *docx.Document) int {
	_ = doc
	if p.OutlineLvl >= 1 && p.OutlineLvl <= 9 {
		return p.OutlineLvl
	}
	if p.StyleID != "" {
		id := strings.ToLower(p.StyleID)
		if id == "title" {
			return 1
		}
		if strings.HasPrefix(id, "heading") {
			tail := strings.TrimPrefix(id, "heading")
			if n, err := strconv.Atoi(tail); err == nil && n >= 1 && n <= 9 {
				return n
			}
		}
	}
	return 0
}

// firstHeadingBookmark picks the first `_Toc...`-style anchor on a
// heading paragraph. Word inserts these implicitly when the user adds the
// heading to a TOC; falling back to ANY bookmark on the paragraph keeps
// us useful for hand-authored docs that bookmarked headings manually.
func firstHeadingBookmark(p docx.Paragraph) string {
	var fallback string
	for _, r := range p.Runs {
		if r.Bookmark == "" {
			continue
		}
		if strings.HasPrefix(r.Bookmark, "_Toc") {
			return r.Bookmark
		}
		if fallback == "" {
			fallback = r.Bookmark
		}
	}
	return fallback
}

// paragraphPlainText collapses runs into a single string for TOC entries.
func paragraphPlainText(p docx.Paragraph) string {
	var b strings.Builder
	for _, r := range p.Runs {
		if r.FieldBegin || r.FieldSep || r.FieldEnd || r.InstrText != "" {
			continue
		}
		if r.IsBreak {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(r.Text)
	}
	return strings.TrimSpace(b.String())
}

// formatTOC renders a multi-line TOC from the heading list using default
// switches (matches Word's bare `{ TOC }`).
func formatTOC(entries []tocEntry) string {
	return formatTOCWithSwitches(entries, tocSwitches{})
}

// formatTOCWithSwitches renders the TOC, honoring \n / \d / \p switches:
//   - sw.HidePageNums: suppress the trailing page column.
//   - sw.HideMinLvl..HideMaxLvl: suppress the page column only for those
//     levels (other levels render with the page).
//   - sw.Separator: replace the dot-leader with the literal separator.
//     `\d " — "` produces `Heading — 12`.
//   - sw.TabLeader: replace dots with another single character.
//
// The visible width target is 60 columns — purely cosmetic for plain-text
// dumps; the surrounding text layout pass will rewrap and re-leader.
func formatTOCWithSwitches(entries []tocEntry, sw tocSwitches) string {
	const lineWidth = 60
	leader := byte('.')
	if sw.TabLeader != "" {
		// First rune only — the TabLeader pattern is documented as one
		// char in OOXML.
		for _, r := range sw.TabLeader {
			if r >= 32 && r < 127 {
				leader = byte(r)
			}
			break
		}
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		depth := e.Level - 1
		if depth < 0 {
			depth = 0
		}
		indent := strings.Repeat("  ", depth)
		title := strings.TrimSpace(e.Text)
		body := indent + title
		showPage := !sw.HidePageNums && e.PageNum > 0
		if sw.HideMaxLvl > 0 && e.Level >= sw.HideMinLvl && e.Level <= sw.HideMaxLvl {
			showPage = false
		}
		if !showPage {
			b.WriteString(body)
			continue
		}
		pageStr := strconv.Itoa(e.PageNum)
		if sw.Separator != "" {
			// `\d "sep"` overrides the dot leader entirely.
			b.WriteString(body)
			b.WriteString(sw.Separator)
			b.WriteString(pageStr)
			continue
		}
		// Default: pad with leader chars between title and page number,
		// targeting lineWidth total columns.
		minGap := 4
		used := len(body) + len(pageStr)
		if used+minGap > lineWidth {
			b.WriteString(body)
			b.WriteByte(' ')
			b.WriteString(pageStr)
			continue
		}
		gap := lineWidth - used - 2
		b.WriteString(body)
		b.WriteByte(' ')
		for k := 0; k < gap; k++ {
			b.WriteByte(leader)
		}
		b.WriteByte(' ')
		b.WriteString(pageStr)
	}
	return b.String()
}

// formatIndex synthesizes a simple alphabetical index from XE entries.
// Duplicates collapse; "Major:Minor" entries indent the minor part under
// the major heading.
// formatIndexWithPages is formatIndex enriched with a per-title page
// list. When xePages is nil or empty, falls back to formatIndex's
// page-less layout. Pages are de-duped and emitted as a comma-separated
// "term ... 3, 5, 8" trailer.
func formatIndexWithPages(entries []string, xePages *map[string][]int) string {
	if xePages == nil || len(*xePages) == 0 {
		return formatIndex(entries)
	}
	pageList := func(title string) string {
		raw := (*xePages)[title]
		if len(raw) == 0 {
			return ""
		}
		seen := map[int]bool{}
		out := make([]int, 0, len(raw))
		for _, p := range raw {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j] < out[j-1]; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
		var b strings.Builder
		for i, p := range out {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Itoa(p))
		}
		return b.String()
	}
	if len(entries) == 0 {
		return ""
	}
	type indexLine struct {
		major string
		minor map[string]bool
	}
	majorOrder := []string{}
	lines := map[string]*indexLine{}
	majorPages := map[string]string{}
	minorPages := map[string]string{}
	for _, raw := range entries {
		major, minor, _ := strings.Cut(raw, ":")
		major = strings.TrimSpace(major)
		minor = strings.TrimSpace(minor)
		if major == "" {
			continue
		}
		if _, ok := lines[major]; !ok {
			lines[major] = &indexLine{major: major, minor: map[string]bool{}}
			majorOrder = append(majorOrder, major)
		}
		if minor != "" {
			lines[major].minor[minor] = true
			minorPages[major+"\x00"+minor] = pageList(raw)
		} else {
			majorPages[major] = pageList(raw)
		}
	}
	for i := 1; i < len(majorOrder); i++ {
		for j := i; j > 0 && majorOrder[j] < majorOrder[j-1]; j-- {
			majorOrder[j], majorOrder[j-1] = majorOrder[j-1], majorOrder[j]
		}
	}
	var b strings.Builder
	for i, m := range majorOrder {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m)
		if pp := majorPages[m]; pp != "" {
			b.WriteString(", ")
			b.WriteString(pp)
		}
		mins := make([]string, 0, len(lines[m].minor))
		for k := range lines[m].minor {
			mins = append(mins, k)
		}
		for i := 1; i < len(mins); i++ {
			for j := i; j > 0 && mins[j] < mins[j-1]; j-- {
				mins[j], mins[j-1] = mins[j-1], mins[j]
			}
		}
		for _, mn := range mins {
			b.WriteString("\n  ")
			b.WriteString(mn)
			if pp := minorPages[m+"\x00"+mn]; pp != "" {
				b.WriteString(", ")
				b.WriteString(pp)
			}
		}
	}
	return b.String()
}

func formatIndex(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	type indexLine struct {
		major string
		minor []string
	}
	// Stable de-dup then alphabetise.
	seen := map[string]map[string]bool{}
	majorOrder := []string{}
	for _, raw := range entries {
		major, minor, _ := strings.Cut(raw, ":")
		major = strings.TrimSpace(major)
		minor = strings.TrimSpace(minor)
		if major == "" {
			continue
		}
		if _, ok := seen[major]; !ok {
			seen[major] = map[string]bool{}
			majorOrder = append(majorOrder, major)
		}
		if minor != "" && !seen[major][minor] {
			seen[major][minor] = true
		}
	}
	// Sort majors alphabetically (Go-style ascending bytes — good enough).
	for i := 1; i < len(majorOrder); i++ {
		for j := i; j > 0 && majorOrder[j] < majorOrder[j-1]; j-- {
			majorOrder[j], majorOrder[j-1] = majorOrder[j-1], majorOrder[j]
		}
	}
	var b strings.Builder
	for i, m := range majorOrder {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m)
		// Sort minors for stability.
		mins := make([]string, 0, len(seen[m]))
		for k := range seen[m] {
			mins = append(mins, k)
		}
		for i := 1; i < len(mins); i++ {
			for j := i; j > 0 && mins[j] < mins[j-1]; j-- {
				mins[j], mins[j-1] = mins[j-1], mins[j]
			}
		}
		for _, mn := range mins {
			b.WriteString("\n  ")
			b.WriteString(mn)
		}
	}
	return b.String()
}

// formatCitation produces an APA-style "(Author, Year)" string.
func formatCitation(s docx.BibSource) string {
	author := ""
	if len(s.Authors) > 0 {
		author = s.Authors[0]
	}
	switch {
	case author != "" && s.Year != "":
		return "(" + author + ", " + s.Year + ")"
	case author != "":
		return "(" + author + ")"
	case s.Year != "":
		return "(" + s.Year + ")"
	case s.Title != "":
		return "(" + s.Title + ")"
	}
	return "(" + s.Tag + ")"
}

// formatBibliography emits a newline-joined list of full entries.
func formatBibliography(sources map[string]docx.BibSource) string {
	tags := make([]string, 0, len(sources))
	for t := range sources {
		tags = append(tags, t)
	}
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && tags[j] < tags[j-1]; j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
	var b strings.Builder
	for i, tag := range tags {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(formatBibEntry(sources[tag]))
	}
	return b.String()
}

func formatBibEntry(s docx.BibSource) string {
	var b strings.Builder
	if len(s.Authors) > 0 {
		b.WriteString(strings.Join(s.Authors, ", "))
	}
	if s.Year != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('(')
		b.WriteString(s.Year)
		b.WriteByte(')')
	}
	if s.Title != "" {
		if b.Len() > 0 {
			b.WriteString(". ")
		}
		b.WriteString(s.Title)
	}
	if s.JournalName != "" {
		b.WriteString(". ")
		b.WriteString(s.JournalName)
	}
	if s.Publisher != "" {
		b.WriteString(". ")
		b.WriteString(s.Publisher)
	}
	if s.City != "" {
		b.WriteString(", ")
		b.WriteString(s.City)
	}
	if s.Pages != "" {
		b.WriteString(", ")
		b.WriteString(s.Pages)
	}
	if s.URL != "" {
		b.WriteString(". ")
		b.WriteString(s.URL)
	}
	b.WriteByte('.')
	return b.String()
}

// formFieldOutput returns the synthetic glyph for a legacy form field.
// FORMCHECKBOX → ☒/☐; FORMDROPDOWN → currently-selected choice;
// FORMTEXT → ffData.Default (or "" if nothing to show).
func formFieldOutput(ff *docx.FormFieldInfo, code string) (string, bool) {
	if ff == nil {
		return "", false
	}
	kind := ff.Kind
	if kind == "" {
		// Infer from the field code when ffData didn't say.
		switch code {
		case "FORMCHECKBOX":
			kind = "checkbox"
		case "FORMDROPDOWN":
			kind = "dropdown"
		case "FORMTEXT":
			kind = "text"
		}
	}
	switch kind {
	case "checkbox":
		if ff.Checked {
			return "☒", true
		}
		return "☐", true
	case "dropdown":
		var sel string
		if ff.Selected >= 0 && ff.Selected < len(ff.Choices) {
			sel = ff.Choices[ff.Selected]
		} else if len(ff.Choices) > 0 {
			sel = ff.Choices[0]
		}
		if sel != "" {
			return sel + " ▾", true
		}
		return "▾", true
	case "text":
		if ff.Default != "" {
			return ff.Default, true
		}
		// Empty FORMTEXT: synthesize a placeholder that hints at the
		// expected content. We honor TextType for the common variants
		// Word ships ("number" / "date" / "currentDate" / "currentTime"
		// / "calculation"); fall back to a blank slot for regular text.
		switch ff.TextType {
		case "number":
			return "0", true
		case "date":
			return "MM/DD/YYYY", true
		case "currentDate":
			return time.Now().Format("01/02/2006"), true
		case "currentTime":
			return time.Now().Format("15:04"), true
		case "calculation":
			return "=", true
		default:
			return "          ", true
		}
	}
	return "", false
}

// decorationBorderForField returns a soft outlined border style that
// reads as a form widget. Checkbox/dropdown use a 1pt outline; text
// fields use a thicker bottom rule via a single style hint (the renderer
// draws all four sides — close enough). Color stays gray so the control
// doesn't compete with the surrounding text.
func decorationBorderForField(code string) docx.BorderEdge {
	switch code {
	case "FORMCHECKBOX", "FORMDROPDOWN":
		return docx.BorderEdge{Style: "single", Sz: 0.5, Color: "808080"}
	case "FORMTEXT":
		return docx.BorderEdge{Style: "single", Sz: 0.5, Color: "B0B0B0"}
	}
	return docx.BorderEdge{}
}

// formFieldKindCode derives the field code from a FormFieldInfo when
// the instrText didn't supply one (some FORMFIELDs ship with empty
// instrText and just the ffData blob).
func formFieldKindCode(ff *docx.FormFieldInfo) (string, string) {
	if ff == nil {
		return "", ""
	}
	switch ff.Kind {
	case "checkbox":
		return ff.Kind, "FORMCHECKBOX"
	case "dropdown":
		return ff.Kind, "FORMDROPDOWN"
	case "text":
		return ff.Kind, "FORMTEXT"
	}
	return "", ""
}

// formatFieldDateTime applies a `\@ "format"` switch to t. When no switch
// is present, fallback is used as a sensible default. Supported tokens:
// yyyy, yy, MMMM, MMM, MM, M, dddd, ddd, dd, d, HH, H, hh, h, mm, m, ss,
// s, AM/PM, am/pm.
func formatFieldDateTime(t time.Time, instrFull, fallback string) string {
	layout := fieldDateLayoutSwitch(instrFull)
	if layout == "" {
		return t.Format(fallback)
	}
	return applyWordDateLayout(t, layout)
}

// fieldDateLayoutSwitch extracts the quoted body of a `\@ "format"`
// switch. Returns "" when no such switch is present.
func fieldDateLayoutSwitch(instrFull string) string {
	i := strings.Index(instrFull, `\@`)
	if i < 0 {
		return ""
	}
	rest := instrFull[i+2:]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, `"`) {
		// Unquoted form: `\@ yyyy/MM/dd` until end-of-string or next `\`.
		if j := strings.Index(rest, " \\"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return rest[1:]
	}
	return rest[1 : 1+end]
}

// applyWordDateLayout converts a Word format string ("yyyy/MM/dd h:mm")
// into the corresponding rendered time. We process longer tokens first
// so "MMMM" doesn't get matched as four "M"s. Literal tokens (slashes,
// colons, the words "AM"/"PM") pass through.
func applyWordDateLayout(t time.Time, layout string) string {
	type repl struct {
		tok string
		val string
	}
	year, month, day := t.Date()
	hour, minute, second := t.Clock()
	weekday := t.Weekday()
	twoDigit := func(n int) string {
		if n < 10 {
			return "0" + strconv.Itoa(n)
		}
		return strconv.Itoa(n)
	}
	hour12 := hour % 12
	if hour12 == 0 {
		hour12 = 12
	}
	monthLong := []string{"", "January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	monthShort := []string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	dayLong := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	dayShort := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	tokens := []repl{
		{"yyyy", strconv.Itoa(year)},
		{"yy", twoDigit(year % 100)},
		{"MMMM", monthLong[int(month)]},
		{"MMM", monthShort[int(month)]},
		{"MM", twoDigit(int(month))},
		{"M", strconv.Itoa(int(month))},
		{"dddd", dayLong[weekday]},
		{"ddd", dayShort[weekday]},
		{"dd", twoDigit(day)},
		{"d", strconv.Itoa(day)},
		{"HH", twoDigit(hour)},
		{"H", strconv.Itoa(hour)},
		{"hh", twoDigit(hour12)},
		{"h", strconv.Itoa(hour12)},
		{"mm", twoDigit(minute)},
		{"ss", twoDigit(second)},
		{"s", strconv.Itoa(second)},
		{"AM/PM", func() string {
			if hour < 12 {
				return "AM"
			}
			return "PM"
		}()},
		{"am/pm", func() string {
			if hour < 12 {
				return "am"
			}
			return "pm"
		}()},
		// Word also accepts mixed-case variants like "A/P", "a/p", and
		// the implicit "AMPM" / "ampm" without separators. We map them
		// onto the canonical pair.
		{"AMPM", func() string {
			if hour < 12 {
				return "AM"
			}
			return "PM"
		}()},
		{"ampm", func() string {
			if hour < 12 {
				return "am"
			}
			return "pm"
		}()},
		{"A/P", func() string {
			if hour < 12 {
				return "A"
			}
			return "P"
		}()},
		{"a/p", func() string {
			if hour < 12 {
				return "a"
			}
			return "p"
		}()},
		{"tt", func() string {
			if hour < 12 {
				return "am"
			}
			return "pm"
		}()},
	}
	// We need to consume tokens left-to-right with longest-first matching,
	// so a single sweep with prioritized comparison.
	//
	// Single-quote escaping: per Word's date picture-switch spec, text
	// inside paired single quotes is emitted literally. `'d'` produces the
	// letter d, not the day-of-month. A doubled single-quote `''` inside
	// the quoted region emits one literal single quote. Outside the spec
	// for stray opens we tolerate the bare character.
	var b strings.Builder
	for i := 0; i < len(layout); {
		if layout[i] == '\'' {
			// Walk to the matching closing quote, copying contents literally.
			i++
			for i < len(layout) {
				if layout[i] == '\'' {
					// Doubled? Emit one apostrophe and continue inside the
					// quoted region; single? Stop, exit literal mode.
					if i+1 < len(layout) && layout[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++ // consume closer
					break
				}
				b.WriteByte(layout[i])
				i++
			}
			continue
		}
		matched := false
		for _, tk := range tokens {
			if strings.HasPrefix(layout[i:], tk.tok) {
				b.WriteString(tk.val)
				i += len(tk.tok)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(layout[i])
			i++
		}
	}
	return b.String()
}

// formatNumericValue applies a `\# "format"` switch to v. Returns the
// decimal string when no switch is present.
func formatNumericValue(v float64, instrFull string) string {
	if s := formatNumericSwitchSep(v, instrFull, ".", ","); s != "" {
		return s
	}
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// formatNumericValueWith is like formatNumericValue but honors a
// locale-specific decimal symbol and thousands grouping separator.
func formatNumericValueWith(v float64, instrFull, decSym, grpSep string) string {
	if decSym == "" {
		decSym = "."
	}
	if grpSep == "" {
		grpSep = ","
	}
	if s := formatNumericSwitchSep(v, instrFull, decSym, grpSep); s != "" {
		return s
	}
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	out := strconv.FormatFloat(v, 'f', -1, 64)
	if decSym != "." {
		out = strings.Replace(out, ".", decSym, 1)
	}
	return out
}

// formatNumericSwitch is the default-locale wrapper. Most call sites use
// "." / "," so we keep this signature stable.
func formatNumericSwitch(v float64, instrFull string) string {
	return formatNumericSwitchSep(v, instrFull, ".", ",")
}

// numericSwitchLocale is the doc-locale variant: it consults
// settings.xml's w:decimalSymbol / w:listSeparator (carried on vars)
// instead of the hardcoded "." / "," fallback. Empty on no `\#`.
func numericSwitchLocale(v float64, instrFull string, vars fieldVars) string {
	decSym := vars.decimalSymbol
	if decSym == "" {
		decSym = "."
	}
	grpSep := vars.listSeparator
	if grpSep == "" {
		grpSep = ","
	}
	return formatNumericSwitchSep(v, instrFull, decSym, grpSep)
}

// formatNumericSwitchSep implements Word's `\#` numeric picture-format
// switch. Recognized format chars: '0' = digit required, '#' = digit
// optional, '.' = decimal separator, ',' = thousands separator,
// 'x' = drop digits to the right of this position (truncate-then-round),
// '%' = percent (value gets multiplied by 100 before formatting).
// Any other characters before / after the numeric block (or between
// thousands and decimal) are kept as literal prefix / suffix so
// currency symbols like '$' and unit suffixes pass through.
//
// A semicolon splits the picture into positive ; negative ; zero
// sub-formats (e.g. `0.00;(0.00);-` shows negatives in parens, zero as
// a dash).
//
// decSym / grpSep are the locale-specific decimal point and thousands
// separator the OUTPUT uses; the PICTURE always uses "." and ",".
// Returns "" when no `\#` switch is present.
func formatNumericSwitchSep(v float64, instrFull, decSym, grpSep string) string {
	i := strings.Index(instrFull, `\#`)
	if i < 0 {
		return ""
	}
	rest := instrFull[i+2:]
	rest = strings.TrimLeft(rest, " \t")
	picture := ""
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			picture = rest[1:]
		} else {
			picture = rest[1 : 1+end]
		}
	} else {
		if j := strings.Index(rest, " \\"); j >= 0 {
			picture = strings.TrimSpace(rest[:j])
		} else {
			picture = strings.TrimSpace(rest)
		}
	}
	if picture == "" {
		return ""
	}
	// Word allows single-quote escapes around literal chars to keep them
	// out of the numeric block ("\# '$'#,##0"). We resolve quotes to a
	// sentinel byte so subsequent format-rune scanning ignores them,
	// then restore them as literals at the end.
	picture = unescapeNumericPicture(picture)
	posPic, negPic, zeroPic, hasNeg, hasZero := splitNumericPicture(picture)
	abs := v
	negative := v < 0
	if negative {
		abs = -v
	}
	if hasZero && v == 0 {
		return applyNumericPicture(0, zeroPic, false, decSym, grpSep)
	}
	if negative && hasNeg && negPic != "" {
		// Negative format already encodes the sign — suppress the implicit
		// leading minus.
		return applyNumericPicture(abs, negPic, false, decSym, grpSep)
	}
	chosen := posPic
	return applyNumericPicture(abs, chosen, negative, decSym, grpSep)
}

// splitNumericPicture decomposes a `\#` picture into the three Word
// sub-pictures (positive ; negative ; zero) separated by un-escaped
// semicolons. Quoted segments don't terminate sub-pictures.
func splitNumericPicture(picture string) (pos, neg, zero string, hasNeg, hasZero bool) {
	parts := []string{}
	cur := strings.Builder{}
	inQ := false
	for i := 0; i < len(picture); i++ {
		c := picture[i]
		if c == '\x01' { // sentinel: previously a quote
			inQ = !inQ
			cur.WriteByte(c)
			continue
		}
		if c == ';' && !inQ {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	parts = append(parts, cur.String())
	pos = parts[0]
	if len(parts) > 1 {
		neg = parts[1]
		hasNeg = true
	}
	if len(parts) > 2 {
		zero = parts[2]
		hasZero = true
	}
	return
}

// unescapeNumericPicture replaces every "'" with a sentinel \x01 so the
// format scanner can skip quoted literals. The applyNumericPicture stage
// turns the sentinel pair back into the verbatim quoted content.
func unescapeNumericPicture(s string) string {
	return strings.ReplaceAll(s, "'", "\x01")
}

// applyNumericPicture renders v into picture, treating non-format runes
// as literal text. addMinus prepends a '-' to the numeric block when
// the caller hasn't already supplied a negative sub-format.
//
// decSym / grpSep are the runtime decimal point and thousands separator.
// Format chars in `picture` are still '.' / ',' — the substitution
// happens at output time.
//
// Recognized format chars inside the numeric block:
//
//	0  required digit
//	#  optional digit
//	.  decimal point
//	,  thousands grouping
//	x  drop / round at this fractional position
//	+  prepend '+' on positive, '-' on negative
//	-  prepend ' ' on positive, '-' on negative
//	%  percent (value *= 100)
func applyNumericPicture(v float64, picture string, addMinus bool, decSym, grpSep string) string {
	if picture == "" {
		return ""
	}
	if decSym == "" {
		decSym = "."
	}
	if grpSep == "" {
		grpSep = ","
	}
	if strings.Contains(picture, "%") {
		v *= 100
	}
	// Find the numeric block (first run of [0#.,x] outside quoted regions).
	start := -1
	end := -1
	for i := 0; i < len(picture); i++ {
		c := picture[i]
		if c == '\x01' { // skip the sentinel pair (literal block boundary)
			if start >= 0 {
				break
			}
			continue
		}
		if c == '0' || c == '#' || c == '.' || c == ',' || c == 'x' {
			if start < 0 {
				start = i
			}
			end = i + 1
		} else if start >= 0 {
			break
		}
	}
	if start < 0 {
		return restoreLiteralSentinel(picture)
	}
	prefix := picture[:start]
	suffix := picture[end:]
	numPic := picture[start:end]

	// Detect sign-prefix tokens in the literal prefix.
	signMode := "" // "", "+", "-"
	for i := len(prefix) - 1; i >= 0; i-- {
		c := prefix[i]
		if c == '+' {
			signMode = "+"
			prefix = prefix[:i] + prefix[i+1:]
			break
		}
		if c == '-' && i+1 == len(prefix) {
			// Trailing '-' in the prefix is the Word sign-control marker;
			// '-' elsewhere is a literal (rendered later).
			signMode = "-"
			prefix = prefix[:i]
			break
		}
		if c != ' ' && c != '\t' && c != '\x01' {
			break
		}
	}

	intPart, fracPart, hasFrac := strings.Cut(numPic, ".")
	intDigitsNeeded := strings.Count(intPart, "0")
	// 'x' in the fractional part drops everything after it (Word rounds
	// at that position). 'x' in the integer part rounds away the LOW
	// digit (we approximate as "round to nearest 10^k").
	fracDigits := strings.Count(fracPart, "0") + strings.Count(fracPart, "#")
	fracDrop := strings.IndexByte(fracPart, 'x')
	if fracDrop >= 0 {
		fracDigits = fracDrop
		fracPart = fracPart[:fracDrop]
	}
	intDrop := strings.LastIndexByte(intPart, 'x')
	if intDrop >= 0 {
		// Number of zeroes to round to.
		zeroes := len(intPart) - intDrop - 1
		mul := 1.0
		for i := 0; i < zeroes; i++ {
			mul *= 10
		}
		if mul > 0 {
			v = float64(int64(v/mul+0.5)) * mul
		}
		// Strip the 'x' run; downstream code treats the rest as digit spec.
		intPart = strings.ReplaceAll(intPart, "x", "0")
	}

	if fracDigits > 0 {
		mul := 1.0
		for i := 0; i < fracDigits; i++ {
			mul *= 10
		}
		v = float64(int64(v*mul+0.5)) / mul
	} else if !hasFrac {
		v = float64(int64(v + 0.5))
	}
	intVal := int64(v)
	intStr := strconv.FormatInt(intVal, 10)
	for len(intStr) < intDigitsNeeded {
		intStr = "0" + intStr
	}
	if strings.Contains(intPart, ",") {
		var b strings.Builder
		n := len(intStr)
		for i, c := range intStr {
			if i > 0 && (n-i)%3 == 0 {
				b.WriteString(grpSep)
			}
			b.WriteRune(c)
		}
		intStr = b.String()
	}
	numStr := intStr
	if hasFrac && fracDigits > 0 {
		// Render via FormatFloat to dodge per-digit float imprecision: ask
		// for exactly fracDigits decimal places, then peel them off the
		// rendered string.
		rendered := strconv.FormatFloat(v, 'f', fracDigits, 64)
		dotAt := strings.IndexByte(rendered, '.')
		fracStr := ""
		if dotAt >= 0 {
			fracStr = rendered[dotAt+1:]
		}
		for len(fracStr) < fracDigits {
			fracStr += "0"
		}
		if len(fracStr) > fracDigits {
			fracStr = fracStr[:fracDigits]
		}
		// Trim '#' positions where the digit happens to be 0 (Word treats
		// them as optional).
		for i := len(fracStr) - 1; i >= 0; i-- {
			if i >= len(fracPart) {
				break
			}
			if fracPart[i] == '#' && fracStr[i] == '0' {
				fracStr = fracStr[:i]
				continue
			}
			break
		}
		if fracStr != "" {
			numStr += decSym + fracStr
		}
	}
	// Apply sign-prefix logic.
	switch signMode {
	case "+":
		if addMinus {
			numStr = "-" + numStr
		} else {
			numStr = "+" + numStr
		}
	case "-":
		if addMinus {
			numStr = "-" + numStr
		} else {
			numStr = " " + numStr
		}
	default:
		if addMinus {
			numStr = "-" + numStr
		}
	}
	return restoreLiteralSentinel(prefix + numStr + suffix)
}

// restoreLiteralSentinel turns the \x01 quote markers back into a no-op
// (the quoted content survives as-is; quotes themselves are stripped
// per Word's behavior).
func restoreLiteralSentinel(s string) string {
	if !strings.ContainsRune(s, '\x01') {
		return s
	}
	return strings.ReplaceAll(s, "\x01", "")
}

// applyValueFormatters applies the `\@` (date) and `\#` (numeric) switches
// to a text value coming from MERGEFIELD / DOCPROPERTY / DOCVARIABLE.
// When neither switch is present the value passes through unchanged.
// Uses NumberExtractor to peel currency / unit decorations off the raw
// string before re-formatting.
func applyValueFormatters(value, instrFull string, vars fieldVars) string {
	if strings.Contains(instrFull, `\#`) {
		if n, frac, ok := extractNumber(value); ok {
			decSym := vars.decimalSymbol
			if decSym == "" {
				decSym = "."
			}
			grpSep := vars.listSeparator
			if grpSep == "" {
				grpSep = ","
			}
			if formatted := formatNumericSwitchSep(n+frac, instrFull, decSym, grpSep); formatted != "" {
				return formatted
			}
		}
	}
	if strings.Contains(instrFull, `\@`) {
		if t, ok := parseFlexibleDate(value); ok {
			return formatFieldDateTime(t, instrFull, "2006-01-02")
		}
	}
	return value
}

// extractNumber peels a numeric value out of a free-text string. It tolerates
// currency symbols, unit suffixes, parens for negatives, and both "1,234.56"
// and "1.234,56" grouping conventions. Returns ok=false when the string
// contains no obvious number.
func extractNumber(s string) (intPart float64, fracPart float64, ok bool) {
	if s == "" {
		return 0, 0, false
	}
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = s[1 : len(s)-1]
	}
	// Walk left → right collecting the first digit run + intervening
	// punctuation. The first non-digit-non-sep run AFTER the digits
	// terminates the number.
	var b strings.Builder
	started := false
	sawDot := false
	sawComma := false
	for _, r := range s {
		if r == '-' && !started {
			negative = true
			continue
		}
		if r == '+' && !started {
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			started = true
			continue
		}
		if r == '.' || r == ',' || r == ' ' || r == '\u00a0' /* NBSP */ {
			if !started {
				continue
			}
			if r == '.' {
				sawDot = true
				b.WriteRune('.')
			} else if r == ',' {
				sawComma = true
				b.WriteRune(',')
			} else {
				continue // ignore spaces inside numbers
			}
			continue
		}
		if started {
			break
		}
	}
	raw := b.String()
	if raw == "" {
		return 0, 0, false
	}
	// Decide which symbol is the decimal point. Heuristic: if BOTH appear,
	// the LAST one is the decimal point (matches "1.234,56" and
	// "1,234.56"). If only one appears AND it's followed by exactly 3
	// digits AND not at the very end, treat it as a grouping separator.
	last := strings.LastIndexAny(raw, ".,")
	dec := '.'
	if sawDot && sawComma {
		dec = rune(raw[last])
	} else if sawComma && !sawDot {
		// Single "," — could be either; default to thousands when followed
		// by exactly 3 digits.
		if last >= 0 && len(raw)-last-1 == 3 {
			dec = '?' // no decimal; treat all separators as grouping
		} else {
			dec = ','
		}
	}
	cleaned := strings.Builder{}
	for i, c := range raw {
		if c == '.' || c == ',' {
			if rune(c) == dec && i == last {
				cleaned.WriteByte('.')
			}
			// else: grouping char, drop it
			continue
		}
		cleaned.WriteRune(c)
	}
	out := cleaned.String()
	if out == "" {
		return 0, 0, false
	}
	if v, err := strconv.ParseFloat(out, 64); err == nil {
		if negative {
			v = -v
		}
		// Split into integer and fractional pieces so the caller can pass
		// the sum back into the picture formatter (which expects a single
		// float). The split exists only because the API is friendlier
		// that way — both halves are summed at call-site.
		return v, 0, true
	}
	return 0, 0, false
}

// parseFlexibleDate handles the date-shaped strings that DOCPROPERTY /
// MERGEFIELD typically carry: ISO-8601, RFC-3339, "YYYY/MM/DD", and the
// docProps/core.xml epoch form. Returns ok=false when nothing parses.
func parseFlexibleDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006/01/02",
		"01/02/2006",
		"02/01/2006",
		"Jan 2 2006",
		"Jan 02, 2006",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// initialsOf extracts a 2-3 letter initials string from a full name.
// "Alice Wonder Land" → "AWL". Falls back to the whole name if it has
// no spaces.
func initialsOf(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		b.WriteRune(r[0])
	}
	return strings.ToUpper(b.String())
}

// tokenizeFieldArgs splits a field's argument list honoring double-quoted
// strings. "a b" c → ["a b", "c"]. Switches (\…) and their operands stay
// separate; the caller can filter them. Whitespace inside quotes is
// preserved verbatim.
func tokenizeFieldArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQ = !inQ
			continue
		}
		if !inQ && (c == ' ' || c == '\t') {
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

// evaluateIfField parses and evaluates a Word IF field instruction:
//
//	IF <e1> <op> <e2> "true" "false"
//
// op ∈ {=, <>, !=, <, >, <=, >=}. The operands are quoted strings, numbers,
// or unquoted identifiers (treated as case-insensitive strings). Returns
// the chosen branch text + ok=true on a successful evaluation; ok=false
// when the instruction can't be parsed (caller falls back to cached
// result).
func evaluateIfField(instrFull string) (string, bool) {
	s := strings.TrimSpace(instrFull)
	upper := strings.ToUpper(s)
	if !strings.HasPrefix(upper, "IF") {
		return "", false
	}
	s = strings.TrimSpace(s[2:])
	toks := tokenizeFieldArgs(s)
	if len(toks) < 5 {
		return "", false
	}
	left, op, right := toks[0], toks[1], toks[2]
	truePart := strings.Trim(toks[3], `"`)
	falsePart := strings.Trim(toks[4], `"`)
	pass := ifCompare(left, op, right)
	if pass {
		return truePart, true
	}
	return falsePart, true
}

func ifCompare(left, op, right string) bool {
	// Try numeric comparison when both sides parse as numbers.
	lf, lok := strconv.ParseFloat(strings.Trim(left, `"`), 64)
	rf, rok := strconv.ParseFloat(strings.Trim(right, `"`), 64)
	if lok == nil && rok == nil {
		switch op {
		case "=":
			return lf == rf
		case "<>", "!=":
			return lf != rf
		case "<":
			return lf < rf
		case ">":
			return lf > rf
		case "<=":
			return lf <= rf
		case ">=":
			return lf >= rf
		}
	}
	// Fall back to string compare (case-insensitive — matches Word).
	l := strings.ToLower(strings.Trim(left, `"`))
	r := strings.ToLower(strings.Trim(right, `"`))
	switch op {
	case "=":
		return l == r
	case "<>", "!=":
		return l != r
	case "<":
		return l < r
	case ">":
		return l > r
	case "<=":
		return l <= r
	case ">=":
		return l >= r
	}
	return false
}

// expandEQField turns a Word 6-style equation field instruction into a
// readable Unicode text approximation. The full EQ grammar is rich
// (\a arrays, \o overstrike, \b bracket modifiers, …); we recognize
// the four constructs that account for ~95% of real-world EQ usage
// and fall through to the raw text otherwise. Used as a fallback path
// for documents that ship EQ fields without a cached result region
// (e.g. programmatically generated reports).
func expandEQField(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ""
	}
	// \f(num,den) — horizontal fraction "num/den".
	if i := strings.Index(arg, `\f(`); i >= 0 {
		body := matchParen(arg[i+len(`\f(`):])
		parts := splitTopLevelCommas(body)
		if len(parts) == 2 {
			return expandEQField(parts[0]) + "/" + expandEQField(parts[1])
		}
	}
	// \r(deg,base) or \r(base) — radical.
	if i := strings.Index(arg, `\r(`); i >= 0 {
		body := matchParen(arg[i+len(`\r(`):])
		parts := splitTopLevelCommas(body)
		switch len(parts) {
		case 1:
			return "√(" + expandEQField(parts[0]) + ")"
		case 2:
			return expandEQField(parts[0]) + "√(" + expandEQField(parts[1]) + ")"
		}
	}
	// \i(lo,hi,expr) — integral with limits.
	if i := strings.Index(arg, `\i(`); i >= 0 {
		body := matchParen(arg[i+len(`\i(`):])
		parts := splitTopLevelCommas(body)
		if len(parts) == 3 {
			return "∫_" + expandEQField(parts[0]) + "^" + expandEQField(parts[1]) + " " + expandEQField(parts[2])
		}
	}
	// \s\up(...) / \s\do(...) — superscript / subscript.
	if i := strings.Index(arg, `\s\up`); i >= 0 {
		j := strings.IndexByte(arg[i+len(`\s\up`):], '(')
		if j >= 0 {
			body := matchParen(arg[i+len(`\s\up`)+j+1:])
			return "^(" + expandEQField(body) + ")"
		}
	}
	if i := strings.Index(arg, `\s\do`); i >= 0 {
		j := strings.IndexByte(arg[i+len(`\s\do`):], '(')
		if j >= 0 {
			body := matchParen(arg[i+len(`\s\do`)+j+1:])
			return "_(" + expandEQField(body) + ")"
		}
	}
	// \b(expr) — bracketed expression. Renders as parentheses; the
	// bracket-type switches (\bc, \lc, \rc) are ignored for the
	// text-only approximation.
	if i := strings.Index(arg, `\b(`); i >= 0 {
		body := matchParen(arg[i+len(`\b(`):])
		return "(" + expandEQField(body) + ")"
	}
	return arg
}

// matchParen returns the substring up to the matching close-paren,
// tracking nesting. The opening "(" is expected to have already been
// consumed by the caller.
func matchParen(s string) string {
	depth := 1
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// splitTopLevelCommas splits on commas that aren't inside parentheses,
// because EQ's nested forms (e.g. \f(1, \f(2,3))) have commas at every
// depth and only the outermost level matters.
func splitTopLevelCommas(s string) []string {
	var out []string
	depth := 0
	last := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[last:i]))
				last = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[last:]))
	return out
}

// refPositionWord returns the word that REF \p substitutes for a bookmark
// cross-reference. Word emits "above" when the bookmark precedes the field
// site and "below" otherwise; when page positions aren't known we default
// to "above" because most authored cross-references point backward.
func refPositionWord(name string, vars fieldVars) string {
	bmPage, ok := vars.bookmarkPages[name]
	if !ok || bmPage <= 0 {
		return "above"
	}
	// vars.page is the page-being-stamped; when the bookmark lands on an
	// earlier page the reference looks "above", on the same page or later
	// it reads "below".
	if vars.page > 0 && bmPage >= vars.page {
		return "below"
	}
	return "above"
}
