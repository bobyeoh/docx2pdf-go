package docx

import (
	"strconv"
	"strings"
)

// parseHTMLAltChunk converts a fragment of HTML (typically from
// w:altChunk's AlternativeFormatInputPart) into a slice of docx Blocks.
//
// We model a small but practical subset:
//
//	p / div / h1..h6 / blockquote / hr — block boundaries
//	strong / b           → bold
//	em / i               → italic
//	u                    → underline
//	s / strike / del     → strikethrough
//	sup / sub            → vertical alignment
//	code / pre / kbd     → monospace via FontFamily="Courier"
//	a href="..."         → hyperlink (LinkURL on the wrapped runs)
//	br                   → soft line break
//	ul / ol + li         → bullet / decimal list with bullets emulated as
//	                       leading "• " or "N. " markers, since we can't
//	                       allocate Numbering definitions here
//	span style="..."     → color: #RRGGBB and font-weight: bold parsed
//
// Unknown tags pass through as transparent wrappers — their text content
// still emerges in the right order. Attributes other than those listed
// are ignored.
func parseHTMLAltChunk(html string, defaults RunProps) []Block {
	t := newHTMLTokenizer(html)
	bld := &htmlBuilder{defaults: defaults, props: defaults}
	for {
		tok, ok := t.next()
		if !ok {
			break
		}
		switch tok.kind {
		case htmlTokText:
			if s := decodeHTMLEntities(tok.text); strings.TrimSpace(s) != "" || bld.inPara {
				bld.appendText(s)
			}
		case htmlTokStart:
			bld.handleStart(tok)
		case htmlTokEnd:
			bld.handleEnd(tok)
		case htmlTokSelfClose:
			bld.handleStart(tok)
			bld.handleEnd(tok)
		}
	}
	bld.finishParagraph()
	return bld.out
}

type htmlTokKind int

const (
	htmlTokText htmlTokKind = iota
	htmlTokStart
	htmlTokEnd
	htmlTokSelfClose
)

type htmlToken struct {
	kind htmlTokKind
	name string
	text string
	attr map[string]string
}

type htmlTokenizer struct {
	s   string
	pos int
}

func newHTMLTokenizer(s string) *htmlTokenizer { return &htmlTokenizer{s: s} }

