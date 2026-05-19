package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
)

const pi180 = math.Pi / 180.0

func cosF(rad float64) float64 { return math.Cos(rad) }
func sinF(rad float64) float64 { return math.Sin(rad) }

// resolveRelTarget resolves a rel target against the source part's
// directory. Targets are expressed relative to the rels file's
// containing part, so word/_rels/document.xml.rels uses "word/" as the
// base. "../customXml/item1.xml" → "customXml/item1.xml".
func resolveRelTarget(base, target string) string {
	target = strings.TrimPrefix(target, "/")
	if strings.HasPrefix(target, "../") || strings.Contains(target, "/../") {
		return path.Clean(path.Join(base, target))
	}
	return base + target
}

// parseDocVars reads w:docVars/w:docVar entries from word/settings.xml into
// the destination map. Called from inside parseSettings's element loop — it
// receives the StartElement and consumes up to the matching EndElement.
func decodeDocVars(dec *xml.Decoder, start xml.StartElement, out map[string]string) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "docVar" {
				name := attr(t, "name")
				val := attr(t, "val")
				if name != "" {
					out[name] = val
				}
				_ = dec.Skip()
			} else {
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// parseCustomProperties reads docProps/custom.xml. Each <property name="X">
// element holds a typed child (vt:lpwstr / vt:i4 / vt:bool / vt:filetime ...)
// whose text content is the value.
func parseCustomProperties(f *zip.File, out map[string]string) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "property" {
			continue
		}
		name := attr(se, "name")
		// The value lives one element deeper, as text inside a vt:* node.
		var val string
		depth := 1
		for depth > 0 {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				depth++
				// Accept the first vt:* descendant as the value source.
				txt, e := readElementText(dec, t)
				if e != nil {
					return e
				}
				if val == "" {
					val = strings.TrimSpace(txt)
				}
				depth-- // readElementText consumed the EndElement
			case xml.EndElement:
				depth--
			}
		}
		if name != "" {
			out[name] = val
		}
	}
}

// extractAltChunkText pulls plain text out of an AlternativeFormatInputPart
// (HTML / XHTML / plain text / RTF). We don't try to lay out the foreign
// markup — we strip tags and return paragraph-broken text.
func extractAltChunkText(f *zip.File) (string, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	s := string(data)
	low := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(low, "{\\rtf"):
		return stripRTF(s), nil
	case strings.HasPrefix(low, "<!doctype html") ||
		strings.HasPrefix(low, "<html") ||
		strings.HasPrefix(low, "<body") ||
		strings.Contains(low, "<p>") ||
		strings.Contains(low, "<div"):
		return stripHTML(s), nil
	}
	return s, nil
}

// stripHTML produces a paragraph-broken plain-text version of an HTML
// fragment. Block-level closes become \n; whitespace collapses; entities
// resolve to the common five.
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	inComment := false
	lastWasSpace := true // suppress leading whitespace
	emit := func(r rune) {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
			return
		}
		b.WriteRune(r)
		lastWasSpace = false
	}
	emitBreak := func() {
		// Collapse a run of breaks to one newline boundary.
		if b.Len() == 0 {
			return
		}
		out := b.String()
		if strings.HasSuffix(out, "\n") {
			return
		}
		b.WriteByte('\n')
		lastWasSpace = true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inComment {
			if c == '-' && i+2 < len(s) && s[i+1] == '-' && s[i+2] == '>' {
				inComment = false
				i += 2
			}
			continue
		}
		if !inTag && c == '<' && i+3 < len(s) && s[i+1] == '!' && s[i+2] == '-' && s[i+3] == '-' {
			inComment = true
			i += 3
			continue
		}
		if c == '<' {
			// Look ahead to spot block-closing tags so we can insert breaks.
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				break
			}
			tag := strings.ToLower(strings.TrimSpace(s[i+1 : i+end]))
			tag = strings.TrimPrefix(tag, "/")
			cut := strings.IndexAny(tag, " \t\r\n")
			if cut > 0 {
				tag = tag[:cut]
			}
			switch tag {
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
				"blockquote", "section", "article", "header", "footer", "pre",
				"table", "thead", "tbody", "tfoot", "ul", "ol":
				emitBreak()
			}
			i += end
			inTag = false
			continue
		}
		if c == '&' {
			semi := strings.IndexByte(s[i:], ';')
			if semi > 0 && semi < 12 {
				name := s[i+1 : i+semi]
				if r, ok := htmlEntity(name); ok {
					emit(r)
					i += semi
					continue
				}
			}
			emit(rune(c))
			continue
		}
		emit(rune(c))
	}
	return strings.TrimSpace(b.String())
}

func htmlEntity(name string) (rune, bool) {
	switch strings.ToLower(name) {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	case "nbsp":
		return ' ', true
	}
	if strings.HasPrefix(name, "#") {
		// Numeric character reference. We don't decode every form; bail.
		return 0, false
	}
	return 0, false
}

// stripRTF returns the text content of an RTF document. Best-effort:
// it removes control words ({\xxx ...}), unescapes \\ \{ \} and \'hh hex
// escapes, and joins everything that's left. Lossy for tables, fonts,
// colors, and most formatting — but the prose survives.
func stripRTF(s string) string {
	var b strings.Builder
	b.Grow(len(s) / 2)
	depth := 0
	skipGroup := -1 // ignore nested groups deeper than this when in \fonttbl etc.
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '{':
			depth++
			i++
		case '}':
			if depth == skipGroup {
				skipGroup = -1
			}
			if depth > 0 {
				depth--
			}
			i++
		case '\\':
			// Control word or escape.
			if i+1 < len(s) {
				n := s[i+1]
				if n == '\\' || n == '{' || n == '}' {
					b.WriteByte(n)
					i += 2
					continue
				}
				if n == '\'' && i+3 < len(s) {
					// Hex escape \'hh
					var hexv byte
					ok := true
					for k := 0; k < 2; k++ {
						hc := s[i+2+k]
						var v byte
						switch {
						case hc >= '0' && hc <= '9':
							v = hc - '0'
						case hc >= 'a' && hc <= 'f':
							v = hc - 'a' + 10
						case hc >= 'A' && hc <= 'F':
							v = hc - 'A' + 10
						default:
							ok = false
						}
						hexv = hexv*16 + v
					}
					if ok {
						b.WriteByte(hexv)
					}
					i += 4
					continue
				}
			}
			// Control word: consume word chars + optional numeric arg + one delimiter.
			j := i + 1
			for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
				j++
			}
			word := s[i+1 : j]
			// Optional numeric argument.
			for j < len(s) && (s[j] == '-' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			// One optional space delimiter is consumed as part of the word.
			if j < len(s) && s[j] == ' ' {
				j++
			}
			// Word-emitting destinations: \par / \line / \tab translate to whitespace.
			switch word {
			case "par", "line", "pard", "sect":
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte('\n')
				}
			case "tab":
				b.WriteByte('\t')
			case "fonttbl", "colortbl", "stylesheet", "info", "pict", "header", "footer":
				// Suppress contents of this group.
				skipGroup = depth
			}
			i = j
		default:
			if skipGroup > 0 && depth >= skipGroup {
				i++
				continue
			}
			if c == '\r' || c == '\n' {
				i++
				continue
			}
			b.WriteByte(c)
			i++
		}
	}
	return strings.TrimSpace(b.String())
}

// loadCustomXMLParts walks the rels map for entries pointing at
// customXml/itemN.xml and reads their bytes into doc.CustomXMLRoots. Also
// detects the bibliography namespace and populates doc.Bibliography.
func loadCustomXMLParts(rels map[string]relEntry, files map[string]*zip.File, doc *Document) {
	for _, e := range rels {
		if !isCustomXMLRel(e.Type) {
			continue
		}
		full := resolveRelTarget("word/", e.Target)
		zf, ok := files[full]
		if !ok {
			zf, ok = files[strings.TrimPrefix(e.Target, "/")]
		}
		if !ok {
			continue
		}
		rc, err := openZipFile(zf)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		// Companion itemPropsN.xml carries the {GUID} that SDTs use to
		// pick a specific data store via w:storeItemID. Path layout is
		// the standard pair: word/customXml/item1.xml ↔ word/customXml/itemProps1.xml.
		storeGuid := readStoreItemGUID(files, full)
		doc.CustomXMLRoots = append(doc.CustomXMLRoots, CustomXMLPart{
			PartName:    full,
			Data:        data,
			StoreItemID: storeGuid,
		})
		// Inspect for bibliography namespace.
		if strings.Contains(string(data), "/officeDocument/2006/bibliography") {
			parseBibliography(data, doc)
		}
		// Inspect for OpenDoPE xpaths namespace.
		if strings.Contains(string(data), "opendope/xpaths") {
			if table := parseOpenDoPEXPaths(data); len(table) > 0 {
				if doc.OpenDoPEXPaths == nil {
					doc.OpenDoPEXPaths = map[string]string{}
				}
				for k, v := range table {
					doc.OpenDoPEXPaths[k] = v
				}
			}
		}
	}
}

