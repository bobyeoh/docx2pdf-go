package docx

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// sdtProps holds the parsed bits of an <w:sdtPr> that the renderer cares
// about beyond the placeholder text — type-discriminator plus per-type
// data (date format, checkbox state, dropdown selection).
type sdtProps struct {
	xpath       string
	xpathPrefix string // w:prefixMappings — "" if absent, mostly informational
	// storeItemID is the GUID from <w:storeItemID>. When non-empty, the
	// XPath resolver searches only the custom-xml part whose
	// itemPropsN.xml advertises that same GUID. Without this, a doc
	// carrying multiple stores with overlapping element names would
	// silently pull values from the wrong store.
	storeItemID string
	// OpenDoPE annotations carried on <w:tag w:val="…"/>. Format:
	//   od:xpath=adX1&od:condition=adC1&od:repeat=adR1
	// Each suffix references an ID in customXml/itemN.xml. The renderer
	// looks the ID up via opendope.xml's <od:xpaths>.
	odCondition   string
	odRepeat      string
	odXpath       string
	kind          string // "", "date", "checkbox", "dropdown", "combo", "picture", "richText", "plainText"
	dateFormat    string
	dateFullValue string
	checked       bool
	// checkedGlyph / uncheckedGlyph are the user-customised symbol code
	// points for w14:checkbox / w14:checkedState / w14:uncheckedState. A
	// font hint may also live alongside; we keep both. Empty falls back
	// to ☒ (U+2612) and ☐ (U+2610) at render time.
	checkedGlyph   string
	uncheckedGlyph string
	checkedFont    string
	uncheckedFont  string
	// multiline flags <w:text w:multiLine="1"/> — plain-text SDT with
	// embedded line breaks. Renderer can keep newlines instead of folding
	// them to a single line.
	multiline bool
	// choices lists displayText values in declaration order — what the
	// user sees in the dropdown menu.
	choices []string
	// choiceValues is the parallel data-value array; choiceValues[i] is
	// the "value" attribute that maps to choices[i]'s displayText. Used
	// to resolve lastValue (a value, not display text) to its label.
	choiceValues []string
	// selectedValue holds Word's <w:dropDownList lastValue="…"> attribute.
	// We resolve it against choiceValues to find the displayText.
	selectedValue string
	defaultText   string
	// placeholderText carries <w:placeholder><w:docPart val="…"/> info
	// when present — we don't currently chase the glossary part, but we
	// keep the field so future code can.
	placeholderText string
	// showingPlcHdr mirrors <w:showingPlcHdr/>. When set, Word renders
	// the placeholder text (glossary lookup) rather than user-supplied
	// content. We surface it so the renderer can prefer the placeholder
	// even if the sdtContent has user typing.
	showingPlcHdr bool
	// lock mirrors <w:lock w:val="…"/> — "unlocked", "sdtLocked",
	// "contentLocked", or "sdtContentLocked". Informational only; the
	// PDF surface has no native edit-permission marker, but consumers
	// inspecting the AST (e.g. for tag-aware exports) can read it.
	lock string
	// isRepeatingSection mirrors w15:repeatingSection — when combined with
	// w:dataBinding, Word's RepeatingSection content control clones the
	// inner content once per matching custom-XML node. Without a binding
	// the marker is purely structural (the user can add more iterations
	// at runtime in Word but the document carries only one copy).
	isRepeatingSection bool
}

// lookupImageByRefOrName resolves a picture SDT's bound text to one of the
// package's known image rIds. Accepts: a literal rId ("rId7"), a path that
// matches a relationship target ("media/image1.png"), or the trailing
// basename ("image1.png"). Returns the rId on hit, "" on miss.
func (d *Document) lookupImageByRefOrName(ref string) string {
	if d == nil || ref == "" {
		return ""
	}
	if _, ok := d.Images[ref]; ok {
		return ref
	}
	// Match against relationship targets.
	for rid, tgt := range d.RelTargets {
		if tgt == ref {
			if _, ok := d.Images[rid]; ok {
				return rid
			}
		}
		// Trailing basename match: "media/image1.png" matches "image1.png".
		if i := strings.LastIndexByte(tgt, '/'); i >= 0 && tgt[i+1:] == ref {
			if _, ok := d.Images[rid]; ok {
				return rid
			}
		}
	}
	return ""
}