func (t *htmlTokenizer) next() (htmlToken, bool) {
	if t.pos >= len(t.s) {
		return htmlToken{}, false
	}
	if t.s[t.pos] != '<' {
		// Text run up to the next "<" or end-of-string.
		start := t.pos
		i := strings.IndexByte(t.s[start:], '<')
		if i < 0 {
			t.pos = len(t.s)
			return htmlToken{kind: htmlTokText, text: t.s[start:]}, true
		}
		t.pos = start + i
		return htmlToken{kind: htmlTokText, text: t.s[start : start+i]}, true
	}
	// Skip HTML comments.
	if strings.HasPrefix(t.s[t.pos:], "<!--") {
		end := strings.Index(t.s[t.pos:], "-->")
		if end < 0 {
			t.pos = len(t.s)
		} else {
			t.pos += end + 3
		}
		return t.next()
	}
	// Skip doctype / processing instructions.
	if strings.HasPrefix(t.s[t.pos:], "<!") || strings.HasPrefix(t.s[t.pos:], "<?") {
		end := strings.IndexByte(t.s[t.pos:], '>')
		if end < 0 {
			t.pos = len(t.s)
		} else {
			t.pos += end + 1
		}
		return t.next()
	}
	end := strings.IndexByte(t.s[t.pos:], '>')
	if end < 0 {
		t.pos = len(t.s)
		return htmlToken{}, false
	}
	inner := t.s[t.pos+1 : t.pos+end]
	t.pos += end + 1
	if inner == "" {
		return t.next()
	}
	tok := htmlToken{attr: map[string]string{}}
	if strings.HasPrefix(inner, "/") {
		tok.kind = htmlTokEnd
		tok.name = strings.ToLower(strings.TrimSpace(inner[1:]))
		// Strip any whitespace and attrs in end tag (illegal but tolerated).
		if cut := strings.IndexAny(tok.name, " \t\r\n"); cut > 0 {
			tok.name = tok.name[:cut]
		}
		return tok, true
	}
	selfClose := false
	if strings.HasSuffix(inner, "/") {
		selfClose = true
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	// Tag name is the first whitespace-delimited token.
	name := inner
	rest := ""
	if cut := strings.IndexAny(inner, " \t\r\n"); cut > 0 {
		name = inner[:cut]
		rest = inner[cut+1:]
	}
	tok.name = strings.ToLower(name)
	if rest != "" {
		parseHTMLAttrs(rest, tok.attr)
	}
	// Void elements per HTML spec are always self-closing.
	switch tok.name {
	case "br", "hr", "img", "input", "meta", "link":
		selfClose = true
	}
	if selfClose {
		tok.kind = htmlTokSelfClose
	} else {
		tok.kind = htmlTokStart
	}
	return tok, true
}

func parseHTMLAttrs(s string, out map[string]string) {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return
		}
		eq := strings.IndexByte(s, '=')
		ws := strings.IndexAny(s, " \t\r\n")
		if eq < 0 || (ws >= 0 && ws < eq) {
			// Valueless attribute.
			name := s
			if ws >= 0 {
				name = s[:ws]
				s = s[ws+1:]
			} else {
				s = ""
			}
			out[strings.ToLower(name)] = ""
			continue
		}
		name := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		s = strings.TrimLeft(s, " \t\r\n")
		var val string
		if strings.HasPrefix(s, `"`) || strings.HasPrefix(s, `'`) {
			quote := s[0]
			end := strings.IndexByte(s[1:], quote)
			if end < 0 {
				val = s[1:]
				s = ""
			} else {
				val = s[1 : 1+end]
				s = s[2+end:]
			}
		} else {
			end := strings.IndexAny(s, " \t\r\n")
			if end < 0 {
				val = s
				s = ""
			} else {
				val = s[:end]
				s = s[end+1:]
			}
		}
		out[name] = val
	}
}

// htmlBuilder accumulates runs into the current paragraph, opens/closes
// blocks on tag boundaries, and tracks a stack of run-property overrides.
type htmlBuilder struct {
	defaults RunProps
	props    RunProps
	stack    []RunProps
	runs     []Run
	out      []Block
	inPara   bool

	// Per-paragraph state.
	heading   int // 1..6 when inside h1..h6
	listLvl   int // 0 = no list, 1+ = nesting depth
	listKind  []string
	listIndex []int
	linkURL   string
	// tableStack tracks nested HTML tables. Each entry holds the rows
	// gathered so far and the active row's cells. <tr> opens a new row,
	// <td>/<th> opens a cell whose content is captured by redirecting
	// finishParagraph into the cell's block slice. <table> close commits
	// the collected rows as a docx.Table block.
	tableStack []*htmlTableState
}

// htmlTableState mirrors the in-progress structure of a single HTML table.
type htmlTableState struct {
	rows        []TableRow
	curRow      []TableCell
	curCellBlks []Block
	inRow       bool
	inCell      bool
	// savedOut is the htmlBuilder.out captured at table open time. Cell
	// content is collected into curCellBlks instead of going to out;
	// when the cell closes we move it into the row.
	savedOut []Block
}

func (b *htmlBuilder) pushProps() {
	b.stack = append(b.stack, b.props)
}

func (b *htmlBuilder) popProps() {
	if n := len(b.stack); n > 0 {
		b.props = b.stack[n-1]
		b.stack = b.stack[:n-1]
	}
}

func (b *htmlBuilder) openParagraph() {
	if b.inPara {
		return
	}
	b.inPara = true
	b.runs = b.runs[:0]
}