func isCustomXMLRel(t string) bool {
	return strings.HasSuffix(t, "/customXml") ||
		strings.HasSuffix(t, "/customXmlProps") ||
		strings.Contains(t, "customXml")
}

// parseBibliography walks a customXml store for <b:Source> entries and
// stores them by tag.
func parseBibliography(data []byte, doc *Document) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "Source" {
			continue
		}
		src := BibSource{}
		// Walk the source's children.
		depth := 1
		var curField string
		var inAuthorName bool
		for depth > 0 {
			tok, err := dec.Token()
			if err != nil {
				return
			}
			switch t := tok.(type) {
			case xml.StartElement:
				depth++
				curField = t.Name.Local
				if curField == "Author" || curField == "Editor" {
					inAuthorName = false
				}
				if curField == "NameList" || curField == "Person" {
					inAuthorName = true
				}
			case xml.CharData:
				v := strings.TrimSpace(string(t))
				if v == "" {
					continue
				}
				switch curField {
				case "Tag":
					src.Tag = v
				case "SourceType":
					src.SourceType = v
				case "Title":
					src.Title = v
				case "Year":
					src.Year = v
				case "Publisher":
					src.Publisher = v
				case "City":
					src.City = v
				case "JournalName":
					src.JournalName = v
				case "Pages":
					src.Pages = v
				case "URL":
					src.URL = v
				case "Last", "First", "Middle":
					if inAuthorName {
						if len(src.Authors) == 0 || curField == "Last" {
							src.Authors = append(src.Authors, v)
						} else {
							// Append given name to most recent surname.
							src.Authors[len(src.Authors)-1] = v + " " + src.Authors[len(src.Authors)-1]
						}
					}
				}
			case xml.EndElement:
				depth--
				if t.Name.Local == "Person" || t.Name.Local == "NameList" {
					inAuthorName = false
				}
			}
		}
		if src.Tag != "" {
			if doc.Bibliography == nil {
				doc.Bibliography = map[string]BibSource{}
			}
			doc.Bibliography[src.Tag] = src
		}
	}
}

// isAltChunkRel matches the AlternativeFormatInputPart relationship type.
func isAltChunkRel(t string) bool {
	return strings.HasSuffix(t, "/aFChunk") ||
		strings.HasSuffix(t, "/altChunk") ||
		strings.HasSuffix(t, "/afChunk")
}

// loadAltChunks reads every AlternativeFormatInputPart referenced in rels
// and parses its content into a Block tree stored under doc.AltChunks keyed
// by rId. HTML content is parsed by parseHTMLAltChunk so bold/italic/
// headings/lists/links survive into the body; RTF and plain text fall back
// to flat paragraphs.
func loadAltChunks(rels map[string]relEntry, files map[string]*zip.File, doc *Document) {
	for rid, e := range rels {
		if !isAltChunkRel(e.Type) {
			continue
		}
		full := resolveRelTarget("word/", e.Target)
		zf, ok := files[full]
		if !ok {
			zf, ok = files[strings.TrimPrefix(e.Target, "/")]
		}
		if !ok {
			continue
		}
		rc, err := openZipFile(zf)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		s := string(data)
		low := strings.ToLower(strings.TrimSpace(s))
		var blocks []Block
		switch {
		case strings.HasPrefix(low, "{\\rtf"):
			// RTF: strip to plain text — we don't model RTF semantics here.
			txt := stripRTF(s)
			blocks = flatTextToParagraphs(txt, doc.Defaults)
		case isMHTMLPayload(low):
			// MHT (RFC 2557 multipart/related): pull out the first
			// text/html part and parse THAT as HTML. Other parts (images,
			// CSS) are dropped — the renderer doesn't reach into the MHT
			// resources, but the visible text content survives.
			if html, ok := extractMHTMLHTMLPart(s); ok {
				blocks = parseHTMLAltChunk(html, doc.Defaults)
			} else {
				blocks = flatTextToParagraphs(s, doc.Defaults)
			}
		case strings.HasPrefix(low, "<?xml") && (strings.Contains(low, "xhtml") || strings.Contains(low, "<html")):
			// XHTML: same HTML parser; the XML declaration prefix is fine.
			blocks = parseHTMLAltChunk(s, doc.Defaults)
		case strings.HasPrefix(low, "<!doctype html") ||
			strings.HasPrefix(low, "<html") ||
			strings.HasPrefix(low, "<body") ||
			strings.Contains(low, "<p>") ||
			strings.Contains(low, "<p ") ||
			strings.Contains(low, "<div") ||
			strings.Contains(low, "<h1") ||
			strings.Contains(low, "<h2") ||
			strings.Contains(low, "<ul") ||
			strings.Contains(low, "<ol"):
			blocks = parseHTMLAltChunk(s, doc.Defaults)
		default:
			blocks = flatTextToParagraphs(s, doc.Defaults)
		}
		if len(blocks) == 0 {
			continue
		}
		if doc.AltChunks == nil {
			doc.AltChunks = map[string][]Block{}
		}
		doc.AltChunks[rid] = blocks
	}
}

// isMHTMLPayload sniffs the first few hundred bytes for the standard MHT
// preamble. We allow either MIME-Version or Content-Type header on the
// first non-blank line, with multipart/related being the typical shape.
func isMHTMLPayload(low string) bool {
	if strings.HasPrefix(low, "mime-version:") {
		return strings.Contains(low, "multipart/")
	}
	if strings.HasPrefix(low, "content-type:") && strings.Contains(low, "multipart/related") {
		return true
	}
	// Some exporters emit a blank first line then the headers.
	if strings.Contains(low[:min(len(low), 512)], "content-type: multipart/related") {
		return true
	}
	return false
}

// extractMHTMLHTMLPart finds the first part of the MHT envelope whose
// Content-Type is text/html and returns its decoded body. Content-Transfer-
// Encoding values "quoted-printable", "base64", and "7bit"/"8bit" are all
// supported; unknown encodings are returned as-is.
func extractMHTMLHTMLPart(s string) (string, bool) {
	// Find the boundary token from the outer Content-Type header.
	boundary := ""
	if i := strings.Index(strings.ToLower(s), "boundary="); i >= 0 {
		rest := s[i+9:]
		// Strip optional quotes.
		end := len(rest)
		for j, c := range rest {
			if c == '"' || c == ';' || c == '\r' || c == '\n' || c == ' ' {
				end = j
				break
			}
		}
		boundary = strings.Trim(rest[:end], "\"")
	}
	if boundary == "" {
		return "", false
	}
	sep := "--" + boundary
	parts := strings.Split(s, sep)
	for _, p := range parts {
		// Each part begins with headers, then a blank line, then the body.
		split := strings.Index(p, "\r\n\r\n")
		if split < 0 {
			split = strings.Index(p, "\n\n")
			if split < 0 {
				continue
			}
		}
		headers := p[:split]
		bodyStart := split
		if strings.HasPrefix(p[split:], "\r\n\r\n") {
			bodyStart += 4
		} else {
			bodyStart += 2
		}
		body := p[bodyStart:]
		lowHeaders := strings.ToLower(headers)
		if !strings.Contains(lowHeaders, "text/html") {
			continue
		}
		// Decode body per Content-Transfer-Encoding.
		switch {
		case strings.Contains(lowHeaders, "content-transfer-encoding: quoted-printable"):
			body = decodeQuotedPrintable(body)
		case strings.Contains(lowHeaders, "content-transfer-encoding: base64"):
			body = string(decodeBase64MHT(body))
		}
		return body, true
	}
	return "", false
}