// decodeBlockSdt walks a block-level <w:sdt> subtree and returns the
// paragraphs and tables found inside its <w:sdtContent>. When the sdtPr
// declares a w:dataBinding whose xpath resolves against the package's
// customXml store, the resolved text REPLACES the content.
func decodeBlockSdt(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, error) {
	var out []Block
	var props sdtProps
	var bindingResolved bool
	var boundText string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sdtPr":
				props = scanSdtProps(dec, t)
				if props.xpath != "" {
					if v, ok := resolveXPathInStoreWithPrefixes(pctx.doc.CustomXMLRoots, props.storeItemID, applyRepeatContext(props.xpath, pctx.repeatStack), parsePrefixMappings(props.xpathPrefix)); ok {
						boundText = v
						bindingResolved = true
					}
				} else if props.odXpath != "" {
					if v, ok := resolveOpenDoPEXPathInContext(pctx.doc, props.odXpath, pctx.repeatStack); ok {
						boundText = v
						bindingResolved = true
					}
				}
			case "sdtContent":
				// OpenDoPE condition: skip the entire SDT content when the
				// referenced predicate evaluates falsy.
				if props.odCondition != "" && !resolveOpenDoPECondition(pctx.doc, props.odCondition) {
					_ = dec.Skip()
					continue
				}
				if syntheticText, useSynth := sdtSyntheticTextWithGlossary(props, pctx.doc.Glossary); useSynth {
					_ = dec.Skip()
					p := Paragraph{Runs: []Run{{Text: syntheticText, Props: pctx.doc.Defaults}}}
					out = append(out, p)
					continue
				}
				if bindingResolved {
					// Picture content controls bind their xpath to an image
					// URL/path/rId in custom XML. We can't fetch arbitrary
					// URLs at parse time, so when the binding's text happens
					// to name a known package image (rId or filename), render
					// THAT image. Otherwise, fall through to the embedded
					// sdtContent picture rather than dropping the picture in
					// favor of a bogus text paragraph.
					if props.kind == "picture" {
						if img := pctx.doc.lookupImageByRefOrName(boundText); img != "" {
							_ = dec.Skip()
							r := Run{ImageID: img, Props: pctx.doc.Defaults}
							out = append(out, Paragraph{Runs: []Run{r}})
							continue
						}
						// No matching image found — fall through to render
						// whatever picture sdtContent already carries.
					} else {
						_ = dec.Skip()
						p := Paragraph{Runs: []Run{{Text: boundText, Props: pctx.doc.Defaults}}}
						out = append(out, p)
						continue
					}
				}
				// OpenDoPE repeat: capture the content's raw token stream
				// once, then RE-DECODE it for each iteration with a fresh
				// repeat frame on pctx.repeatStack. Inner SDT XPaths
				// (resolved during decoding) consult the frame via
				// applyRepeatContext, so each clone resolves its own
				// iteration's data instead of all sharing iteration 0.
				if props.odRepeat != "" {
					n := resolveOpenDoPERepeatCountInContext(pctx.doc, props.odRepeat, pctx.repeatStack)
					if n <= 0 {
						_ = dec.Skip()
						continue
					}
					buf, err := captureElementXML(dec, t)
					if err != nil {
						return nil, err
					}
					// xpathPrefix used for inner-XPath rewriting: the base
					// XPath the repeat is iterating over.
					prefix := ""
					if px, ok := pctx.doc.OpenDoPEXPaths[props.odRepeat]; ok {
						prefix = px
					}
					for i := 0; i < n; i++ {
						pctx.repeatStack = append(pctx.repeatStack, openDopeRepeatFrame{
							xpathPrefix: prefix,
							index:       i + 1,
						})
						sub := xml.NewDecoder(strings.NewReader(buf))
						// captureElementXML wraps the captured content in
						// a synthetic <sdtContent> envelope so we can drive
						// the standard block decoder against it.
						subStart, err := readNextStart(sub)
						if err == nil {
							captured := []Block{}
							_ = decodeSdtContentBlocks(sub, subStart, pctx, &captured)
							out = append(out, captured...)
						}
						pctx.repeatStack = pctx.repeatStack[:len(pctx.repeatStack)-1]
					}
					continue
				}
				// w15:repeatingSection on a block-level SDT bound to multiple
				// XML nodes: clone the inner block content N times. Same as
				// the row-level pass in decodeSdtRowsInTable.
				if props.isRepeatingSection && props.xpath != "" {
					n := countXPathMatches(pctx.doc.CustomXMLRoots, props.xpath)
					if n > 1 {
						buf, err := captureElementXML(dec, t)
						if err != nil {
							return nil, err
						}
						for i := 0; i < n; i++ {
							sub := xml.NewDecoder(strings.NewReader(buf))
							subStart, err := readNextStart(sub)
							if err == nil {
								captured := []Block{}
								_ = decodeSdtContentBlocks(sub, subStart, pctx, &captured)
								out = append(out, captured...)
							}
						}
						continue
					}
				}
				if err := decodeSdtContentBlocks(dec, t, pctx, &out); err != nil {
					return nil, err
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out, nil
			}
		}
	}
}