func (b *htmlBuilder) finishParagraph() {
	if !b.inPara {
		return
	}
	if len(b.runs) == 0 {
		b.inPara = false
		return
	}
	p := Paragraph{Runs: append([]Run(nil), b.runs...)}
	if b.heading > 0 {
		size := b.defaults.FontSize
		if size == 0 {
			size = 11
		}
		switch b.heading {
		case 1:
			size *= 2.0
		case 2:
			size *= 1.6
		case 3:
			size *= 1.4
		case 4:
			size *= 1.2
		case 5:
			size *= 1.1
		default:
			size *= 1.0
		}
		for i := range p.Runs {
			p.Runs[i].Props.Bold = true
			p.Runs[i].Props.FontSize = size
		}
		p.StyleID = "Heading" + strconv.Itoa(b.heading)
	}
	if b.listLvl > 0 {
		// Emulate list markers as a leading run; nest with indent in points.
		kind := "ul"
		if n := len(b.listKind); n > 0 {
			kind = b.listKind[n-1]
		}
		marker := "• "
		if kind == "ol" {
			idx := 1
			if n := len(b.listIndex); n > 0 {
				idx = b.listIndex[n-1]
				b.listIndex[n-1] = idx + 1
			}
			marker = strconv.Itoa(idx) + ". "
		}
		head := Run{Text: marker, Props: b.defaults}
		p.Runs = append([]Run{head}, p.Runs...)
		p.IndentLeftPt = float64(b.listLvl) * 18
		p.IndentFirstLinePt = -12
	}
	// Inside a table cell, paragraphs collect into the cell's block list
	// rather than the document body. The active cell is wherever
	// curCellBlks was last reset by a <td>/<th> open.
	if t := b.curTable(); t != nil && t.inCell {
		t.curCellBlks = append(t.curCellBlks, p)
	} else {
		b.out = append(b.out, p)
	}
	b.runs = b.runs[:0]
	b.inPara = false
}

func (b *htmlBuilder) appendText(s string) {
	if s == "" {
		return
	}
	b.openParagraph()
	run := Run{Text: s, Props: b.props}
	if b.linkURL != "" {
		run.LinkURL = b.linkURL
	}
	b.runs = append(b.runs, run)
}

func (b *htmlBuilder) appendBreak() {
	b.openParagraph()
	b.runs = append(b.runs, Run{IsBreak: true, Props: b.props})
}