// decodeQuotedPrintable does a minimal Quoted-Printable decode sufficient
// for the email-bodied HTML chunks Word writes. Soft line breaks (=\r?\n)
// are dropped; =HH escapes are converted to bytes; the rest passes through.
func decodeQuotedPrintable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' && i+1 < len(s) {
			// Soft line break.
			if s[i+1] == '\n' {
				i++
				continue
			}
			if s[i+1] == '\r' && i+2 < len(s) && s[i+2] == '\n' {
				i += 2
				continue
			}
			if i+2 < len(s) {
				if v, err := strconv.ParseInt(s[i+1:i+3], 16, 32); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// decodeBase64MHT decodes the body's base64-encoded contents, ignoring
// whitespace and any final '=' padding. Reuses the offcrypto base64
// decoder so MHT/altchunk parsing doesn't pull in encoding/base64.
func decodeBase64MHT(s string) []byte {
	return decodeBase64(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// flatTextToParagraphs turns a newline-delimited string into Paragraph
// blocks, one per non-empty line.
func flatTextToParagraphs(s string, defaults RunProps) []Block {
	var blocks []Block
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		blocks = append(blocks, Paragraph{
			Runs: []Run{{Text: line, Props: defaults}},
		})
	}
	return blocks
}

// resolveXPathWithPrefixes is the prefix-aware variant of resolveXPath:
// when prefixes is non-empty, each step's prefix is resolved to a URI and
// the walker matches the element's namespace URI against it. This is what
// Word's <w:dataBinding w:prefixMappings> compels — otherwise distinct
// namespaces with overlapping local names would alias.
//
// An empty prefixes map falls back to legacy name-only behavior.
func resolveXPathWithPrefixes(parts []CustomXMLPart, xpath string, prefixes map[string]string) (string, bool) {
	xpath = strings.TrimSpace(xpath)
	if xpath == "" {
		return "", false
	}
	// Top-level function call? Delegate to the function evaluator so
	// concat() / substring() / sum() / etc. work with namespace-prefixed
	// inner expressions.
	if v, ok, isFn := evalXPathFunction(parts, xpath, prefixes); isFn {
		return v, ok
	}
	attrSel := ""
	if i := strings.LastIndex(xpath, "/@"); i >= 0 {
		attrSel = xpath[i+2:]
		if j := strings.IndexAny(attrSel, "[/"); j >= 0 {
			attrSel = attrSel[:j]
		}
		xpath = xpath[:i]
	}
	if attrSel != "" {
		if j := strings.IndexByte(attrSel, ':'); j >= 0 {
			attrSel = attrSel[j+1:]
		}
	}
	rawSteps := strings.Split(strings.TrimPrefix(xpath, "/"), "/")
	steps := make([]xpathStep, 0, len(rawSteps))
	for _, s := range rawSteps {
		st := parseXPathStep(s)
		if st.name == "" {
			continue
		}
		steps = append(steps, st)
	}
	if len(steps) == 0 && attrSel == "" {
		return "", false
	}
	for _, part := range parts {
		if v, ok := walkXMLForPathWithPrefixes(part.Data, steps, attrSel, prefixes); ok {
			return v, true
		}
	}
	return "", false
}

// resolveXPath does a very small subset of XPath against a custom-xml store.
// Supports:
//   - "/ns:Root/ns:Foo/ns:Bar"             element-suffix match → first text
//   - "/ns:Root/ns:Foo/@attr"              attribute selector → that attr's value
//   - "/ns:Root/ns:Foo[2]"                 positional predicate (1-based)
//   - "/ns:Root/ns:Foo[@k='v']/ns:Bar"     attribute-equality predicate
//
// Namespace prefixes are stripped throughout — the resolver is name-only
// because Word's stored XPaths often reference custom prefixes that aren't
// declared in the same scope as the data. Returns the first matching text.
func resolveXPath(parts []CustomXMLPart, xpath string) (string, bool) {
	xpath = strings.TrimSpace(xpath)
	if xpath == "" {
		return "", false
	}
	// XPath 1.0 function wrappers we recognise at the top of an expression.
	// Functions all follow `name(args)` with the closing paren at end of
	// string. count() / sum() take node sets (single-arg); substring(),
	// substring-before(), substring-after() take strings and indices;
	// concat() takes 2+ strings; normalize-space() / translate() / number()
	// / boolean() / floor() / ceiling() / round() take a single argument.
	if v, ok, isFn := evalXPathFunction(parts, xpath, nil); isFn {
		return v, ok
	}
	// Attribute selector at the tail of the path: "/foo/bar/@attr".
	attrSel := ""
	if i := strings.LastIndex(xpath, "/@"); i >= 0 {
		attrSel = xpath[i+2:]
		if j := strings.IndexAny(attrSel, "[/"); j >= 0 {
			attrSel = attrSel[:j]
		}
		xpath = xpath[:i]
	}
	if attrSel != "" {
		if j := strings.IndexByte(attrSel, ':'); j >= 0 {
			attrSel = attrSel[j+1:]
		}
	}
	rawSteps := strings.Split(strings.TrimPrefix(xpath, "/"), "/")
	steps := make([]xpathStep, 0, len(rawSteps))
	for _, s := range rawSteps {
		st := parseXPathStep(s)
		if st.name == "" {
			continue
		}
		steps = append(steps, st)
	}
	if len(steps) == 0 && attrSel == "" {
		return "", false
	}
	for _, part := range parts {
		if v, ok := walkXMLForPath(part.Data, steps, attrSel); ok {
			return v, true
		}
	}
	return "", false
}

// xpathStep is one parsed segment of an XPath: tag name plus an optional
// predicate. We currently honor either a positional "[N]" predicate or a
// single attribute-equality "[@a='v']" predicate; other forms are ignored.
type xpathStep struct {
	name string
	// prefix carries the namespace prefix declared on this step
	// ("ns0" in "ns0:Root"). The walker resolves it against the
	// prefixMappings map passed alongside, then compares URIs.
	// Empty prefix is taken as "match any namespace" (legacy behavior).
	prefix string
	// position > 0 selects the Nth match (1-based). 0 means "any".
	position int
	// attrName/attrVal are non-empty for attribute-equality predicates.
	attrName, attrVal string
	// childName/childVal are non-empty for child-equality predicates
	// (e.g. /Items/Item[Name='Foo']/Price). Compared against the child
	// element's inner text. Prefix-stripped — namespace match is
	// best-effort by local name only.
	childName, childVal string
	// andAttrName/andAttrVal carry a SECOND attribute-equality predicate
	// joined with `and`. Example: [@a='x' and @b='y']. When both are
	// non-empty, the walker requires both equalities. Supports the
	// common OpenDoPE pattern where one custom-XML element disambiguates
	// across two attributes (e.g. customer id + version).
	andAttrName, andAttrVal string
	// conds is the parsed predicate as a list of boolean clauses joined
	// with `and`. When non-empty it supersedes the attrName/childName
	// flat fields for compound conditions involving XPath functions
	// like not() / contains() / starts-with() / position() / last().
	// Each clause is evaluated against the candidate element; the
	// element passes only when all clauses are true.
	conds []xpathCond
}

// xpathCond is one boolean clause in a predicate.
type xpathCond struct {
	// kind identifies the clause shape. Set on parse:
	//   "attr"        — attribute equality (Name = "@k", Val = "v")
	//   "child"       — child-element equality (Name = "Tag", Val = "v")
	//   "contains"    — contains(@k, "needle") (Name = "@k", Val = "needle")
	//   "starts-with" — starts-with(@k, "prefix") (Name = "@k", Val = "prefix")
	//   "position-eq" — position()=N (PosN = N)
	//   "last"        — position()=last() (no args)
	kind    string
	Name    string
	Val     string
	PosN    int
	Negated bool
}

func parseXPathStep(s string) xpathStep {
	var step xpathStep
	if i := strings.IndexByte(s, ':'); i >= 0 {
		step.prefix = s[:i]
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, '['); i >= 0 {
		pred := s[i+1:]
		s = s[:i]
		pred = strings.TrimSuffix(pred, "]")
		// Fast path: bare integer predicate [N].
		if n, err := strconv.Atoi(strings.TrimSpace(pred)); err == nil && n > 0 {
			step.position = n
		} else {
			// Split on " and " into clauses; each clause becomes one
			// xpathCond. Legacy attrName/attrVal/childName slots are
			// also filled when applicable so existing walker paths
			// remain effective for simple cases.
			parts := splitPredicateAnd(pred)
			for k, part := range parts {
				cond, ok := parseXPathCond(strings.TrimSpace(part))
				if !ok {
					continue
				}
				step.conds = append(step.conds, cond)
				if k == 0 {
					switch cond.kind {
					case "attr":
						if !cond.Negated {
							step.attrName = cond.Name
							step.attrVal = cond.Val
						}
					case "child":
						if !cond.Negated {
							step.childName = cond.Name
							step.childVal = cond.Val
						}
					}
				} else if k == 1 && cond.kind == "attr" && !cond.Negated {
					step.andAttrName = cond.Name
					step.andAttrVal = cond.Val
				}
			}
		}
	}
	step.name = s
	return step
}

// parseXPathCond parses one boolean clause within a predicate. Supported
// shapes (case-insensitive function names; quote either ' or "):
//
//	@k = 'v'
//	Tag = 'v'                   (child-element equality)
//	contains(@k, 'needle')
//	starts-with(@k, 'prefix')
//	position() = N
//	last()
//	not(<inner-clause>)         (any of the above wrapped)
//
// Returns ok=false when the clause shape isn't recognized; the walker
// then treats the entire predicate as "always passes" (legacy behavior).
func parseXPathCond(s string) (xpathCond, bool) {
	c := xpathCond{}
	if strings.HasPrefix(strings.ToLower(s), "not(") && strings.HasSuffix(s, ")") {
		inner, ok := parseXPathCond(strings.TrimSpace(s[4 : len(s)-1]))
		if !ok {
			return c, false
		}
		inner.Negated = !inner.Negated
		return inner, true
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "contains("):
		args := strings.TrimSuffix(s[len("contains("):], ")")
		name, val, ok := splitFnArgs(args)
		if !ok || !strings.HasPrefix(name, "@") {
			return c, false
		}
		c.kind = "contains"
		c.Name = strings.TrimPrefix(name, "@")
		c.Val = val
		return c, true
	case strings.HasPrefix(low, "starts-with("):
		args := strings.TrimSuffix(s[len("starts-with("):], ")")
		name, val, ok := splitFnArgs(args)
		if !ok || !strings.HasPrefix(name, "@") {
			return c, false
		}
		c.kind = "starts-with"
		c.Name = strings.TrimPrefix(name, "@")
		c.Val = val
		return c, true
	case strings.HasPrefix(low, "position()"):
		rest := strings.TrimSpace(s[len("position()"):])
		if strings.HasPrefix(rest, "=") {
			n, err := strconv.Atoi(strings.TrimSpace(rest[1:]))
			if err == nil {
				c.kind = "position-eq"
				c.PosN = n
				return c, true
			}
		}
		return c, false
	case low == "last()" || strings.HasPrefix(low, "last()"):
		c.kind = "last"
		return c, true
	case strings.HasPrefix(s, "@"):
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return c, false
		}
		name := strings.TrimSpace(s[1:eq])
		val := strings.TrimSpace(s[eq+1:])
		val = strings.Trim(val, `'"`)
		c.kind = "attr"
		c.Name = name
		c.Val = val
		return c, true
	default:
		// Child-element equality predicate: Tag = 'v' (optionally
		// namespaced).
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return c, false
		}
		name := strings.TrimSpace(s[:eq])
		if ci := strings.IndexByte(name, ':'); ci >= 0 {
			name = name[ci+1:]
		}
		val := strings.TrimSpace(s[eq+1:])
		val = strings.Trim(val, `'"`)
		c.kind = "child"
		c.Name = name
		c.Val = val
		return c, true
	}
}