// sdtSyntheticTextWithGlossary is the placeholder-aware variant. It
// first tries the typed state (checkbox/date/dropdown/picture). Failing
// that, when the sdtPr declared a w:placeholder/w:docPart, it looks up
// the doc-part name in the glossary and returns the resulting text.
//
// When <w:showingPlcHdr/> is set, the placeholder lookup wins even when
// typed state exists — matches Word's behavior where un-filled controls
// always show their placeholder until the user starts typing.
func sdtSyntheticTextWithGlossary(p sdtProps, glossary map[string]string) (string, bool) {
	if p.showingPlcHdr && p.placeholderText != "" && glossary != nil {
		if v, ok := glossary[p.placeholderText]; ok && v != "" {
			return v, true
		}
	}
	if s, ok := sdtSyntheticText(p); ok {
		return s, true
	}
	if p.placeholderText != "" && glossary != nil {
		if v, ok := glossary[p.placeholderText]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// sdtSyntheticText returns a text representation when the SDT carries
// typed state (checkbox checked, date formatted, dropdown selected) that
// is better surfaced as a glyph than as the literal placeholder content.
// Returns ("", false) when nothing typed applies.
func sdtSyntheticText(p sdtProps) (string, bool) {
	switch p.kind {
	case "checkbox":
		if p.checked {
			if p.checkedGlyph != "" {
				return p.checkedGlyph, true
			}
			return "☒", true
		}
		if p.uncheckedGlyph != "" {
			return p.uncheckedGlyph, true
		}
		return "☐", true
	case "date":
		if p.dateFullValue != "" {
			return formatSdtDate(p.dateFullValue, p.dateFormat), true
		}
	case "dropdown", "combo":
		// Word stores the user's choice as the data value (w:lastValue).
		// Resolve it against choiceValues to surface the display text.
		if p.selectedValue != "" {
			for i, v := range p.choiceValues {
				if v == p.selectedValue && i < len(p.choices) {
					return p.choices[i], true
				}
			}
			return p.selectedValue, true
		}
		// No selection cached — fall back to the first choice as a hint.
		if len(p.choices) > 0 {
			return p.choices[0], true
		}
	case "picture":
		// Picture SDTs hold an inline picture inside sdtContent. When the
		// SDT body resolves we let the picture render normally; when the
		// content is missing we surface an explicit placeholder so the PDF
		// shows there was meant to be an image here.
		if p.defaultText != "" {
			return p.defaultText, true
		}
	}
	return "", false
}

// formatSdtDate converts an ISO-8601 sdt date value to the format the
// content control declared (e.g. "M/d/yyyy"). Best-effort: we recognize a
// small set of Word-style tokens and fall through with the raw value
// otherwise.
func formatSdtDate(iso, layout string) string {
	if iso == "" {
		return ""
	}
	// Trim time portion if any.
	v := iso
	if i := strings.Index(v, "T"); i > 0 {
		v = v[:i]
	}
	parts := strings.Split(v, "-")
	if len(parts) != 3 {
		return iso
	}
	yyyy := parts[0]
	mm := parts[1]
	dd := parts[2]
	if layout == "" {
		return yyyy + "-" + mm + "-" + dd
	}
	// Tokens Word writes: yyyy, yy, MMMM, MMM, MM, M, dd, d.
	out := layout
	out = strings.ReplaceAll(out, "yyyy", yyyy)
	if len(yyyy) >= 2 {
		out = strings.ReplaceAll(out, "yy", yyyy[len(yyyy)-2:])
	}
	out = strings.ReplaceAll(out, "MM", mm)
	if n, err := strconv.Atoi(mm); err == nil {
		out = strings.ReplaceAll(out, "M", strconv.Itoa(n))
	}
	out = strings.ReplaceAll(out, "dd", dd)
	if n, err := strconv.Atoi(dd); err == nil {
		out = strings.ReplaceAll(out, "d", strconv.Itoa(n))
	}
	return out
}

// scanSdtProps walks an <w:sdtPr> subtree and pulls out the bits we care
// about: dataBinding XPath, type discriminator, and per-type metadata.
func scanSdtProps(dec *xml.Decoder, start xml.StartElement) sdtProps {
	var p sdtProps
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return p
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "dataBinding":
				if p.xpath == "" {
					p.xpath = attr(t, "xpath")
				}
				if p.storeItemID == "" {
					p.storeItemID = strings.Trim(attr(t, "storeItemID"), "{}")
				}
				if p.xpathPrefix == "" {
					p.xpathPrefix = attr(t, "prefixMappings")
				}
			case "repeatingSection", "repeatingSectionItem":
				// w15:repeatingSection (per-section template) and
				// w15:repeatingSectionItem (per-item wrapper inside it).
				// Both flag the SDT as iterating per data node.
				p.isRepeatingSection = true
			case "tag":
				val := attr(t, "val")
				if val != "" {
					p.odCondition, p.odRepeat, p.odXpath = parseOpenDoPETag(val, p.odCondition, p.odRepeat, p.odXpath)
				}
			case "date":
				if p.kind == "" {
					p.kind = "date"
				}
				if v := attr(t, "fullDate"); v != "" {
					p.dateFullValue = v
				}
			case "dateFormat":
				if v := attr(t, "val"); v != "" {
					p.dateFormat = v
				}
			case "checkbox":
				if p.kind == "" {
					p.kind = "checkbox"
				}
			case "checked":
				v := attr(t, "val")
				switch v {
				case "1", "true", "on", "":
					p.checked = true
				}
			case "checkedState":
				// w14:checkedState carries a custom glyph code-point + font
				// for the "checked" state. val is a 4-hex code-point; font
				// is the font family used to render it.
				if v := attr(t, "val"); v != "" {
					if cp, err := strconv.ParseInt(v, 16, 32); err == nil {
						p.checkedGlyph = string(rune(cp))
					}
				}
				if f := attr(t, "font"); f != "" {
					p.checkedFont = f
				}
			case "uncheckedState":
				if v := attr(t, "val"); v != "" {
					if cp, err := strconv.ParseInt(v, 16, 32); err == nil {
						p.uncheckedGlyph = string(rune(cp))
					}
				}
				if f := attr(t, "font"); f != "" {
					p.uncheckedFont = f
				}
			case "dropDownList":
				if p.kind == "" {
					p.kind = "dropdown"
				}
				// Selected value can live as attribute on the dropDownList
				// (Word 2013+ encoding).
				if v := attr(t, "lastValue"); v != "" {
					p.selectedValue = v
				}
			case "comboBox":
				if p.kind == "" {
					p.kind = "combo"
				}
				if v := attr(t, "lastValue"); v != "" {
					p.selectedValue = v
				}
			case "listItem":
				display := attr(t, "displayText")
				value := attr(t, "value")
				if display == "" {
					display = value
				}
				if value == "" {
					value = display
				}
				if display != "" {
					p.choices = append(p.choices, display)
					p.choiceValues = append(p.choiceValues, value)
				}
			case "richText", "text":
				if p.kind == "" {
					if t.Name.Local == "richText" {
						p.kind = "richText"
					} else {
						p.kind = "plainText"
					}
				}
				if v := attr(t, "default"); v != "" {
					p.defaultText = v
				}
				// <w:text w:multiLine="1"/> opts into embedded line breaks
				// on plain-text SDTs; default is single-line.
				if t.Name.Local == "text" {
					switch attr(t, "multiLine") {
					case "1", "true", "on":
						p.multiline = true
					}
				}
			case "picture":
				if p.kind == "" {
					p.kind = "picture"
				}
			case "docPartObj", "docPartList":
				// w:docPartObj references a single AutoText / building-block
				// entry by name (via w:docPartGallery + w:docPartCategory).
				// w:docPartList exposes a picker. Both ultimately surface
				// glossary content; we mark the kind so sdtSyntheticText
				// can attempt a glossary lookup via placeholderText.
				if p.kind == "" {
					if t.Name.Local == "docPartList" {
						p.kind = "docPartList"
					} else {
						p.kind = "docPartObj"
					}
				}
				// Capture the building-block name from any nested
				// w:docPart val attr (a few Word versions emit it here
				// rather than via w:placeholder). scanDocPartName drains
				// the entire subtree including its own end tag, so we
				// must counter-balance the depth++ at the top of this
				// case (the outer loop won't see the matching end).
				if name := scanDocPartName(dec, t); name != "" && p.placeholderText == "" {
					p.placeholderText = name
				}
				depth--
				continue
			case "group", "bibliography", "citation", "equation":
				// Pure structural markers — they wrap content but don't
				// drive synthesis. Record the kind so callers can branch
				// on it (e.g. to suppress placeholder text for these).
				if p.kind == "" {
					p.kind = t.Name.Local
				}
			case "placeholder":
				// <w:placeholder><w:docPart w:val="DocPartName"/></w:placeholder>
				// names a glossary doc-part whose body Word substitutes when
				// the SDT has no other content. We capture the name; the
				// caller resolves it against the Document.Glossary map.
				if dpName := scanPlaceholderDocPart(dec, t); dpName != "" {
					p.placeholderText = dpName
				}
			case "showingPlcHdr":
				p.showingPlcHdr = true
			case "lock":
				if v := attr(t, "val"); v != "" {
					p.lock = v
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return p
}

// scanSdtBinding is a thin wrapper that exposes just the xpath from an
// sdtPr subtree, kept for callers that don't need the typed metadata.
func scanSdtBinding(dec *xml.Decoder, start xml.StartElement) string {
	return scanSdtProps(dec, start).xpath
}

// scanDocPartName walks a w:docPartObj / w:docPartList subtree and returns
// the w:docPart val attribute when present. Same lookup convention as
// scanPlaceholderDocPart but for direct building-block references rather
// than placeholders.
func scanDocPartName(dec *xml.Decoder, start xml.StartElement) string {
	depth := 1
	var name string
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return name
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "docPart" {
				if v := attr(t, "val"); v != "" && name == "" {
					name = v
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return name
}

// scanPlaceholderDocPart walks a w:placeholder subtree and returns the
// w:docPart w:val attribute when present. This name keys into the
// Document.Glossary map; an empty return means the placeholder didn't
// reference a glossary part.
func scanPlaceholderDocPart(dec *xml.Decoder, start xml.StartElement) string {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "docPart" {
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						return a.Value
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return ""
}

// captureElementXML serializes the current element's full sub-tree
// (start through matching end) into a string buffer so callers can
// re-decode the same content multiple times. The decoder is positioned
// at the open of `start` and is advanced through the matching end-tag
// on return. The returned XML wraps the captured content in the same
// outer element so re-decoding starts at the same level.
//
// Note: namespaces declared on ancestor elements aren't repeated on the
// captured element. This is acceptable for our consumers (the SDT block
// decoder) which only consult local names — but means the captured XML
// shouldn't be handed to an XPath-style consumer expecting full
// namespace context.
func captureElementXML(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var b strings.Builder
	enc := xml.NewEncoder(&strBuilderWriter{&b})
	if err := enc.EncodeToken(start); err != nil {
		return "", err
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// readNextStart advances the decoder to the next start-element token
// and returns it. Returns the zero StartElement on early EOF.
func readNextStart(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if s, ok := tok.(xml.StartElement); ok {
			return s, nil
		}
	}
}

// strBuilderWriter adapts strings.Builder to the io.Writer interface
// xml.NewEncoder needs. (strings.Builder.Write already exists, but
// xml.Encoder constructs an *bufio.Writer that wants io.Writer.)
type strBuilderWriter struct{ b *strings.Builder }

func (w *strBuilderWriter) Write(p []byte) (int, error) {
	return w.b.Write(p)
}

// decodeSdtContentBlocks dispatches block-level children of <w:sdtContent>.
func decodeSdtContentBlocks(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext, out *[]Block) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, p)
			case "tbl":
				tbl, err := decodeTable(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, tbl)
			case "sdt":
				nested, err := decodeBlockSdt(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, nested...)
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

// decodeInlineSdt walks an inline <w:sdt> appearing inside a paragraph.
func decodeInlineSdt(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool) error {
	var props sdtProps
	var boundText string
	var bindingResolved bool
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sdtPr":
				props = scanSdtProps(dec, t)
				if props.xpath != "" {
					if v, ok := resolveXPathInStoreWithPrefixes(pctx.doc.CustomXMLRoots, props.storeItemID, applyRepeatContext(props.xpath, pctx.repeatStack), parsePrefixMappings(props.xpathPrefix)); ok {
						boundText = v
						bindingResolved = true
					}
				} else if props.odXpath != "" {
					if v, ok := resolveOpenDoPEXPathInContext(pctx.doc, props.odXpath, pctx.repeatStack); ok {
						boundText = v
						bindingResolved = true
					}
				}
			case "sdtContent":
				// OpenDoPE condition: drop the inline SDT content when the
				// condition resolves falsy.
				if props.odCondition != "" && !resolveOpenDoPECondition(pctx.doc, props.odCondition) {
					_ = dec.Skip()
					continue
				}
				if syntheticText, useSynth := sdtSyntheticText(props); useSynth {
					_ = dec.Skip()
					if !drop {
						p.Runs = append(p.Runs, Run{Text: syntheticText, Props: paraRPr})
					}
					continue
				}
				if bindingResolved {
					// Picture SDTs: when the bound value names a known
					// package image, replace with that image; otherwise
					// fall through so the embedded sdtContent picture
					// renders rather than emitting the URL/path as text.
					if props.kind == "picture" {
						if img := pctx.doc.lookupImageByRefOrName(boundText); img != "" {
							_ = dec.Skip()
							if !drop {
								p.Runs = append(p.Runs, Run{ImageID: img, Props: paraRPr})
							}
							continue
						}
					} else {
						_ = dec.Skip()
						if !drop {
							p.Runs = append(p.Runs, Run{Text: boundText, Props: paraRPr})
						}
						continue
					}
				}
				if err := decodeWrapper(dec, t, p, paraRPr, pctx, drop); err != nil {
					return err
				}
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