func (b *htmlBuilder) handleStart(tok htmlToken) {
	switch tok.name {
	case "p", "div", "blockquote":
		b.finishParagraph()
	case "h1", "h2", "h3", "h4", "h5", "h6":
		b.finishParagraph()
		b.heading = int(tok.name[1] - '0')
	case "br":
		b.appendBreak()
	case "hr":
		b.finishParagraph()
		// Emit an empty paragraph with a bottom border-like effect via a
		// "----" placeholder; the renderer treats this as a thematic break.
		b.out = append(b.out, Paragraph{Runs: []Run{{Text: "————————————————", Props: b.defaults}}})
	case "strong", "b":
		b.pushProps()
		b.props.Bold = true
	case "em", "i":
		b.pushProps()
		b.props.Italic = true
	case "u":
		b.pushProps()
		b.props.Underline = true
	case "s", "strike", "del":
		b.pushProps()
		b.props.Strike = true
	case "sup":
		b.pushProps()
		b.props.VertAlign = "superscript"
	case "sub":
		b.pushProps()
		b.props.VertAlign = "subscript"
	case "code", "kbd", "tt", "samp":
		b.pushProps()
		b.props.FontFamily = "Courier"
	case "pre":
		b.finishParagraph()
		b.pushProps()
		b.props.FontFamily = "Courier"
	case "a":
		b.pushProps()
		b.linkURL = tok.attr["href"]
	case "span", "font":
		b.pushProps()
		if c := tok.attr["color"]; c != "" {
			b.props.Color = strings.TrimPrefix(c, "#")
		}
		if style := tok.attr["style"]; style != "" {
			applyHTMLStyle(style, &b.props)
		}
	case "ul":
		b.finishParagraph()
		b.listLvl++
		b.listKind = append(b.listKind, "ul")
		b.listIndex = append(b.listIndex, 1)
	case "ol":
		b.finishParagraph()
		b.listLvl++
		b.listKind = append(b.listKind, "ol")
		b.listIndex = append(b.listIndex, 1)
	case "li":
		b.finishParagraph()
		// li reopens a paragraph; finish caller will emit the marker.
	case "table":
		b.finishParagraph()
		// Capture out so the table's cells don't leak into the
		// surrounding document. Cell content is redirected into
		// curCellBlks via finishParagraph's table-aware dispatch.
		b.tableStack = append(b.tableStack, &htmlTableState{savedOut: b.out})
		b.out = nil
	case "tr":
		if t := b.curTable(); t != nil {
			b.finishParagraph()
			if t.inRow {
				t.rows = append(t.rows, TableRow{Cells: t.curRow})
				t.curRow = nil
			}
			t.inRow = true
		}
	case "td", "th":
		if t := b.curTable(); t != nil {
			if !t.inRow {
				t.inRow = true
			}
			b.finishParagraph()
			t.inCell = true
			t.curCellBlks = nil
		}
		if tok.name == "th" {
			b.pushProps()
			b.props.Bold = true
		}
	case "img":
		// HTML <img src="..."> from an altChunk doesn't carry a docx
		// relationship, so the image content isn't accessible to the
		// renderer. Surface the alt text (or the basename of the src)
		// as a labeled placeholder run so the position is preserved.
		alt := tok.attr["alt"]
		if alt == "" {
			alt = tok.attr["src"]
		}
		if alt == "" {
			alt = "image"
		}
		b.openParagraph()
		b.runs = append(b.runs, Run{Text: "[" + alt + "]", Props: b.props})
	}
}

// curTable returns the active htmlTableState or nil if no <table> is
// currently open.
func (b *htmlBuilder) curTable() *htmlTableState {
	if n := len(b.tableStack); n > 0 {
		return b.tableStack[n-1]
	}
	return nil
}

func (b *htmlBuilder) handleEnd(tok htmlToken) {
	switch tok.name {
	case "p", "div", "blockquote":
		b.finishParagraph()
	case "h1", "h2", "h3", "h4", "h5", "h6":
		b.finishParagraph()
		b.heading = 0
	case "strong", "b", "em", "i", "u", "s", "strike", "del",
		"sup", "sub", "code", "kbd", "tt", "samp", "span", "font":
		b.popProps()
	case "pre":
		b.finishParagraph()
		b.popProps()
	case "a":
		b.popProps()
		b.linkURL = ""
	case "ul", "ol":
		b.finishParagraph()
		if b.listLvl > 0 {
			b.listLvl--
		}
		if n := len(b.listKind); n > 0 {
			b.listKind = b.listKind[:n-1]
		}
		if n := len(b.listIndex); n > 0 {
			b.listIndex = b.listIndex[:n-1]
		}
	case "li":
		b.finishParagraph()
	case "th":
		b.popProps()
		if t := b.curTable(); t != nil {
			b.finishParagraph()
			t.curRow = append(t.curRow, TableCell{Blocks: t.curCellBlks})
			t.curCellBlks = nil
			t.inCell = false
		}
	case "td":
		if t := b.curTable(); t != nil {
			b.finishParagraph()
			t.curRow = append(t.curRow, TableCell{Blocks: t.curCellBlks})
			t.curCellBlks = nil
			t.inCell = false
		}
	case "tr":
		if t := b.curTable(); t != nil {
			b.finishParagraph()
			if t.inCell {
				t.curRow = append(t.curRow, TableCell{Blocks: t.curCellBlks})
				t.curCellBlks = nil
				t.inCell = false
			}
			t.rows = append(t.rows, TableRow{Cells: t.curRow})
			t.curRow = nil
			t.inRow = false
		}
	case "table":
		if n := len(b.tableStack); n > 0 {
			b.finishParagraph()
			t := b.tableStack[n-1]
			// Flush a row that closed without a </tr>.
			if t.inCell {
				t.curRow = append(t.curRow, TableCell{Blocks: t.curCellBlks})
			}
			if t.inRow && len(t.curRow) > 0 {
				t.rows = append(t.rows, TableRow{Cells: t.curRow})
			}
			b.tableStack = b.tableStack[:n-1]
			// Restore the surrounding flow and commit the table as one
			// block. Computed grid widths default to equal share of the
			// page; renderer falls back to default column sizing when
			// none are set.
			b.out = t.savedOut
			if len(t.rows) > 0 {
				b.out = append(b.out, Table{Rows: t.rows})
			}
		}
	}
}