// splitFnArgs splits a two-argument xpath function call's body. Returns
// the two trimmed args (with surrounding quotes stripped from arg 2)
// and ok=false on shape mismatch.
func splitFnArgs(s string) (string, string, bool) {
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				a := strings.TrimSpace(s[:i])
				b := strings.TrimSpace(s[i+1:])
				b = strings.Trim(b, `'"`)
				return a, b, true
			}
		}
	}
	return "", "", false
}

// splitPredicateAnd splits a predicate body on the literal " and "
// connector (case-insensitive). Quoted segments are respected — `and`
// inside single or double quotes is not treated as a separator. Returns
// the original string as a 1-element slice when no separator is found.
func splitPredicateAnd(s string) []string {
	if !strings.Contains(strings.ToLower(s), " and ") {
		return []string{s}
	}
	parts := []string{}
	inQuote := byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			inQuote = c
			continue
		}
		if i+5 <= len(s) && strings.EqualFold(s[i:i+5], " and ") {
			parts = append(parts, s[start:i])
			start = i + 5
			i += 4
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parsePrefixMappings unpacks Word's <w:dataBinding w:prefixMappings="…">
// attribute. The value is a space-delimited list of `xmlns:prefix='uri'`
// declarations. The legacy form `prefix=uri` (no xmlns prefix, no quotes)
// is also accepted for older docs.
//
// Returns a map prefix→URI; empty when input is empty/malformed.
func parsePrefixMappings(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	// Find each "xmlns:NAME=" prefix → quoted URI in the string.
	for len(s) > 0 {
		i := strings.Index(s, "xmlns:")
		if i < 0 {
			break
		}
		s = s[i+len("xmlns:"):]
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		prefix := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		// Quoted URI: ' or " — accept either.
		var uri string
		switch {
		case strings.HasPrefix(s, `'`):
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				return out
			}
			uri = s[1 : 1+end]
			s = s[2+end:]
		case strings.HasPrefix(s, `"`):
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				return out
			}
			uri = s[1 : 1+end]
			s = s[2+end:]
		default:
			// Bare URI up to next whitespace.
			end := strings.IndexAny(s, " \t")
			if end < 0 {
				end = len(s)
			}
			uri = s[:end]
			s = s[end:]
		}
		if prefix != "" {
			out[prefix] = uri
		}
	}
	return out
}

// xpathFrame is one element on the walker's stack.
type xpathFrame struct {
	name     string
	uri      string // namespace URI of this element
	attrs    map[string]string
	childPos map[string]int // how many times each child tag has been seen
}

// resolveXPathInStore is the store-scoped variant of resolveXPath: when
// storeItemID is non-empty, only the matching part is searched; when
// empty, every part is searched (legacy behavior). Returns the value
// and a found flag.
func resolveXPathInStore(parts []CustomXMLPart, storeItemID, xpath string) (string, bool) {
	return resolveXPathInStoreWithPrefixes(parts, storeItemID, xpath, nil)
}

// resolveXPathInStoreWithPrefixes is the namespace-aware variant. When
// prefixes is non-empty, namespace prefixes embedded in xpath are matched
// against element URIs rather than dropped — required for documents that
// bind multiple custom-xml stores carrying overlapping local names.
func resolveXPathInStoreWithPrefixes(parts []CustomXMLPart, storeItemID, xpath string, prefixes map[string]string) (string, bool) {
	resolve := func(p []CustomXMLPart) (string, bool) {
		if len(prefixes) > 0 {
			return resolveXPathWithPrefixes(p, xpath, prefixes)
		}
		return resolveXPath(p, xpath)
	}
	if storeItemID == "" {
		return resolve(parts)
	}
	guid := strings.Trim(storeItemID, "{}")
	for _, p := range parts {
		pg := strings.Trim(p.StoreItemID, "{}")
		if !strings.EqualFold(pg, guid) {
			continue
		}
		if v, ok := resolve([]CustomXMLPart{p}); ok {
			return v, true
		}
		return "", false
	}
	// Fallback: GUID not found among loaded stores (older docs sometimes
	// drop the itemProps file). Search every store as a recovery.
	return resolve(parts)
}

// readStoreItemGUID locates customXml/itemPropsN.xml that pairs with
// itemDataPath and pulls the ds:itemID GUID from its
// <ds:datastoreItem> root. Returns "" when the props file is absent.
func readStoreItemGUID(files map[string]*zip.File, itemDataPath string) string {
	propsPath := storePropsCompanion(itemDataPath)
	zf, ok := files[propsPath]
	if !ok {
		return ""
	}
	rc, err := openZipFile(zf)
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return ""
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "datastoreItem" {
				for _, a := range se.Attr {
					if a.Name.Local == "itemID" {
						return strings.TrimSpace(a.Value)
					}
				}
				return ""
			}
		}
	}
}

// storePropsCompanion turns "word/customXml/item3.xml" into
// "word/customXml/itemProps3.xml". For non-conforming names it returns
// the original path unchanged (so the lookup harmlessly misses).
func storePropsCompanion(itemPath string) string {
	dir := itemPath
	base := ""
	if i := strings.LastIndex(itemPath, "/"); i >= 0 {
		dir = itemPath[:i+1]
		base = itemPath[i+1:]
	}
	if !strings.HasPrefix(base, "item") {
		return itemPath
	}
	return dir + "itemProps" + strings.TrimPrefix(base, "item")
}