// applyHTMLStyle applies a tiny subset of inline CSS to p.
//
//	color: #RRGGBB or named (red/black/blue/green) — accepted
//	font-weight: bold
//	font-style: italic
//	text-decoration: underline | line-through
//	background-color: #RRGGBB → Shading
func applyHTMLStyle(style string, p *RunProps) {
	for _, decl := range strings.Split(style, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		name, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		val = strings.TrimSpace(val)
		switch name {
		case "color":
			p.Color = normalizeColorWord(val)
		case "background", "background-color":
			p.Shading = normalizeColorWord(val)
		case "font-weight":
			if val == "bold" || val == "bolder" || val == "700" || val == "800" || val == "900" {
				p.Bold = true
			}
		case "font-style":
			if val == "italic" || val == "oblique" {
				p.Italic = true
			}
		case "text-decoration":
			if strings.Contains(val, "underline") {
				p.Underline = true
			}
			if strings.Contains(val, "line-through") {
				p.Strike = true
			}
		}
	}
}

func normalizeColorWord(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimPrefix(v, "#")
	switch v {
	case "red":
		return "FF0000"
	case "green":
		return "008000"
	case "blue":
		return "0000FF"
	case "yellow":
		return "FFFF00"
	case "white":
		return "FFFFFF"
	case "black":
		return "000000"
	case "gray", "grey":
		return "808080"
	}
	if len(v) == 6 {
		return strings.ToUpper(v)
	}
	if len(v) == 3 {
		// Expand "abc" → "AABBCC".
		var b strings.Builder
		for _, r := range v {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		return strings.ToUpper(b.String())
	}
	return ""
}

// decodeHTMLEntities resolves the common named entities + numeric entities.
func decodeHTMLEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(s[i:], ';')
		if end < 0 || end > 12 {
			b.WriteByte(c)
			i++
			continue
		}
		ent := s[i+1 : i+end]
		i += end + 1
		switch ent {
		case "lt":
			b.WriteByte('<')
		case "gt":
			b.WriteByte('>')
		case "amp":
			b.WriteByte('&')
		case "quot":
			b.WriteByte('"')
		case "apos":
			b.WriteByte('\'')
		case "nbsp":
			b.WriteByte(' ')
		case "copy":
			b.WriteString("©")
		case "reg":
			b.WriteString("®")
		case "trade":
			b.WriteString("™")
		case "hellip":
			b.WriteString("…")
		case "mdash":
			b.WriteString("—")
		case "ndash":
			b.WriteString("–")
		case "rsquo":
			b.WriteString("’")
		case "lsquo":
			b.WriteString("‘")
		case "rdquo":
			b.WriteString("”")
		case "ldquo":
			b.WriteString("“")
		default:
			if strings.HasPrefix(ent, "#") {
				num := ent[1:]
				base := 10
				if strings.HasPrefix(num, "x") || strings.HasPrefix(num, "X") {
					num = num[1:]
					base = 16
				}
				if n, err := strconv.ParseInt(num, base, 32); err == nil && n > 0 {
					b.WriteRune(rune(n))
					continue
				}
			}
			// Unknown entity — emit literally so the source survives.
			b.WriteByte('&')
			b.WriteString(ent)
			b.WriteByte(';')
		}
	}
	return b.String()
}