// evalXPathFunction recognises an XPath 1.0 function-form expression
// `name(args)` and computes the result. Returns (value, ok, true) when
// the expression IS a function call; (_, _, false) when it isn't and the
// caller should fall through to normal path resolution.
//
// prefixes is optional and only consulted by inner path resolutions; pass
// nil for the default (no namespace remapping).
func evalXPathFunction(parts []CustomXMLPart, expr string, prefixes map[string]string) (string, bool, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasSuffix(expr, ")") {
		return "", false, false
	}
	lp := strings.IndexByte(expr, '(')
	if lp <= 0 {
		return "", false, false
	}
	name := strings.ToLower(strings.TrimSpace(expr[:lp]))
	body := strings.TrimSpace(expr[lp+1 : len(expr)-1])
	// Only treat as a function call when the name looks like one we know.
	// This avoids accidentally swallowing expressions that contain '('
	// inside a predicate.
	switch name {
	case "count", "sum", "substring", "substring-before", "substring-after",
		"concat", "normalize-space", "translate", "number", "boolean", "not",
		"floor", "ceiling", "round", "string", "string-length", "local-name", "name":
	default:
		return "", false, false
	}
	args := splitXPathArgs(body)
	// Helper to resolve an arg to a string. If the arg is a quoted literal
	// it's used as-is; numeric literals also pass through; otherwise it's
	// treated as a sub-expression which may itself be a function or path.
	resolveArg := func(a string) (string, bool) {
		a = strings.TrimSpace(a)
		if a == "" {
			return "", false
		}
		if (strings.HasPrefix(a, "'") && strings.HasSuffix(a, "'")) ||
			(strings.HasPrefix(a, "\"") && strings.HasSuffix(a, "\"")) {
			return a[1 : len(a)-1], true
		}
		if _, err := strconv.ParseFloat(a, 64); err == nil {
			return a, true
		}
		if v, ok, isFn := evalXPathFunction(parts, a, prefixes); isFn {
			return v, ok
		}
		// Path resolution: prefer the prefixed path resolver when a map is
		// supplied so OpenDoPE expressions with prefixMappings still work.
		if prefixes != nil {
			return resolveXPathWithPrefixes(parts, a, prefixes)
		}
		return resolveXPath(parts, a)
	}
	switch name {
	case "count":
		n := countXPathMatches(parts, body)
		return strconv.Itoa(n), true, true
	case "sum":
		// Sum the numeric values of all element matches for the inner path.
		// We approximate by counting matches of the (single) path, then
		// re-fetching each via positional predicate.
		n := countXPathMatches(parts, body)
		var total float64
		for i := 1; i <= n; i++ {
			indexed := injectPositionalPredicate(body, i)
			if v, ok := resolveXPath(parts, indexed); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					total += f
				}
			}
		}
		return formatXPathNumber(total), true, true
	case "substring":
		if len(args) < 2 {
			return "", false, true
		}
		s, _ := resolveArg(args[0])
		startF, _ := strconv.ParseFloat(strings.TrimSpace(args[1]), 64)
		start := int(math.Round(startF))
		// XPath 1.0 substring is 1-based; clamp left edge.
		if start < 1 {
			start = 1
		}
		runes := []rune(s)
		if start-1 >= len(runes) {
			return "", true, true
		}
		end := len(runes)
		if len(args) >= 3 {
			lenF, _ := strconv.ParseFloat(strings.TrimSpace(args[2]), 64)
			end = start - 1 + int(math.Round(lenF))
			if end > len(runes) {
				end = len(runes)
			}
			if end < start-1 {
				end = start - 1
			}
		}
		return string(runes[start-1 : end]), true, true
	case "substring-before":
		if len(args) != 2 {
			return "", false, true
		}
		s, _ := resolveArg(args[0])
		sep, _ := resolveArg(args[1])
		if sep == "" {
			return "", true, true
		}
		if i := strings.Index(s, sep); i >= 0 {
			return s[:i], true, true
		}
		return "", true, true
	case "substring-after":
		if len(args) != 2 {
			return "", false, true
		}
		s, _ := resolveArg(args[0])
		sep, _ := resolveArg(args[1])
		if sep == "" {
			return s, true, true
		}
		if i := strings.Index(s, sep); i >= 0 {
			return s[i+len(sep):], true, true
		}
		return "", true, true
	case "concat":
		var b strings.Builder
		for _, a := range args {
			v, _ := resolveArg(a)
			b.WriteString(v)
		}
		return b.String(), true, true
	case "normalize-space":
		var s string
		if len(args) == 0 {
			s = ""
		} else {
			s, _ = resolveArg(args[0])
		}
		return strings.Join(strings.Fields(s), " "), true, true
	case "translate":
		if len(args) != 3 {
			return "", false, true
		}
		s, _ := resolveArg(args[0])
		from, _ := resolveArg(args[1])
		to, _ := resolveArg(args[2])
		fromR := []rune(from)
		toR := []rune(to)
		var b strings.Builder
		for _, c := range s {
			idx := -1
			for k, fc := range fromR {
				if fc == c {
					idx = k
					break
				}
			}
			if idx < 0 {
				b.WriteRune(c)
				continue
			}
			if idx < len(toR) {
				b.WriteRune(toR[idx])
			}
			// idx >= len(toR) → character is removed.
		}
		return b.String(), true, true
	case "number":
		if len(args) == 0 {
			return "NaN", false, true
		}
		v, ok := resolveArg(args[0])
		if !ok {
			return "NaN", false, true
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return "NaN", false, true
		}
		return formatXPathNumber(f), true, true
	case "boolean":
		if len(args) == 0 {
			return "false", true, true
		}
		v, _ := resolveArg(args[0])
		if v == "" || v == "0" || strings.EqualFold(v, "false") {
			return "false", true, true
		}
		return "true", true, true
	case "not":
		if len(args) == 0 {
			return "true", true, true
		}
		v, _ := resolveArg(args[0])
		if v == "" || v == "0" || strings.EqualFold(v, "false") {
			return "true", true, true
		}
		return "false", true, true
	case "floor":
		if len(args) == 0 {
			return "NaN", false, true
		}
		v, _ := resolveArg(args[0])
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return "NaN", false, true
		}
		return formatXPathNumber(math.Floor(f)), true, true
	case "ceiling":
		if len(args) == 0 {
			return "NaN", false, true
		}
		v, _ := resolveArg(args[0])
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return "NaN", false, true
		}
		return formatXPathNumber(math.Ceil(f)), true, true
	case "round":
		if len(args) == 0 {
			return "NaN", false, true
		}
		v, _ := resolveArg(args[0])
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return "NaN", false, true
		}
		return formatXPathNumber(math.Round(f)), true, true
	case "string":
		if len(args) == 0 {
			return "", true, true
		}
		v, _ := resolveArg(args[0])
		return v, true, true
	case "string-length":
		if len(args) == 0 {
			return "0", true, true
		}
		v, _ := resolveArg(args[0])
		return strconv.Itoa(len([]rune(v))), true, true
	case "local-name", "name":
		// Without a real node model we can only return the trailing step
		// of the path argument. Word documents rarely use these but the
		// approximation keeps templates that probe element names alive.
		if len(args) == 0 {
			return "", true, true
		}
		a := strings.TrimSpace(args[0])
		a = strings.TrimSuffix(a, "/")
		if i := strings.LastIndexByte(a, '/'); i >= 0 {
			a = a[i+1:]
		}
		if name == "local-name" {
			if i := strings.IndexByte(a, ':'); i >= 0 {
				a = a[i+1:]
			}
		}
		if i := strings.IndexAny(a, "[@"); i >= 0 {
			a = a[:i]
		}
		return a, true, true
	}
	return "", false, false
}

// splitXPathArgs splits a comma-separated arg list, respecting nesting in
// parentheses and quoted literals. Empty input yields a single empty arg.
func splitXPathArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	depth := 0
	quote := byte(0)
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
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

// injectPositionalPredicate appends a `[N]` predicate to the final step
// of an XPath. Used by sum() to enumerate matches one at a time.
func injectPositionalPredicate(xpath string, n int) string {
	xpath = strings.TrimSpace(xpath)
	// If the path already ends in /@attr, the predicate goes on the
	// element BEFORE the attribute selector.
	if i := strings.LastIndex(xpath, "/@"); i >= 0 {
		return xpath[:i] + "[" + strconv.Itoa(n) + "]" + xpath[i:]
	}
	return xpath + "[" + strconv.Itoa(n) + "]"
}

// formatXPathNumber matches XPath 1.0 number-to-string: integers print
// without a decimal point; non-integers preserve enough precision for
// document interpolation without trailing zeros.
func formatXPathNumber(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if f == math.Trunc(f) && !math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// countXPathMatches returns how many element-matches an XPath has across
// all custom XML stores. Used by the OpenDoPE repeat resolver so it
// doesn't have to probe positional predicates one-at-a-time. Matches
// are counted by *element start* — a path that ends with /@attr is
// counted by attribute presence on each match.
func countXPathMatches(parts []CustomXMLPart, xpath string) int {
	xpath = strings.TrimSpace(xpath)
	if xpath == "" {
		return 0
	}
	attrSel := ""
	if i := strings.LastIndex(xpath, "/@"); i >= 0 {
		attrSel = xpath[i+2:]
		if j := strings.IndexAny(attrSel, "[/"); j >= 0 {
			attrSel = attrSel[:j]
		}
		xpath = xpath[:i]
	}
	if attrSel != "" {
		if j := strings.IndexByte(attrSel, ':'); j >= 0 {
			attrSel = attrSel[j+1:]
		}
	}
	rawSteps := strings.Split(strings.TrimPrefix(xpath, "/"), "/")
	steps := make([]xpathStep, 0, len(rawSteps))
	for _, s := range rawSteps {
		st := parseXPathStep(s)
		if st.name == "" {
			continue
		}
		steps = append(steps, st)
	}
	if len(steps) == 0 {
		return 0
	}
	total := 0
	for _, part := range parts {
		total += countMatchesInPart(part.Data, steps, attrSel)
	}
	return total
}

func countMatchesInPart(data []byte, steps []xpathStep, attrSel string) int {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []xpathFrame
	count := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return count
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) > 0 {
				if stack[len(stack)-1].childPos == nil {
					stack[len(stack)-1].childPos = map[string]int{}
				}
				stack[len(stack)-1].childPos[t.Name.Local]++
			}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			stack = append(stack, xpathFrame{name: t.Name.Local, attrs: attrs})
			if matchSuffixWithPredicates(stack, steps) {
				if attrSel != "" {
					if _, ok := attrs[attrSel]; ok {
						count++
					}
				} else {
					count++
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

// walkXMLForPath streams through data and returns the first text node that
// satisfies a suffix match of steps. When attrSel is non-empty the walker
// instead returns the named attribute's value on the deepest matching
// element.
func walkXMLForPath(data []byte, steps []xpathStep, attrSel string) (string, bool) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []xpathFrame
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// Increment the parent's child-count for this tag (used by
			// positional predicates on the upcoming frame).
			if len(stack) > 0 {
				if stack[len(stack)-1].childPos == nil {
					stack[len(stack)-1].childPos = map[string]int{}
				}
				stack[len(stack)-1].childPos[t.Name.Local]++
			}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			stack = append(stack, xpathFrame{name: t.Name.Local, attrs: attrs})
			if attrSel != "" && matchSuffixWithPredicates(stack, steps) {
				if v, ok := attrs[attrSel]; ok {
					return strings.TrimSpace(v), true
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if attrSel != "" {
				continue
			}
			if matchSuffixWithPredicates(stack, steps) {
				v := strings.TrimSpace(string(t))
				if v != "" {
					return v, true
				}
			}
		}
	}
}

// walkXMLForPathWithPrefixes is walkXMLForPath plus URI matching when the
// step carries a namespace prefix that resolves through `prefixes`.
//
// Empty prefixes map = legacy local-name matching for every step (callers
// who want namespace-strict matching must populate prefixes from
// w:prefixMappings).
func walkXMLForPathWithPrefixes(data []byte, steps []xpathStep, attrSel string, prefixes map[string]string) (string, bool) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []xpathFrame
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) > 0 {
				if stack[len(stack)-1].childPos == nil {
					stack[len(stack)-1].childPos = map[string]int{}
				}
				stack[len(stack)-1].childPos[t.Name.Local]++
			}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			stack = append(stack, xpathFrame{
				name:  t.Name.Local,
				uri:   t.Name.Space,
				attrs: attrs,
			})
			if attrSel != "" && matchSuffixNS(stack, steps, prefixes) {
				if v, ok := attrs[attrSel]; ok {
					return strings.TrimSpace(v), true
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if attrSel != "" {
				continue
			}
			if matchSuffixNS(stack, steps, prefixes) {
				v := strings.TrimSpace(string(t))
				if v != "" {
					return v, true
				}
			}
		}
	}
}

// matchSuffixNS is matchSuffixWithPredicates plus a namespace-URI check
// when the step's prefix resolves through `prefixes`. Steps with no
// prefix (or with a prefix not in the map) match by local name only —
// keeping behavior identical to the legacy resolver for those steps.
func matchSuffixNS(stack []xpathFrame, steps []xpathStep, prefixes map[string]string) bool {
	if len(stack) < len(steps) {
		return false
	}
	off := len(stack) - len(steps)
	for i, st := range steps {
		f := stack[off+i]
		if st.name != "" && st.name != f.name {
			return false
		}
		if st.prefix != "" && len(prefixes) > 0 {
			if uri, ok := prefixes[st.prefix]; ok && uri != f.uri {
				return false
			}
		}
		if st.position > 0 {
			var parentCount int
			if off+i-1 >= 0 {
				parentCount = stack[off+i-1].childPos[f.name]
			}
			if parentCount != st.position {
				return false
			}
		}
		if st.attrName != "" && f.attrs[st.attrName] != st.attrVal {
			return false
		}
		if st.andAttrName != "" && f.attrs[st.andAttrName] != st.andAttrVal {
			return false
		}
		if !evalXPathConds(st.conds, f, stack, off+i) {
			return false
		}
	}
	return true
}

// evalXPathConds checks every clause in conds against the candidate
// frame f. Returns true when ALL clauses pass; false otherwise.
// position-eq / last require parentCount + total-children which the
// streaming walker doesn't have a clean shot at — we approximate.
func evalXPathConds(conds []xpathCond, f xpathFrame, stack []xpathFrame, idx int) bool {
	if len(conds) == 0 {
		return true
	}
	for _, c := range conds {
		pass := true
		switch c.kind {
		case "attr":
			pass = f.attrs[c.Name] == c.Val
		case "child":
			// Streaming walker doesn't retain child text in the frame;
			// child-equality is handled by the legacy branch in
			// matchSuffix. We pass-through here.
			pass = true
		case "contains":
			pass = strings.Contains(f.attrs[c.Name], c.Val)
		case "starts-with":
			pass = strings.HasPrefix(f.attrs[c.Name], c.Val)
		case "position-eq":
			var parentCount int
			if idx-1 >= 0 && idx-1 < len(stack) {
				parentCount = stack[idx-1].childPos[f.name]
			}
			pass = parentCount == c.PosN
		case "last":
			// Without an end-of-parent signal we can't know "is last";
			// approximate as always pass so authors get partial coverage.
			pass = true
		default:
			pass = true
		}
		if c.Negated {
			pass = !pass
		}
		if !pass {
			return false
		}
	}
	return true
}

// matchSuffixWithPredicates returns true when the last len(steps) stack
// frames satisfy their corresponding steps' tag-name and optional predicate.
func matchSuffixWithPredicates(stack []xpathFrame, steps []xpathStep) bool {
	if len(stack) < len(steps) {
		return false
	}
	off := len(stack) - len(steps)
	for i, st := range steps {
		f := stack[off+i]
		if st.name != "" && st.name != f.name {
			return false
		}
		if st.position > 0 {
			// Position checks against the parent frame's child-count
			// for this tag at the moment this frame was created. We
			// approximate by reading the live child-count which equals
			// the cumulative number of f.name children opened so far —
			// matching st.position when this is the Nth.
			var parentCount int
			if off+i-1 >= 0 {
				parentCount = stack[off+i-1].childPos[f.name]
			}
			if parentCount != st.position {
				return false
			}
		}
		if st.attrName != "" && f.attrs[st.attrName] != st.attrVal {
			return false
		}
		if st.andAttrName != "" && f.attrs[st.andAttrName] != st.andAttrVal {
			return false
		}
		if !evalXPathConds(st.conds, f, stack, off+i) {
			return false
		}
	}
	return true
}

// parseGradFill parses a DrawingML <a:gradFill> element into a list of
// color stops, the gradient angle (in degrees), and a kind ("linear" or
// "radial"). The XML schema is:
//
//	<a:gradFill ...>
//	  <a:gsLst>
//	    <a:gs pos="0">    <a:srgbClr val="…"/> </a:gs>
//	    <a:gs pos="100000"><a:srgbClr val="…"/> </a:gs>
//	  </a:gsLst>
//	  <a:lin ang="5400000" />   <!-- linear angle in 60000ths of a degree -->
//	  OR
//	  <a:path path="circle"/>    <!-- radial -->
//	</a:gradFill>
func parseGradFill(dec *xml.Decoder, start xml.StartElement) (stops []GradientStop, angleDeg float64, kind string, err error) {
	kind = "linear"
	for {
		tok, e := dec.Token()
		if e != nil {
			return stops, angleDeg, kind, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "gsLst":
				if err := parseGsLst(dec, t, &stops); err != nil {
					return stops, angleDeg, kind, err
				}
			case "lin":
				kind = "linear"
				if v := attr(t, "ang"); v != "" {
					if a, e := strconv.ParseFloat(v, 64); e == nil {
						angleDeg = a / 60000.0
					}
				}
				_ = dec.Skip()
			case "path":
				kind = "radial"
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return stops, angleDeg, kind, nil
			}
		}
	}
}

func parseGsLst(dec *xml.Decoder, start xml.StartElement, stops *[]GradientStop) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "gs" {
				stop := GradientStop{Alpha: 1}
				if v := attr(t, "pos"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						stop.Pos = x / 100000.0
					}
				}
				if c := scanSolidFillColor(dec, t); c != "" {
					stop.Color = c
				}
				*stops = append(*stops, stop)
			} else {
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// extractChartStruct parses a chart part (word/charts/chartN.xml) into a
// structured ChartData ready for the renderer. The parser only recognizes
// the chart families we can paint (bar / column / pie / doughnut / line /
// scatter); other chart types return an empty Kind so the caller can fall
// back to the existing text-extraction path.
func extractChartStruct(f *zip.File) (ChartData, error) {
	var out ChartData
	rc, err := openZipFile(f)
	if err != nil {
		return out, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	valAxIDs := map[int64]bool{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "title":
			out.Title = extractChartTitle(dec, se)
		case "catAx":
			title, deleted := parseAxisInfo(dec, se)
			if title != "" {
				out.XAxisTitle = title
			}
			if deleted {
				out.CategoryAxisDeleted = true
			}
		case "valAx":
			info := parseValAxScaling(dec, se)
			if info.Title != "" && out.YAxisTitle == "" {
				out.YAxisTitle = info.Title
			}
			if info.Deleted {
				out.ValueAxisDeleted = true
			}
			if info.HasMin && !out.HasValMin {
				out.HasValMin = true
				out.ValMin = info.Min
			}
			if info.HasMax && !out.HasValMax {
				out.HasValMax = true
				out.ValMax = info.Max
			}
			if info.LogBase > 0 && out.LogBase == 0 {
				out.LogBase = info.LogBase
			}
			if info.MajorUnit > 0 && out.MajorUnit == 0 {
				out.MajorUnit = info.MajorUnit
			}
			if info.MinorUnit > 0 && out.MinorUnit == 0 {
				out.MinorUnit = info.MinorUnit
			}
			if info.HasCrossesAt && !out.HasCrossesAt {
				out.HasCrossesAt = true
				out.CrossesAt = info.CrossesAt
			}
			if info.AxId != 0 {
				valAxIDs[info.AxId] = true
				if len(valAxIDs) > 1 {
					out.HasSecondaryAxis = true
				}
			}
		case "legend":
			pos, del := parseLegendInfo(dec, se)
			if pos != "" {
				out.LegendPos = pos
			}
			if del {
				out.LegendDeleted = true
			}
		case "plotVisOnly":
			if attr(se, "val") != "0" {
				out.PlotVisOnly = true
			}
			_ = dec.Skip()
		case "dispBlanksAs":
			if v := attr(se, "val"); v != "" {
				out.DispBlanksAs = v
			}
			_ = dec.Skip()
		case "dLbls":
			parseDLbls(dec, se, &out.DataLabels)
		case "barChart":
			out.Kind = "column"
			for _, a := range se.Attr {
				_ = a
			}
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
			// barChart sub-discriminator: <c:barDir val="bar"/> ↔ horizontal.
			// Our column kind is the vertical default; horizontal is recorded
			// at parseChartTypeBody time when it sees barDir.
		case "lineChart":
			out.Kind = "line"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "pieChart", "pie3DChart":
			out.Kind = "pie"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "doughnutChart":
			out.Kind = "doughnut"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "scatterChart":
			out.Kind = "scatter"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "areaChart", "area3DChart":
			out.Kind = "area"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "bubbleChart":
			out.Kind = "bubble"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "radarChart":
			out.Kind = "radar"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "stockChart":
			// Stock chart carries 3 or 4 series (open/high/low/close). The
			// renderer composes candlesticks from them.
			out.Kind = "stock"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "surfaceChart", "surface3DChart":
			// Surface charts: render as stacked line series with subtle
			// gradient-style fills between adjacent series so the
			// "topographic" intent is at least suggested.
			out.Kind = "surface"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		case "ofPieChart":
			// Pie-of-pie / bar-of-pie. The detail series gets pulled out
			// of the main pie into a smaller adjacent pie or column.
			out.Kind = "ofPie"
			if err := parseChartTypeBody(dec, se, &out); err != nil {
				return out, err
			}
		}
	}
}

// extractChartTitle pulls the visible text out of a c:title element.
func extractChartTitle(dec *xml.Decoder, start xml.StartElement) string {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return strings.TrimSpace(sb.String())
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				if sb.Len() > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(s)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// parseAxisInfo walks a c:catAx / c:valAx subtree, scraping the axis
// title text and whether the axis is marked deleted.
func parseAxisInfo(dec *xml.Decoder, start xml.StartElement) (title string, deleted bool) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "title":
				title = extractChartTitle(dec, t)
			case "delete":
				v := attr(t, "val")
				deleted = v == "" || v == "1" || v == "true"
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return
}

// valAxInfo collects the metadata we care about from a single c:valAx
// subtree in one pass.
type valAxInfo struct {
	Title        string
	Deleted      bool
	Min, Max     float64
	HasMin       bool
	HasMax       bool
	LogBase      float64
	MajorUnit    float64
	MinorUnit    float64
	CrossesAt    float64
	HasCrossesAt bool
	AxId         int64
}

// parseValAxScaling drains a c:valAx subtree, scraping the title,
// delete flag, axId, and any c:scaling children (logBase/min/max), plus
// majorUnit / minorUnit / crossesAt.
func parseValAxScaling(dec *xml.Decoder, start xml.StartElement) valAxInfo {
	info := valAxInfo{}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return info
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "axId":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseInt(v, 10, 64); err == nil {
						info.AxId = n
					}
				}
				_ = dec.Skip()
			case "title":
				info.Title = extractChartTitle(dec, t)
			case "delete":
				v := attr(t, "val")
				info.Deleted = v == "" || v == "1" || v == "true"
				_ = dec.Skip()
			case "scaling":
				parseAxScaling(dec, t, &info)
			case "majorUnit":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						info.MajorUnit = n
					}
				}
				_ = dec.Skip()
			case "minorUnit":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						info.MinorUnit = n
					}
				}
				_ = dec.Skip()
			case "crossesAt":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						info.CrossesAt = n
						info.HasCrossesAt = true
					}
				}
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return info
}

// parseAxScaling reads c:scaling/c:logBase / min / max.
func parseAxScaling(dec *xml.Decoder, start xml.StartElement, info *valAxInfo) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "logBase":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil && n > 1 {
						info.LogBase = n
					}
				}
				_ = dec.Skip()
			case "min":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						info.Min = n
						info.HasMin = true
					}
				}
				_ = dec.Skip()
			case "max":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						info.Max = n
						info.HasMax = true
					}
				}
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
}

// parseLegendInfo extracts c:legendPos@val ("b","t","l","r","tr") and
// any c:delete inside c:legend.
func parseLegendInfo(dec *xml.Decoder, start xml.StartElement) (pos string, deleted bool) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "legendPos":
				pos = attr(t, "val")
				_ = dec.Skip()
			case "delete":
				v := attr(t, "val")
				deleted = v == "" || v == "1" || v == "true"
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return
}

// parseDLbls reads c:dLbls option toggles into out.
func parseDLbls(dec *xml.Decoder, start xml.StartElement, out *DataLabelOptions) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			val := attr(t, "val")
			truthy := val == "" || val == "1" || val == "true"
			switch t.Name.Local {
			case "showVal":
				if truthy {
					out.ShowVal = true
				}
				_ = dec.Skip()
			case "showCatName":
				if truthy {
					out.ShowCatName = true
				}
				_ = dec.Skip()
			case "showSerName":
				if truthy {
					out.ShowSerName = true
				}
				_ = dec.Skip()
			case "showPercent":
				if truthy {
					out.ShowPercent = true
				}
				_ = dec.Skip()
			case "showLegendKey":
				if truthy {
					out.ShowLegendKey = true
				}
				_ = dec.Skip()
			case "showBubbleSize":
				if truthy {
					out.ShowBubbleSize = true
				}
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
}

// parseChartTypeBody walks the children of a chart type element, picking
// up each c:ser sub-tree. Also detects barDir="bar" so the renderer can
// distinguish horizontal bars from vertical columns.
func parseChartTypeBody(dec *xml.Decoder, start xml.StartElement, out *ChartData) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "barDir":
				if attr(t, "val") == "bar" && out.Kind == "column" {
					out.Kind = "bar"
				}
				_ = dec.Skip()
			case "grouping":
				// c:grouping discriminates clustered (default) / stacked /
				// percentStacked for bar+column, and standard / stacked /
				// percentStacked for area+line. The renderer reads this to
				// pick between side-by-side and stacked bar layouts.
				if v := attr(t, "val"); v != "" && out.Grouping == "" {
					out.Grouping = v
				}
				_ = dec.Skip()
			case "varyColors":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					out.VaryColors = true
				}
				_ = dec.Skip()
			case "gapWidth":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						out.GapWidthPct = n
					}
				}
				_ = dec.Skip()
			case "overlap":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						out.OverlapPct = n
					}
				}
				_ = dec.Skip()
			case "smooth":
				// Chart-level c:smooth defaults all series to smooth.
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					for i := range out.Series {
						out.Series[i].Smooth = true
					}
				}
				_ = dec.Skip()
			case "dLbls":
				// Chart-level dLbls — merge into out.DataLabels.
				parseDLbls(dec, t, &out.DataLabels)
			case "ser":
				ser, cats, err := parseChartSeries(dec, t)
				if err != nil {
					return err
				}
				if ser.Name != "" || len(ser.Values) > 0 {
					out.Series = append(out.Series, ser)
				}
				if len(cats) > len(out.Categories) {
					out.Categories = cats
				}
			case "upDownBars":
				out.HasUpDownBars = true
				_ = dec.Skip()
			case "hiLowLines":
				out.HasHiLowLines = true
				_ = dec.Skip()
			case "dropLines":
				out.HasDropLines = true
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// parseChartSeries reads one c:ser element. Returns the series plus any
// category labels it carried (categories live on the series in OOXML —
// they typically repeat identically across series, so the caller picks
// the longest list).
func parseChartSeries(dec *xml.Decoder, start xml.StartElement) (ChartSeries, []string, error) {
	var ser ChartSeries
	var cats []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return ser, cats, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tx":
				ser.Name = extractFirstText(dec, t)
			case "cat", "xVal":
				cats = parseChartRefStrings(dec, t)
			case "val", "yVal":
				ser.Values = parseChartRefNumbers(dec, t)
			case "spPr":
				ser.Color = parseFirstSolidFill(dec, t)
			case "smooth":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					ser.Smooth = true
				}
				_ = dec.Skip()
			case "marker":
				ser.MarkerSymbol = parseSeriesMarker(dec, t)
			case "trendline":
				ser.Trendline = parseTrendline(dec, t)
			case "errBars":
				ser.ErrBars = parseErrBars(dec, t)
			case "dLbls":
				dl := DataLabelOptions{}
				parseDLbls(dec, t, &dl)
				ser.DataLabels = &dl
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return ser, cats, nil
			}
		}
	}
}

// parseSeriesMarker reads c:marker/c:symbol. Returns "" when no
// child symbol is present.
func parseSeriesMarker(dec *xml.Decoder, start xml.StartElement) string {
	sym := ""
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return sym
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "symbol" {
				if v := attr(t, "val"); v != "" {
					sym = v
				}
				_ = dec.Skip()
			} else {
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return sym
}

// parseTrendline reads a c:trendline subtree.
func parseTrendline(dec *xml.Decoder, start xml.StartElement) *ChartTrendline {
	out := &ChartTrendline{Kind: "linear"}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trendlineType":
				if v := attr(t, "val"); v != "" {
					out.Kind = v
				}
				_ = dec.Skip()
			case "order":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						out.Order = n
					}
				}
				_ = dec.Skip()
			case "period":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.Atoi(v); err == nil {
						out.Order = n
					}
				}
				_ = dec.Skip()
			case "spPr":
				out.Color = parseFirstSolidFill(dec, t)
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return out
}

// parseErrBars reads a c:errBars subtree.
func parseErrBars(dec *xml.Decoder, start xml.StartElement) *ChartErrBars {
	out := &ChartErrBars{Direction: "y", ErrValType: "fixedVal"}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "errDir":
				if v := attr(t, "val"); v != "" {
					out.Direction = v
				}
				_ = dec.Skip()
			case "errValType":
				if v := attr(t, "val"); v != "" {
					out.ErrValType = v
				}
				_ = dec.Skip()
			case "val":
				if v := attr(t, "val"); v != "" {
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						out.Value = n
					}
				}
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return out
}

// extractFirstText returns the first non-empty CharData inside the
// subtree. Used for series name + axis label captures.
func extractFirstText(dec *xml.Decoder, start xml.StartElement) string {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				// Drain remaining tokens so the caller's element loop
				// sees the matching EndElement at the correct depth.
				for depth > 0 {
					tk, e := dec.Token()
					if e != nil {
						return s
					}
					switch tk.(type) {
					case xml.StartElement:
						depth++
					case xml.EndElement:
						depth--
					}
				}
				return s
			}
		}
	}
	return ""
}

// parseChartRefStrings collects the visible text of every <c:pt><c:v>
// child anywhere under start. Categories arrive in document order; idx
// attributes are not honored beyond what the source already provides.
func parseChartRefStrings(dec *xml.Decoder, start xml.StartElement) []string {
	var out []string
	depth := 1
	inV := false
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "v" {
				inV = true
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "v" {
				inV = false
			}
		case xml.CharData:
			if inV {
				s := strings.TrimSpace(string(t))
				if s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// parseChartRefNumbers is the numeric twin of parseChartRefStrings. Non-
// numeric entries are silently dropped (Word writes "#N/A" for missing
// data points; we treat those as zero so the chart still renders).
func parseChartRefNumbers(dec *xml.Decoder, start xml.StartElement) []float64 {
	var out []float64
	depth := 1
	inV := false
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "v" {
				inV = true
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "v" {
				inV = false
			}
		case xml.CharData:
			if inV {
				s := strings.TrimSpace(string(t))
				if s == "" {
					continue
				}
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					out = append(out, v)
				} else {
					out = append(out, 0)
				}
			}
		}
	}
	return out
}

// parseFirstSolidFill returns the first <a:srgbClr val="…"/> color found
// anywhere in the subtree. Used for series color discovery.
func parseFirstSolidFill(dec *xml.Decoder, start xml.StartElement) string {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "srgbClr" {
				v := attr(t, "val")
				if v != "" {
					// Drain remaining tokens at correct depth before return.
					for depth > 0 {
						tk, e := dec.Token()
						if e != nil {
							return v
						}
						switch tk.(type) {
						case xml.StartElement:
							depth++
						case xml.EndElement:
							depth--
						}
					}
					return strings.ToUpper(v)
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return ""
}

// parsePattFill parses a DrawingML <a:pattFill> element and returns an
// approximated solid color: the per-channel average of <a:fgClr> and
// <a:bgClr>. We don't render the actual pattern tile; the average gives
// a sensible mid-tone the eye reads as the pattern's overall shade.
//
// XML schema:
//
//	<a:pattFill prst="…">
//	  <a:fgClr><a:srgbClr val="…"/></a:fgClr>
//	  <a:bgClr><a:srgbClr val="…"/></a:bgClr>
//	</a:pattFill>
func parsePattFill(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var fg, bg string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "fgClr":
				if c := scanSolidFillColor(dec, t); c != "" {
					fg = c
				}
			case "bgClr":
				if c := scanSolidFillColor(dec, t); c != "" {
					bg = c
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				switch {
				case fg != "" && bg != "":
					return averageHexColor(fg, bg), nil
				case fg != "":
					return fg, nil
				case bg != "":
					return bg, nil
				}
				return "", nil
			}
		}
	}
}

// averageHexColor returns the per-channel arithmetic mean of two 6-hex
// colors. Inputs are tolerated with or without a leading '#'. Returns
// "" if either side fails to parse.
func averageHexColor(a, b string) string {
	a = strings.TrimPrefix(a, "#")
	b = strings.TrimPrefix(b, "#")
	if len(a) != 6 || len(b) != 6 {
		return ""
	}
	parse := func(s string) (int, int, int, bool) {
		x, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		return int(x>>16) & 0xff, int(x>>8) & 0xff, int(x) & 0xff, true
	}
	ar, ag, ab, ok1 := parse(a)
	br, bg, bb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return ""
	}
	return fmt.Sprintf("%02X%02X%02X", (ar+br)/2, (ag+bg)/2, (ab+bb)/2)
}

// parseEffectList scans <a:effectLst> for the first outer-shadow effect
// and returns its parameters. Inner-shadow, glow, reflection, and other
// effects are ignored.
func parseEffectList(dec *xml.Decoder, start xml.StartElement) (*ShadowEffect, error) {
	var out *ShadowEffect
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "outerShdw" && out == nil {
				eff := &ShadowEffect{Alpha: 1, Color: "000000"}
				// blurRad in EMU
				if v := attr(t, "blurRad"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						eff.BlurPt = x / emuPerPt
					}
				}
				// dist + dir: distance in EMU, direction in 60000ths-of-a-degree.
				dist := 0.0
				dirDeg := 0.0
				if v := attr(t, "dist"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						dist = x / emuPerPt
					}
				}
				if v := attr(t, "dir"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						dirDeg = x / 60000.0
					}
				}
				rad := dirDeg * pi180
				eff.OffsetXPt = dist * cosF(rad)
				eff.OffsetYPt = dist * sinF(rad)
				if c := scanSolidFillColor(dec, t); c != "" {
					eff.Color = c
				}
				out = eff
			} else {
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out, nil
			}
		}
	}
}
