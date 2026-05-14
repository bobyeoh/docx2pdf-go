package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strconv"
	"strings"
)

// Open reads and parses a .docx file at path. It is a thin wrapper around
// Parse — useful when you have a filesystem path; for in-memory bytes or
// streaming use Parse directly.
func Open(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	return Parse(f, st.Size())
}

// Parse reads a docx package from an io.ReaderAt of the given size. This is
// the streaming variant suitable for in-memory bytes, HTTP request bodies
// wrapped in bytes.Reader, or any other ReaderAt source.
//
// Pipeline (mirrors docx4j's load flow):
//  1. Unzip the package.
//  2. Parse word/styles.xml → default RunProps + ParaDefaults + named styles.
//  3. Parse word/_rels/document.xml.rels → rId → media / hyperlink / part.
//  4. Decode word/media/* into image.Image objects.
//  5. Parse word/header*.xml + word/footer*.xml referenced by sectPr.
//  6. Parse word/numbering.xml.
//  7. Parse word/document.xml → AST split into Sections at every sectPr.
func Parse(r io.ReaderAt, size int64) (*Document, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	doc := &Document{
		Images:      map[string]image.Image{},
		Hyperlink:   map[string]string{},
		Styles:      map[string]ParagraphStyle{},
		CharStyles:  map[string]RunProps{},
		TableStyles: map[string]TableStyle{},
		Footnotes:   map[string][]Block{},
		Endnotes:    map[string][]Block{},
		Bookmarks:   map[string]string{},
		Theme: Theme{
			Colors: map[string]string{},
			Fonts:  map[string]string{},
		},
		PageSize: A4Twips,
		Margins:  DefaultMarginsTwips,
		Numbering: Numbering{
			Abstract: map[int]AbstractNum{},
			NumToAbs: map[int]int{},
		},
	}

	// Theme + core properties run before styles so docDefaults can reference
	// them (theme colors etc.).
	if f, ok := files["word/theme/theme1.xml"]; ok {
		_ = parseTheme(f, &doc.Theme) // best-effort; we tolerate a missing theme
	}
	if f, ok := files["docProps/core.xml"]; ok {
		_ = parseCoreProps(f, &doc.Properties)
	}
	if f, ok := files["docProps/app.xml"]; ok {
		_ = parseAppProps(f, &doc.Properties)
	}

	if f, ok := files["word/styles.xml"]; ok {
		if err := parseStyles(f, doc); err != nil {
			return nil, fmt.Errorf("styles.xml: %w", err)
		}
	}

	rels := map[string]relEntry{} // rId → relationship metadata
	if f, ok := files["word/_rels/document.xml.rels"]; ok {
		if err := parseRels(f, rels); err != nil {
			return nil, fmt.Errorf("rels: %w", err)
		}
	}

	// Categorize relationships by mode. External targets are hyperlink URLs;
	// internal targets resolve to package parts (e.g. media/image1.png).
	for rid, e := range rels {
		switch {
		case e.External:
			doc.Hyperlink[rid] = e.Target
		case strings.HasPrefix(e.Target, "media/"):
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			img, err := loadImage(zf)
			if err != nil {
				continue
			}
			doc.Images[rid] = img
		}
	}

	if f, ok := files["word/numbering.xml"]; ok {
		if err := parseNumbering(f, &doc.Numbering); err != nil {
			return nil, fmt.Errorf("numbering.xml: %w", err)
		}
	}

	if f, ok := files["word/footnotes.xml"]; ok {
		if err := parseNotes(f, doc, "footnote", doc.Footnotes); err != nil {
			return nil, fmt.Errorf("footnotes.xml: %w", err)
		}
	}
	if f, ok := files["word/endnotes.xml"]; ok {
		if err := parseNotes(f, doc, "endnote", doc.Endnotes); err != nil {
			return nil, fmt.Errorf("endnotes.xml: %w", err)
		}
	}

	// Pre-parse header/footer parts before document.xml. We need them by rId
	// so that sectPr handling in document.xml can resolve references.
	headerParts := map[string][]Block{}
	footerParts := map[string][]Block{}
	for rid, e := range rels {
		switch {
		case isHeaderRel(e.Type), isFooterRel(e.Type):
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			blocks, err := parseHeaderFooter(zf, doc)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", full, err)
			}
			if isHeaderRel(e.Type) {
				headerParts[rid] = blocks
			} else {
				footerParts[rid] = blocks
			}
		}
	}

	docFile, ok := files["word/document.xml"]
	if !ok {
		return nil, fmt.Errorf("not a docx: missing word/document.xml")
	}
	pctx := &parseDocContext{
		doc:         doc,
		headerParts: headerParts,
		footerParts: footerParts,
	}
	if err := parseDocument(docFile, pctx); err != nil {
		return nil, fmt.Errorf("document.xml: %w", err)
	}
	return doc, nil
}

// parseDocContext carries the lookup tables we need while streaming
// word/document.xml so sectPr can resolve r:id references to the already-
// parsed header/footer block lists, plus a section accumulator so we can
// split body content at sectPr boundaries.
type parseDocContext struct {
	doc         *Document
	headerParts map[string][]Block
	footerParts map[string][]Block

	// curSection accumulates blocks for the section currently being parsed.
	// Each sectPr (inline or top-level) finalizes it and starts a new one.
	curSection Section
}

// finalizeSection appends the in-progress section to doc.Sections, applies
// defaults for any unset page properties, and resets the accumulator.
func (p *parseDocContext) finalizeSection() {
	sec := p.curSection
	if sec.PageSize.WidthTwips == 0 || sec.PageSize.HeightTwips == 0 {
		sec.PageSize = A4Twips
	}
	if sec.Margins == (Margins{}) {
		sec.Margins = DefaultMarginsTwips
	}
	p.doc.Sections = append(p.doc.Sections, sec)
	p.curSection = Section{}
}

// parseTheme reads the bits of theme1.xml we resolve at run time: the color
// scheme (a:clrScheme) and the font scheme (a:fontScheme). Both schemes are
// inside a:themeElements. We do best-effort tokenization rather than full
// JAXB-style binding — theme XML is verbose and we only need a handful of
// leaf values.
func parseTheme(f *zip.File, theme *Theme) error {
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
		switch se.Name.Local {
		case "clrScheme":
			if err := decodeClrScheme(dec, se, theme); err != nil {
				return err
			}
		case "fontScheme":
			if err := decodeFontScheme(dec, se, theme); err != nil {
				return err
			}
		}
	}
}

// decodeClrScheme reads color entries. Each entry is named (accent1, dk1, ...)
// and has either a srgbClr w:val or a sysClr w:lastClr child.
func decodeClrScheme(dec *xml.Decoder, start xml.StartElement, theme *Theme) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			// Drain children looking for a color value.
			depth := 1
			for depth > 0 {
				inner, err := dec.Token()
				if err != nil {
					return err
				}
				switch it := inner.(type) {
				case xml.StartElement:
					depth++
					switch it.Name.Local {
					case "srgbClr":
						theme.Colors[name] = attr(it, "val")
					case "sysClr":
						if v := attr(it, "lastClr"); v != "" {
							theme.Colors[name] = v
						}
					}
				case xml.EndElement:
					depth--
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeFontScheme captures majorFont/minorFont latin face. We don't load
// the TTFs ourselves — we just remember the name so themed runs map there.
func decodeFontScheme(dec *xml.Decoder, start xml.StartElement, theme *Theme) error {
	currentGroup := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "majorFont":
				currentGroup = "major"
			case "minorFont":
				currentGroup = "minor"
			case "latin":
				if currentGroup != "" {
					theme.Fonts[currentGroup+"Ascii"] = attr(t, "typeface")
					theme.Fonts[currentGroup+"HAnsi"] = attr(t, "typeface")
				}
				_ = dec.Skip()
			case "ea":
				if currentGroup != "" {
					theme.Fonts[currentGroup+"EastAsia"] = attr(t, "typeface")
				}
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

// parseCoreProps pulls Title/Author/Subject out of docProps/core.xml. The
// schema has cp:coreProperties with dc:title, dc:creator, dc:subject children.
func parseCoreProps(f *zip.File, p *Properties) error {
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
		var s string
		switch se.Name.Local {
		case "title":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Title = s
			}
		case "creator":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Author = s
			}
		case "subject":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Subject = s
			}
		}
	}
}

// parseAppProps reads docProps/app.xml — extended Office properties. We pull
// out Company/Pages/Words/Characters/Lines for the PDF /Info dictionary.
func parseAppProps(f *zip.File, p *Properties) error {
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
		var s string
		switch se.Name.Local {
		case "Company":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Company = s
			}
		case "Pages":
			if err := dec.DecodeElement(&s, &se); err == nil {
				if x, err := strconv.Atoi(s); err == nil {
					p.Pages = x
				}
			}
		case "Words":
			if err := dec.DecodeElement(&s, &se); err == nil {
				if x, err := strconv.Atoi(s); err == nil {
					p.Words = x
				}
			}
		case "Characters":
			if err := dec.DecodeElement(&s, &se); err == nil {
				if x, err := strconv.Atoi(s); err == nil {
					p.Characters = x
				}
			}
		case "Lines":
			if err := dec.DecodeElement(&s, &se); err == nil {
				if x, err := strconv.Atoi(s); err == nil {
					p.Lines = x
				}
			}
		}
	}
}

// parseNotes parses word/footnotes.xml or word/endnotes.xml. Each
// w:footnote / w:endnote element has w:id and contains paragraphs.
// Separator/continuation notes (w:type="separator"/"continuationSeparator")
// are skipped — they contain only the visual separator rule.
func parseNotes(f *zip.File, doc *Document, elemName string, out map[string][]Block) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	pctx := &parseDocContext{doc: doc}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != elemName {
			continue
		}
		id := attr(se, "id")
		kind := attr(se, "type")
		if kind == "separator" || kind == "continuationSeparator" {
			_ = dec.Skip()
			continue
		}
		blocks, err := parseNoteBody(dec, se, pctx)
		if err != nil {
			return err
		}
		if id != "" {
			out[id] = blocks
		}
	}
	return nil
}

func parseNoteBody(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, error) {
	var blocks []Block
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, p)
			case "tbl":
				tbl, err := decodeTable(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, tbl)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return blocks, nil
			}
		}
	}
}

// parseHeaderFooter parses a w:hdr or w:ftr XML part into a slice of blocks
// by reusing the existing paragraph / table decoders. Header/footer parts
// never carry sectPr, so we ignore that case.
func parseHeaderFooter(f *zip.File, doc *Document) ([]Block, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	pctx := &parseDocContext{doc: doc}
	var blocks []Block
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "p":
			p, err := decodeParagraph(dec, se, pctx)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, p)
		case "tbl":
			t, err := decodeTable(dec, se, pctx)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, t)
		}
	}
	return blocks, nil
}

func openZipFile(f *zip.File) (io.ReadCloser, error) { return f.Open() }

func loadImage(zf *zip.File) (image.Image, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	img, _, err := image.Decode(rc)
	return img, err
}

// --- styles.xml ---------------------------------------------------------

// xmlStyles binds the bits of styles.xml we actually consume:
//   - docDefaults/rPrDefault → Document.Defaults (already worked before)
//   - each paragraph-typed w:style → Document.Styles[styleId]
//
// We resolve w:basedOn after the raw pass so callers never have to walk the
// inheritance chain. docx4j relies on its DOM/JAXB tree and has a similar
// "style resolver" — we collapse to a flat map at load.
type xmlStyles struct {
	XMLName     xml.Name `xml:"styles"`
	DocDefaults struct {
		RPrDefault struct {
			RPr xmlRPr `xml:"rPr"`
		} `xml:"rPrDefault"`
		PPrDefault struct {
			PPr xmlStylePPr `xml:"pPr"`
		} `xml:"pPrDefault"`
	} `xml:"docDefaults"`
	Styles []xmlStyle `xml:"style"`
}

type xmlStyle struct {
	Type    string `xml:"type,attr"`
	StyleID string `xml:"styleId,attr"`
	BasedOn *struct {
		Val string `xml:"val,attr"`
	} `xml:"basedOn"`
	PPr *xmlStylePPr `xml:"pPr"`
	RPr *xmlRPr      `xml:"rPr"`
}

type xmlStylePPr struct {
	Jc *struct {
		Val string `xml:"val,attr"`
	} `xml:"jc"`
	Spacing *struct {
		Before   string `xml:"before,attr"`
		After    string `xml:"after,attr"`
		Line     string `xml:"line,attr"`
		LineRule string `xml:"lineRule,attr"`
	} `xml:"spacing"`
}

func parseStyles(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	var s xmlStyles
	if err := xml.Unmarshal(data, &s); err != nil {
		return err
	}
	doc.Defaults = rPrToProps(s.DocDefaults.RPrDefault.RPr, RunProps{})
	doc.ParaDefaults = pPrDefaultsFromStyle(s.DocDefaults.PPrDefault.PPr)

	// Character styles — flat map of styleId → RunProps with basedOn flattened.
	rawChar := map[string]struct {
		BasedOn string
		Run     RunProps
	}{}
	for _, xs := range s.Styles {
		if xs.Type != "character" || xs.StyleID == "" {
			continue
		}
		var basedOn string
		if xs.BasedOn != nil {
			basedOn = xs.BasedOn.Val
		}
		var rp RunProps
		if xs.RPr != nil {
			rp = rPrToProps(*xs.RPr, RunProps{})
		}
		rawChar[xs.StyleID] = struct {
			BasedOn string
			Run     RunProps
		}{basedOn, rp}
	}
	var resolveChar func(id string, seen map[string]bool) RunProps
	resolveChar = func(id string, seen map[string]bool) RunProps {
		if seen[id] {
			return RunProps{}
		}
		seen[id] = true
		r, ok := rawChar[id]
		if !ok {
			return RunProps{}
		}
		if r.BasedOn == "" {
			return r.Run
		}
		parent := resolveChar(r.BasedOn, seen)
		return mergeRunProps(parent, r.Run)
	}
	for id := range rawChar {
		doc.CharStyles[id] = resolveChar(id, map[string]bool{})
	}

	// Table styles: take the rPr (run defaults) and any shading/borders the
	// style declares at top level. Conditional formatting blocks would be
	// parsed via decodeElement of the raw style; we keep that minimal.
	for _, xs := range s.Styles {
		if xs.Type != "table" || xs.StyleID == "" {
			continue
		}
		ts := TableStyle{ID: xs.StyleID}
		if xs.BasedOn != nil {
			ts.BasedOn = xs.BasedOn.Val
		}
		if xs.RPr != nil {
			ts.Run = rPrToProps(*xs.RPr, RunProps{})
		}
		doc.TableStyles[xs.StyleID] = ts
	}

	// First pass: turn each xmlStyle into a raw ParagraphStyle (no inheritance yet).
	raw := map[string]ParagraphStyle{}
	for _, xs := range s.Styles {
		if xs.Type != "paragraph" || xs.StyleID == "" {
			continue
		}
		ps := ParagraphStyle{ID: xs.StyleID}
		if xs.BasedOn != nil {
			ps.BasedOn = xs.BasedOn.Val
		}
		if xs.RPr != nil {
			ps.Run = rPrToProps(*xs.RPr, RunProps{})
		}
		if xs.PPr != nil {
			if xs.PPr.Jc != nil {
				ps.Alignment = jcToAlignment(xs.PPr.Jc.Val)
				ps.HasAlignment = true
			}
			if xs.PPr.Spacing != nil {
				if v := xs.PPr.Spacing.Before; v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ps.SpacingBefore = float64(x) / 20.0
					}
				}
				if v := xs.PPr.Spacing.After; v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ps.SpacingAfter = float64(x) / 20.0
					}
				}
				if v := xs.PPr.Spacing.Line; v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						rule := xs.PPr.Spacing.LineRule
						if rule == "" {
							rule = "auto"
						}
						ps.LineHeight = LineHeight{Rule: rule}
						switch rule {
						case "exact", "atLeast":
							ps.LineHeight.Pt = float64(x) / 20.0
						default:
							ps.LineHeight.Mul = float64(x) / 240.0
						}
					}
				}
			}
		}
		raw[xs.StyleID] = ps
	}

	// Second pass: flatten basedOn chains. We resolve each style by walking
	// to its root, then merging from root downward so child fields override.
	// Cycle-safe via a visited set.
	var resolve func(id string, seen map[string]bool) ParagraphStyle
	resolve = func(id string, seen map[string]bool) ParagraphStyle {
		if seen[id] {
			return ParagraphStyle{}
		}
		seen[id] = true
		r, ok := raw[id]
		if !ok {
			return ParagraphStyle{}
		}
		if r.BasedOn == "" {
			return r
		}
		parent := resolve(r.BasedOn, seen)
		return mergeStyles(parent, r)
	}
	for id := range raw {
		doc.Styles[id] = resolve(id, map[string]bool{})
	}
	return nil
}

// mergeStyles overlays child on top of parent. A field on child wins iff it
// is "set" — for RunProps that's the same OR-merge rPrToProps already does;
// for paragraph-level fields we use zero/HasAlignment discriminators.
func mergeStyles(parent, child ParagraphStyle) ParagraphStyle {
	out := parent
	out.ID = child.ID
	out.BasedOn = child.BasedOn
	out.Run = mergeRunProps(parent.Run, child.Run)
	if child.HasAlignment {
		out.Alignment = child.Alignment
		out.HasAlignment = true
	}
	if child.SpacingBefore != 0 {
		out.SpacingBefore = child.SpacingBefore
	}
	if child.SpacingAfter != 0 {
		out.SpacingAfter = child.SpacingAfter
	}
	if child.LineHeight.Rule != "" {
		out.LineHeight = child.LineHeight
	}
	return out
}

// mergeRunProps overlays a child run-props block on top of a parent block.
// Each field's "set" predicate matches what rPrToProps actually writes.
func mergeRunProps(parent, child RunProps) RunProps {
	out := parent
	if child.Bold {
		out.Bold = true
	}
	if child.Italic {
		out.Italic = true
	}
	if child.Underline {
		out.Underline = true
	}
	if child.Strike {
		out.Strike = true
	}
	if child.Caps {
		out.Caps = true
	}
	if child.SmallCaps {
		out.SmallCaps = true
	}
	if child.FontSize != 0 {
		out.FontSize = child.FontSize
	}
	if child.FontFamily != "" {
		out.FontFamily = child.FontFamily
	}
	if child.Color != "" {
		out.Color = child.Color
	}
	if child.Highlight != "" {
		out.Highlight = child.Highlight
	}
	if child.Shading != "" {
		out.Shading = child.Shading
	}
	if child.VertAlign != "" {
		out.VertAlign = child.VertAlign
	}
	if child.StyleID != "" {
		out.StyleID = child.StyleID
	}
	if child.Vanish {
		out.Vanish = true
	}
	if child.PositionPt != 0 {
		out.PositionPt = child.PositionPt
	}
	if child.CharacterScale != 0 {
		out.CharacterScale = child.CharacterScale
	}
	if child.ThemeColor != "" {
		out.ThemeColor = child.ThemeColor
	}
	if child.ThemeFontRole != "" {
		out.ThemeFontRole = child.ThemeFontRole
	}
	if child.LetterSpacingPt != 0 {
		out.LetterSpacingPt = child.LetterSpacingPt
	}
	if child.TextEffect != "" {
		out.TextEffect = child.TextEffect
	}
	return out
}

// pPrDefaultsFromStyle pulls spacing/line-height bits out of a style-level
// pPr block. Used for docDefaults/pPrDefault so unstyled paragraphs inherit
// the document-wide defaults Office writes there.
func pPrDefaultsFromStyle(pp xmlStylePPr) ParaDefaults {
	out := ParaDefaults{}
	if pp.Spacing != nil {
		if v := pp.Spacing.Before; v != "" {
			if x, err := strconv.Atoi(v); err == nil {
				out.SpacingBefore = float64(x) / 20.0
			}
		}
		if v := pp.Spacing.After; v != "" {
			if x, err := strconv.Atoi(v); err == nil {
				out.SpacingAfter = float64(x) / 20.0
			}
		}
		if v := pp.Spacing.Line; v != "" {
			if x, err := strconv.Atoi(v); err == nil {
				rule := pp.Spacing.LineRule
				if rule == "" {
					rule = "auto"
				}
				out.LineHeight = LineHeight{Rule: rule}
				switch rule {
				case "exact", "atLeast":
					out.LineHeight.Pt = float64(x) / 20.0
				default:
					out.LineHeight.Mul = float64(x) / 240.0
				}
			}
		}
	}
	return out
}

// decodeTabs parses <w:tabs> children — each <w:tab w:val w:pos w:leader/>.
// "clear" entries are skipped (Word uses them to remove inherited stops).
func decodeTabs(dec *xml.Decoder, start xml.StartElement, p *Paragraph) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tab" {
				val := attr(t, "val")
				if val == "clear" {
					_ = dec.Skip()
					continue
				}
				ts := TabStop{Val: val, Leader: attr(t, "leader")}
				if v := attr(t, "pos"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ts.Pos = float64(x) / 20.0
					}
				}
				p.Tabs = append(p.Tabs, ts)
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				// Sort tabs by Pos so the renderer can binary-search.
				sortTabs(p.Tabs)
				return nil
			}
		}
	}
}

func sortTabs(t []TabStop) {
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && t[j].Pos < t[j-1].Pos; j-- {
			t[j], t[j-1] = t[j-1], t[j]
		}
	}
}

func jcToAlignment(val string) Alignment {
	switch val {
	case "center":
		return AlignCenter
	case "right", "end":
		return AlignRight
	case "both", "distribute":
		return AlignJustify
	}
	return AlignLeft
}

// --- relationships ------------------------------------------------------

type xmlRels struct {
	XMLName       xml.Name `xml:"Relationships"`
	Relationships []struct {
		ID         string `xml:"Id,attr"`
		Type       string `xml:"Type,attr"`
		Target     string `xml:"Target,attr"`
		TargetMode string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

type relEntry struct {
	Target   string
	Type     string
	External bool
}

func parseRels(f *zip.File, out map[string]relEntry) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	var r xmlRels
	if err := xml.Unmarshal(data, &r); err != nil {
		return err
	}
	for _, rel := range r.Relationships {
		out[rel.ID] = relEntry{
			Target:   rel.Target,
			Type:     rel.Type,
			External: rel.TargetMode == "External",
		}
	}
	return nil
}

// isHeaderRel / isFooterRel match the well-known OOXML relationship type URIs
// by suffix so we don't have to hardcode the full schema URL.
func isHeaderRel(t string) bool { return strings.HasSuffix(t, "/header") }
func isFooterRel(t string) bool { return strings.HasSuffix(t, "/footer") }

// --- numbering.xml -------------------------------------------------------

func parseNumbering(f *zip.File, out *Numbering) error {
	if out.PicBullets == nil {
		out.PicBullets = map[int]string{}
	}
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
		switch se.Name.Local {
		case "numPicBullet":
			// numPicBullet wraps a w:pict (legacy VML) OR w:drawing containing
			// the image. We walk the subtree looking for any rId reference.
			id, err := strconv.Atoi(attr(se, "numPicBulletId"))
			if err != nil {
				_ = dec.Skip()
				continue
			}
			rid := findRidInSubtree(dec, se)
			if rid != "" {
				out.PicBullets[id] = rid
			}
		case "abstractNum":
			id, err := strconv.Atoi(attr(se, "abstractNumId"))
			if err != nil {
				_ = dec.Skip()
				continue
			}
			an, err := decodeAbstractNum(dec, se)
			if err != nil {
				return err
			}
			out.Abstract[id] = an
		case "num":
			numID, err := strconv.Atoi(attr(se, "numId"))
			if err != nil {
				_ = dec.Skip()
				continue
			}
			absID, err := decodeNum(dec, se)
			if err != nil {
				return err
			}
			if absID >= 0 {
				out.NumToAbs[numID] = absID
			}
		}
	}
}

func decodeAbstractNum(dec *xml.Decoder, start xml.StartElement) (AbstractNum, error) {
	an := AbstractNum{Levels: map[int]NumLevel{}}
	for {
		tok, err := dec.Token()
		if err != nil {
			return an, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "lvl" {
				ilvl, _ := strconv.Atoi(attr(t, "ilvl"))
				lv, err := decodeLevel(dec, t)
				if err != nil {
					return an, err
				}
				an.Levels[ilvl] = lv
			} else {
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return an, nil
			}
		}
	}
}

// findRidInSubtree walks the subtree under start, returning the first rId we
// find on any element (a:blip r:embed, v:imagedata r:id, etc.).
func findRidInSubtree(dec *xml.Decoder, start xml.StartElement) string {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			for _, a := range t.Attr {
				if a.Name.Local == "embed" || a.Name.Local == "id" {
					if strings.HasPrefix(a.Value, "rId") || strings.HasPrefix(a.Value, "rid") {
						// Consume rest of subtree.
						for depth > 0 {
							tok2, err := dec.Token()
							if err != nil {
								return a.Value
							}
							switch tok2.(type) {
							case xml.StartElement:
								depth++
							case xml.EndElement:
								depth--
							}
						}
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

func decodeLevel(dec *xml.Decoder, start xml.StartElement) (NumLevel, error) {
	lv := NumLevel{Start: 1}
	for {
		tok, err := dec.Token()
		if err != nil {
			return lv, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "start":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.Start = x
					}
				}
				_ = dec.Skip()
			case "numFmt":
				lv.Format = attr(t, "val")
				_ = dec.Skip()
			case "lvlText":
				lv.Text = attr(t, "val")
				_ = dec.Skip()
			case "pPr":
				if err := decodeLevelPPr(dec, t, &lv); err != nil {
					return lv, err
				}
			case "isLgl":
				lv.IsLgl = true
				_ = dec.Skip()
			case "lvlPicBulletId":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.PicBulletID = x
					}
				}
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return lv, nil
			}
		}
	}
}

func decodeLevelPPr(dec *xml.Decoder, start xml.StartElement, lv *NumLevel) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "ind" {
				if v := attr(t, "left"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.LeftTwips = x
					}
				}
				if v := attr(t, "hanging"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.HangingTwips = x
					}
				}
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeNum returns the abstractNumId pointed to by this w:num, or -1.
func decodeNum(dec *xml.Decoder, start xml.StartElement) (int, error) {
	abs := -1
	for {
		tok, err := dec.Token()
		if err != nil {
			return abs, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "abstractNumId" {
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						abs = x
					}
				}
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return abs, nil
			}
		}
	}
}

// --- document.xml --------------------------------------------------------

// We intentionally do not bind the whole WordprocessingML schema. Instead we
// stream tokens and pull out only the elements we render, ignoring namespaces
// other than by local name. This is the same pragmatic shortcut documents4j
// takes for its lightweight readers, and avoids JAXB-level ceremony.

type xmlRPr struct {
	RStyle    *xmlValAttr `xml:"rStyle"` // character-style reference
	B         *struct{}   `xml:"b"`
	I         *struct{}   `xml:"i"`
	U         *xmlValAttr `xml:"u"`
	Strike    *struct{}   `xml:"strike"`
	Caps      *struct{}   `xml:"caps"`
	SmallCaps *struct{}   `xml:"smallCaps"`
	Vanish    *struct{}   `xml:"vanish"`
	Emboss    *struct{}   `xml:"emboss"`
	Imprint   *struct{}   `xml:"imprint"`
	Outline   *struct{}   `xml:"outline"`
	Sz        *xmlValAttr `xml:"sz"` // half-points
	Color     *struct {
		Val        string `xml:"val,attr"`
		ThemeColor string `xml:"themeColor,attr"`
		ThemeShade string `xml:"themeShade,attr"` // hex 00-FF: darken (lumMod = hex/255)
		ThemeTint  string `xml:"themeTint,attr"`  // hex 00-FF: lighten (lumOff = (255-hex)/255)
	} `xml:"color"`
	Highlight *xmlValAttr `xml:"highlight"` // named: yellow, green, ...
	Shd       *struct {
		Fill string `xml:"fill,attr"`
	} `xml:"shd"`
	VertAlign *xmlValAttr `xml:"vertAlign"` // "superscript" | "subscript"
	Position  *xmlValAttr `xml:"position"`  // half-points; +up / -down
	WAttr     *xmlValAttr `xml:"w"`         // character scale percent (100 = normal)
	Spacing   *xmlValAttr `xml:"spacing"`   // letter spacing in 1/20 pt
	RFonts    *struct {
		ASCII      string `xml:"ascii,attr"`
		HAnsi      string `xml:"hAnsi,attr"`
		EA         string `xml:"eastAsia,attr"`
		AsciiTheme string `xml:"asciiTheme,attr"`
		HAnsiTheme string `xml:"hAnsiTheme,attr"`
		EATheme    string `xml:"eastAsiaTheme,attr"`
	} `xml:"rFonts"`
}

type xmlValAttr struct {
	Val string `xml:"val,attr"`
}

func rPrToProps(r xmlRPr, base RunProps) RunProps {
	p := base
	if r.B != nil {
		p.Bold = true
	}
	if r.I != nil {
		p.Italic = true
	}
	if r.U != nil && r.U.Val != "" && r.U.Val != "none" {
		p.Underline = true
	}
	if r.Strike != nil {
		p.Strike = true
	}
	if r.Caps != nil {
		p.Caps = true
	}
	if r.SmallCaps != nil {
		p.SmallCaps = true
	}
	if r.RStyle != nil && r.RStyle.Val != "" {
		p.StyleID = r.RStyle.Val
	}
	if r.Highlight != nil && r.Highlight.Val != "" && r.Highlight.Val != "none" {
		p.Highlight = r.Highlight.Val
	}
	if r.Shd != nil && r.Shd.Fill != "" && r.Shd.Fill != "auto" {
		p.Shading = r.Shd.Fill
	}
	if r.VertAlign != nil {
		p.VertAlign = r.VertAlign.Val
	}
	if r.Vanish != nil {
		p.Vanish = true
	}
	if r.Position != nil && r.Position.Val != "" {
		if x, err := strconv.Atoi(r.Position.Val); err == nil {
			p.PositionPt = float64(x) / 2.0 // half-points
		}
	}
	if r.WAttr != nil && r.WAttr.Val != "" {
		if x, err := strconv.Atoi(r.WAttr.Val); err == nil && x > 0 {
			p.CharacterScale = float64(x) / 100.0
		}
	}
	if r.Sz != nil {
		if hp, err := strconv.ParseFloat(r.Sz.Val, 64); err == nil {
			p.FontSize = hp / 2 // half-points → points
		}
	}
	if r.Color != nil {
		if r.Color.Val != "" && r.Color.Val != "auto" {
			p.Color = r.Color.Val
		}
		if r.Color.ThemeColor != "" {
			p.ThemeColor = r.Color.ThemeColor
		}
		if r.Color.ThemeShade != "" {
			if v, err := strconv.ParseUint(r.Color.ThemeShade, 16, 8); err == nil {
				p.LumMod = float64(v) / 255.0
			}
		}
		if r.Color.ThemeTint != "" {
			if v, err := strconv.ParseUint(r.Color.ThemeTint, 16, 8); err == nil {
				p.LumOff = float64(255-v) / 255.0
			}
		}
	}
	if r.Emboss != nil {
		p.TextEffect = "emboss"
	}
	if r.Imprint != nil {
		p.TextEffect = "imprint"
	}
	if r.Outline != nil {
		p.TextEffect = "outline"
	}
	if r.Spacing != nil && r.Spacing.Val != "" {
		if x, err := strconv.Atoi(r.Spacing.Val); err == nil {
			p.LetterSpacingPt = float64(x) / 20.0
		}
	}
	if r.RFonts != nil {
		switch {
		case r.RFonts.EA != "":
			p.FontFamily = r.RFonts.EA
		case r.RFonts.ASCII != "":
			p.FontFamily = r.RFonts.ASCII
		case r.RFonts.HAnsi != "":
			p.FontFamily = r.RFonts.HAnsi
		}
		// Theme font role: only set when we don't already have an explicit
		// face. Word's resolution is: explicit font wins, then theme refs.
		switch {
		case r.RFonts.AsciiTheme != "":
			p.ThemeFontRole = themeFontRole("Ascii", r.RFonts.AsciiTheme)
		case r.RFonts.HAnsiTheme != "":
			p.ThemeFontRole = themeFontRole("HAnsi", r.RFonts.HAnsiTheme)
		case r.RFonts.EATheme != "":
			p.ThemeFontRole = themeFontRole("EastAsia", r.RFonts.EATheme)
		}
	}
	return p
}

// themeFontRole maps Word's family + role attr to the role key we store in
// Document.Theme.Fonts. e.g. "Ascii" + "majorHAnsi" → "majorAscii". Word
// actually overloads asciiTheme = majorHAnsi / minorHAnsi (the H is for the
// "H-Ansi" variant of Latin) — we collapse to "major"/"minor".
func themeFontRole(family, role string) string {
	switch role {
	case "majorHAnsi", "majorAscii":
		return "majorAscii"
	case "minorHAnsi", "minorAscii":
		return "minorAscii"
	case "majorEastAsia":
		return "majorEastAsia"
	case "minorEastAsia":
		return "minorEastAsia"
	}
	return ""
}

// parseDocument streams word/document.xml. Each sectPr (inline inside pPr or
// top-level after the last paragraph) finalizes the current Section and
// starts a new one. Body-level helpers like doc.Body / doc.PageSize /
// doc.Margins / doc.HeaderBlocks / doc.FooterBlocks are kept up to date as
// the "last section" view for backward compat.
func parseDocument(f *zip.File, pctx *parseDocContext) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)

	doc := pctx.doc
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "p":
			p, err := decodeParagraph(dec, se, pctx)
			if err != nil {
				return err
			}
			pctx.curSection.Blocks = append(pctx.curSection.Blocks, p)
			doc.Body = append(doc.Body, p)
			// Inline sectPr inside this paragraph's pPr ends the section.
			if p.endsSection {
				pctx.finalizeSection()
			}
		case "tbl":
			t, err := decodeTable(dec, se, pctx)
			if err != nil {
				return err
			}
			pctx.curSection.Blocks = append(pctx.curSection.Blocks, t)
			doc.Body = append(doc.Body, t)
		case "sectPr":
			// Top-level sectPr: properties of the final section.
			if err := decodeSectPr(dec, se, pctx); err != nil {
				return err
			}
			pctx.finalizeSection()
		}
	}
	// If the document ends without any sectPr (unusual but legal), still
	// emit a section so the renderer has something to walk.
	if len(pctx.curSection.Blocks) > 0 || len(doc.Sections) == 0 {
		pctx.finalizeSection()
	}
	// Backward-compat: expose the last section's properties as doc-level fields.
	if n := len(doc.Sections); n > 0 {
		last := doc.Sections[n-1]
		doc.PageSize = last.PageSize
		doc.Margins = last.Margins
		doc.HeaderBlocks = last.HeaderBlocks
		doc.FooterBlocks = last.FooterBlocks
	}
	return nil
}

func decodeParagraph(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) (Paragraph, error) {
	doc := pctx.doc
	// Seed with document-wide pPr defaults; later pPr siblings override.
	p := Paragraph{
		SpacingBefore: doc.ParaDefaults.SpacingBefore,
		SpacingAfter:  doc.ParaDefaults.SpacingAfter,
		LineHeight:    doc.ParaDefaults.LineHeight,
	}
	paraRPr := doc.Defaults // paragraph-level rPr inherited by runs

	// Per-paragraph bookmark scope. We map w:id → w:name when bookmarkStart
	// fires, then while a name is active we accumulate run text into
	// doc.Bookmarks[name] so REF fields can resolve later.
	bmIDToName := map[string]string{}
	bmActive := map[string]bool{}

	for {
		tok, err := dec.Token()
		if err != nil {
			return p, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				if err := decodePPr(dec, t, &p, &paraRPr, pctx); err != nil {
					return p, err
				}
			case "r":
				runs, err := decodeRun(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return p, err
				}
				p.Runs = append(p.Runs, runs...)
				// While any bookmark is active, append the visible text from
				// this run to the corresponding doc.Bookmarks entries.
				if len(bmActive) > 0 {
					var sb strings.Builder
					for _, rn := range runs {
						sb.WriteString(rn.Text)
					}
					if t := sb.String(); t != "" {
						for name := range bmActive {
							doc.Bookmarks[name] += t
						}
					}
				}
			case "hyperlink":
				if err := decodeHyperlink(dec, t, &p, paraRPr, pctx); err != nil {
					return p, err
				}
			case "bookmarkStart":
				// Emit a marker run + register the bookmark name as active so
				// the rest of this paragraph contributes its text to the
				// bookmark body (used by REF field resolution).
				id := attr(t, "id")
				name := attr(t, "name")
				if name != "" {
					bmIDToName[id] = name
					bmActive[name] = true
					if !strings.HasPrefix(name, "_") {
						p.Runs = append(p.Runs, Run{Bookmark: name, Props: paraRPr})
					}
				}
				_ = dec.Skip()
			case "bookmarkEnd":
				id := attr(t, "id")
				if name, ok := bmIDToName[id]; ok {
					delete(bmActive, name)
				}
				_ = dec.Skip()
			case "ins":
				// w:ins wraps a tracked-change INSERTION — accept mode: render
				// its child runs as normal text.
				if err := decodeRevisionWrapper(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "del":
				// w:del wraps DELETED runs — accept mode: drop entirely.
				if err := decodeRevisionWrapper(dec, t, &p, paraRPr, pctx, true); err != nil {
					return p, err
				}
			case "commentRangeStart", "commentRangeEnd", "commentReference":
				// Comments are out-of-flow; we skip the inline markers.
				_ = dec.Skip()
			default:
				if err := dec.Skip(); err != nil {
					return p, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return p, nil
			}
		}
	}
}

// decodeRevisionWrapper handles <w:ins> and <w:del>. In accept mode (Word's
// default for read-only render) we either keep all child runs (ins) or drop
// them (del). The decoder structurally mirrors decodeHyperlink.
func decodeRevisionWrapper(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "r" {
				runs, err := decodeRun(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return err
				}
				if !drop {
					p.Runs = append(p.Runs, runs...)
				}
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

func decodeHyperlink(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext) error {
	// w:hyperlink has either r:id (external URL via rels) or w:anchor
	// (internal bookmark name). The renderer treats them differently.
	rid, anchor := "", ""
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "id":
			rid = a.Value
		case "anchor":
			anchor = a.Value
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "r" {
				runs, err := decodeRun(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return err
				}
				for i := range runs {
					if rid != "" {
						runs[i].LinkURL = rid
					}
					if anchor != "" {
						runs[i].LinkAnchor = anchor
					}
				}
				p.Runs = append(p.Runs, runs...)
			} else {
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func decodeNumPr(dec *xml.Decoder, start xml.StartElement, p *Paragraph) error {
	li := ListInfo{}
	have := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "ilvl":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						li.Level = x
					}
				}
				_ = dec.Skip()
			case "numId":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						li.NumID = x
						have = true
					}
				}
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if have && li.NumID > 0 {
					p.List = &li
				}
				return nil
			}
		}
	}
}

func decodePPr(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr *RunProps, pctx *parseDocContext) error {
	doc := pctx.doc
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pStyle":
				// pStyle is conventionally first in pPr; we apply the named style
				// as the *base* before later siblings override.
				styleID := attr(t, "val")
				if styleID != "" {
					p.StyleID = styleID
					if st, ok := doc.Styles[styleID]; ok {
						*paraRPr = mergeRunProps(*paraRPr, st.Run)
						if st.HasAlignment {
							p.Alignment = st.Alignment
						}
						if st.SpacingBefore != 0 {
							p.SpacingBefore = st.SpacingBefore
						}
						if st.SpacingAfter != 0 {
							p.SpacingAfter = st.SpacingAfter
						}
						if st.LineHeight.Rule != "" {
							p.LineHeight = st.LineHeight
						}
					}
				}
				_ = dec.Skip()
			case "numPr":
				if err := decodeNumPr(dec, t, p); err != nil {
					return err
				}
			case "jc":
				val := attr(t, "val")
				switch val {
				case "center":
					p.Alignment = AlignCenter
				case "right", "end":
					p.Alignment = AlignRight
				case "both", "distribute":
					p.Alignment = AlignJustify
				default:
					p.Alignment = AlignLeft
				}
				_ = dec.Skip()
			case "spacing":
				if v := attr(t, "before"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.SpacingBefore = float64(x) / 20.0 // twips → points
					}
				}
				if v := attr(t, "after"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.SpacingAfter = float64(x) / 20.0
					}
				}
				// w:line + w:lineRule controls inter-line spacing.
				if v := attr(t, "line"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						rule := attr(t, "lineRule")
						if rule == "" {
							rule = "auto"
						}
						p.LineHeight = LineHeight{Rule: rule}
						switch rule {
						case "exact", "atLeast":
							p.LineHeight.Pt = float64(x) / 20.0
						default: // "auto"
							p.LineHeight.Mul = float64(x) / 240.0
						}
					}
				}
				_ = dec.Skip()
			case "pageBreakBefore":
				p.PageBreak = true
				_ = dec.Skip()
			case "keepNext":
				p.KeepNext = true
				_ = dec.Skip()
			case "keepLines":
				p.KeepLines = true
				_ = dec.Skip()
			case "contextualSpacing":
				p.ContextualSpacing = true
				_ = dec.Skip()
			case "bidi":
				p.Bidi = true
				_ = dec.Skip()
			case "tabs":
				if err := decodeTabs(dec, t, p); err != nil {
					return err
				}
			case "framePr":
				if v := attr(t, "dropCap"); v != "" && v != "none" {
					p.DropCap = v
				}
				if v := attr(t, "lines"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						p.DropCapLines = x
					}
				}
				if p.DropCap != "" && p.DropCapLines == 0 {
					p.DropCapLines = 3
				}
				_ = dec.Skip()
			case "sectPr":
				// Inline section break: paragraph belongs to current section
				// (parseDocument appends it before finalizing), section's
				// properties are decoded here, finalize happens after the
				// paragraph is added so it ends up in the correct section.
				if err := decodeSectPr(dec, t, pctx); err != nil {
					return err
				}
				p.endsSection = true
			case "ind":
				// w:ind sets left/right indent and a first-line offset.
				// Word stores firstLine as a positive offset and hanging as a
				// positive value with the opposite sign — we collapse to one
				// signed IndentFirstLinePt (positive = first-line indent,
				// negative = hanging indent).
				if v := attr(t, "left"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentLeftPt = float64(x) / 20.0
					}
				}
				if v := attr(t, "firstLine"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentFirstLinePt = float64(x) / 20.0
					}
				}
				if v := attr(t, "hanging"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentFirstLinePt = -float64(x) / 20.0
					}
				}
				_ = dec.Skip()
			case "rPr":
				var r xmlRPr
				if err := dec.DecodeElement(&r, &t); err != nil {
					return err
				}
				*paraRPr = rPrToProps(r, *paraRPr)
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

// decodeRun returns one or more Run atoms because a single w:r may carry a
// text node, a w:br, and a w:drawing in any order.
func decodeRun(dec *xml.Decoder, start xml.StartElement, paraRPr RunProps, doc *Document) ([]Run, error) {
	rp := paraRPr
	var atoms []Run

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "rPr":
				var r xmlRPr
				if err := dec.DecodeElement(&r, &t); err != nil {
					return nil, err
				}
				rp = rPrToProps(r, paraRPr)
				// Apply the named character style underneath (style props are
				// defaults; whatever this run set explicitly already wins via
				// rPrToProps writing-on-top).
				if rp.StyleID != "" && doc != nil {
					if sp, ok := doc.CharStyles[rp.StyleID]; ok {
						rp = mergeRunProps(sp, rp)
					}
				}
			case "t":
				// xml:space="preserve" matters; encoding/xml preserves it for us.
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				atoms = append(atoms, Run{Text: s, Props: rp})
			case "tab":
				atoms = append(atoms, Run{Text: "\t", Props: rp})
				_ = dec.Skip()
			case "br":
				kind := attr(t, "type")
				if kind == "page" {
					atoms = append(atoms, Run{IsBreak: true, Text: "\f", Props: rp})
				} else {
					atoms = append(atoms, Run{IsBreak: true, Props: rp})
				}
				_ = dec.Skip()
			case "noBreakHyphen":
				// U+2011 NON-BREAKING HYPHEN.
				atoms = append(atoms, Run{Text: "‑", Props: rp})
				_ = dec.Skip()
			case "softHyphen":
				// U+00AD is a break opportunity that's invisible unless the
				// line wraps at it. Implementing proper soft-hyphen wrap
				// requires a more sophisticated line breaker; for now we
				// emit the codepoint (most fonts render it 0-width).
				atoms = append(atoms, Run{Text: "­", Props: rp})
				_ = dec.Skip()
			case "footnoteReference", "endnoteReference":
				// Render as a superscript marker AND tag the run with the
				// note ID so the renderer can place the body at page bottom.
				id := attr(t, "id")
				if id != "" {
					srp := rp
					srp.VertAlign = "superscript"
					atoms = append(atoms, Run{
						Text:       "[" + id + "]",
						Props:      srp,
						FootnoteID: id,
						IsEndnote:  t.Name.Local == "endnoteReference",
					})
				}
				_ = dec.Skip()
			case "sym":
				// <w:sym w:font="Wingdings" w:char="F0E0"/> — code point in the
				// referenced font. Best-effort: convert the hex code to a rune
				// and render with the default font (the symbol font itself is
				// not registered, but for common math/arrow chars in the BMP
				// area the rune is meaningful enough).
				if v := attr(t, "char"); v != "" {
					if cp, err := strconv.ParseUint(v, 16, 32); err == nil {
						atoms = append(atoms, Run{Text: string(rune(cp)), Props: rp})
					}
				}
				_ = dec.Skip()
			case "fldChar":
				switch attr(t, "fldCharType") {
				case "begin":
					atoms = append(atoms, Run{FieldBegin: true, Props: rp})
				case "separate":
					atoms = append(atoms, Run{FieldSep: true, Props: rp})
				case "end":
					atoms = append(atoms, Run{FieldEnd: true, Props: rp})
				}
				_ = dec.Skip()
			case "instrText":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				atoms = append(atoms, Run{InstrText: s, Props: rp})
			case "drawing":
				di, err := findDrawingInfo(dec, t)
				if err != nil {
					return nil, err
				}
				if di.RID != "" {
					atoms = append(atoms, Run{
						ImageID:       di.RID,
						ImageWidthPt:  di.WPt,
						ImageHeightPt: di.HPt,
						CropTopPct:    di.CropT,
						CropBottomPct: di.CropB,
						CropLeftPct:   di.CropL,
						CropRightPct:  di.CropR,
						Props:         rp,
					})
				}
			case "pict":
				// Legacy VML images; skip for now (docx4j supports via fallback).
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return atoms, nil
			}
		}
	}
}

// drawingInfo captures everything we need from a w:drawing subtree: rId,
// optional explicit extent, and optional a:srcRect crop percentages.
type drawingInfo struct {
	RID                        string
	WPt, HPt                   float64
	CropT, CropB, CropL, CropR float64 // pct
}

// findDrawingInfo walks a w:drawing subtree and pulls out the drawing info.
// EMU conversion: 914400 EMU = 1 inch = 72 pt; so pt = EMU / 9525.
// srcRect: each side is in 1/1000ths of a percent (so 10000 = 10%).
func findDrawingInfo(dec *xml.Decoder, start xml.StartElement) (info drawingInfo, err error) {
	depth := 1
	for depth > 0 {
		tok, e := dec.Token()
		if e != nil {
			return info, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "blip":
				for _, a := range t.Attr {
					if a.Name.Local == "embed" {
						info.RID = a.Value
					}
				}
			case "extent":
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "cx":
						if x, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
							info.WPt = float64(x) / 9525.0
						}
					case "cy":
						if x, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
							info.HPt = float64(x) / 9525.0
						}
					}
				}
			case "srcRect":
				parseCrop := func(name string) float64 {
					v := attr(t, name)
					if v == "" {
						return 0
					}
					if x, err := strconv.Atoi(v); err == nil {
						return float64(x) / 1000.0
					}
					return 0
				}
				info.CropT = parseCrop("t")
				info.CropB = parseCrop("b")
				info.CropL = parseCrop("l")
				info.CropR = parseCrop("r")
			}
		case xml.EndElement:
			depth--
		}
	}
	return info, nil
}

// findBlipEmbed is kept for backward compatibility; deprecated wrapper.
func findBlipEmbed(dec *xml.Decoder, start xml.StartElement) (string, error) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "blip" {
				// r:embed is namespace-qualified; we accept any "embed" attr name.
				for _, a := range t.Attr {
					if a.Name.Local == "embed" {
						// consume rest of subtree
						for depth > 1 {
							tok2, err := dec.Token()
							if err != nil {
								return "", err
							}
							switch tok2.(type) {
							case xml.StartElement:
								depth++
							case xml.EndElement:
								depth--
							}
						}
						// Now consume the drawing closing token.
						for depth > 0 {
							tok3, err := dec.Token()
							if err != nil {
								return a.Value, err
							}
							switch tok3.(type) {
							case xml.StartElement:
								depth++
							case xml.EndElement:
								depth--
							}
						}
						return a.Value, nil
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return "", nil
}

func decodeTable(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) (Table, error) {
	tbl := Table{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return tbl, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tblPr":
				if err := decodeTblPr(dec, t, &tbl); err != nil {
					return tbl, err
				}
			case "tblGrid":
				if err := decodeTblGrid(dec, t, &tbl); err != nil {
					return tbl, err
				}
			case "tr":
				row, err := decodeRow(dec, t, pctx)
				if err != nil {
					return tbl, err
				}
				tbl.Rows = append(tbl.Rows, row)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return tbl, nil
			}
		}
	}
}

// decodeTblPr reads the table-level properties block. We only need the
// style reference here — column widths come from tblGrid and per-row/per-cell
// stuff is in trPr/tcPr.
func decodeTblPr(dec *xml.Decoder, start xml.StartElement, tbl *Table) error {
	flagSet := func(a string) bool { return a == "1" || a == "true" }
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tblStyle":
				tbl.StyleID = attr(t, "val")
			case "tblLook":
				// Word can encode either via individual w:firstRow="1" attrs
				// or via a single w:val hex bitmask. Handle both.
				if flagSet(attr(t, "firstRow")) {
					tbl.Look.FirstRow = true
				}
				if flagSet(attr(t, "lastRow")) {
					tbl.Look.LastRow = true
				}
				if flagSet(attr(t, "firstColumn")) {
					tbl.Look.FirstColumn = true
				}
				if flagSet(attr(t, "lastColumn")) {
					tbl.Look.LastColumn = true
				}
				if flagSet(attr(t, "noHBand")) {
					tbl.Look.NoHBand = true
				}
				if flagSet(attr(t, "noVBand")) {
					tbl.Look.NoVBand = true
				}
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func decodeTblGrid(dec *xml.Decoder, start xml.StartElement, tbl *Table) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "gridCol" {
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						tbl.ColumnWidthsTwips = append(tbl.ColumnWidthsTwips, x)
					}
				}
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func decodeRow(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) (TableRow, error) {
	row := TableRow{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return row, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tc":
				cell, err := decodeCell(dec, t, pctx)
				if err != nil {
					return row, err
				}
				row.Cells = append(row.Cells, cell)
			case "trPr":
				// trPr children include <w:tblHeader/> — when present (with
				// w:val absent OR = "1" / "true"), this row repeats on each
				// page the table crosses.
				if err := decodeTrPr(dec, t, &row); err != nil {
					return row, err
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return row, nil
			}
		}
	}
}

func decodeTrPr(dec *xml.Decoder, start xml.StartElement, row *TableRow) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tblHeader":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					row.IsHeader = true
				}
			case "trHeight":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						row.HeightTwips = x
					}
				}
				if attr(t, "hRule") == "exact" {
					row.HeightRuleExact = true
				}
			case "cantSplit":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					row.CantSplit = true
				}
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func decodeCell(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) (TableCell, error) {
	cell := TableCell{GridSpan: 1}
	for {
		tok, err := dec.Token()
		if err != nil {
			return cell, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tcPr":
				if err := decodeTcPr(dec, t, &cell); err != nil {
					return cell, err
				}
			case "p":
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return cell, err
				}
				cell.Blocks = append(cell.Blocks, p)
			case "tbl":
				// Nested table.
				nt, err := decodeTable(dec, t, pctx)
				if err != nil {
					return cell, err
				}
				cell.Blocks = append(cell.Blocks, nt)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return cell, nil
			}
		}
	}
}

func decodeTcPr(dec *xml.Decoder, start xml.StartElement, cell *TableCell) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "gridSpan":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						cell.GridSpan = x
					}
				}
				_ = dec.Skip()
			case "vMerge":
				v := attr(t, "val")
				if v == "" {
					v = "continue"
				}
				cell.VMerge = v
				_ = dec.Skip()
			case "shd":
				if v := attr(t, "fill"); v != "" && v != "auto" {
					cell.Shading = v
				}
				_ = dec.Skip()
			case "vAlign":
				cell.VAlign = attr(t, "val")
				_ = dec.Skip()
			case "tcBorders":
				if err := decodeCellBorders(dec, t, &cell.Borders); err != nil {
					return err
				}
			case "tcMar":
				if err := decodeTcMar(dec, t, cell); err != nil {
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

// decodePgBorders is structurally identical to decodeCellBorders, just over
// a PageBorders target — Word reuses the border-edge XML schema.
func decodePgBorders(dec *xml.Decoder, start xml.StartElement, b *PageBorders) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			edge := BorderEdge{Style: attr(t, "val"), Color: attr(t, "color")}
			if v := attr(t, "sz"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					edge.Sz = float64(x) / 8.0
				}
			}
			switch t.Name.Local {
			case "top":
				b.Top = edge
			case "bottom":
				b.Bottom = edge
			case "left":
				b.Left = edge
			case "right":
				b.Right = edge
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeTcMar reads per-cell margin overrides into the cell.
func decodeTcMar(dec *xml.Decoder, start xml.StartElement, cell *TableCell) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			v := attr(t, "w")
			pt := 0.0
			if x, err := strconv.Atoi(v); err == nil {
				pt = float64(x) / 20.0
			}
			switch t.Name.Local {
			case "top":
				cell.MarginTopPt = pt
			case "bottom":
				cell.MarginBottomPt = pt
			case "left", "start":
				cell.MarginLeftPt = pt
			case "right", "end":
				cell.MarginRightPt = pt
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeCellBorders reads tcBorders { top | bottom | left | right } children.
// Each child element has Style/Sz/Color attrs. We map directly to BorderEdge.
func decodeCellBorders(dec *xml.Decoder, start xml.StartElement, b *CellBorders) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			edge := BorderEdge{
				Style: attr(t, "val"),
				Color: attr(t, "color"),
			}
			if v := attr(t, "sz"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					edge.Sz = float64(x) / 8.0 // Word stores 1/8 pt units
				}
			}
			switch t.Name.Local {
			case "top":
				b.Top = edge
			case "bottom":
				b.Bottom = edge
			case "left", "start":
				b.Left = edge
			case "right", "end":
				b.Right = edge
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func decodeSectPr(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) error {
	sec := &pctx.curSection
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "headerReference":
				kind := attr(t, "type") // "default", "first", "even"
				rid := ""
				for _, a := range t.Attr {
					if a.Name.Local == "id" {
						rid = a.Value
					}
				}
				if blocks, ok := pctx.headerParts[rid]; ok {
					switch kind {
					case "", "default":
						sec.HeaderBlocks = blocks
					case "first":
						sec.HeaderFirstBlocks = blocks
					case "even":
						sec.HeaderEvenBlocks = blocks
						sec.EvenAndOddHeaders = true
					}
				}
				_ = dec.Skip()
			case "footerReference":
				kind := attr(t, "type")
				rid := ""
				for _, a := range t.Attr {
					if a.Name.Local == "id" {
						rid = a.Value
					}
				}
				if blocks, ok := pctx.footerParts[rid]; ok {
					switch kind {
					case "", "default":
						sec.FooterBlocks = blocks
					case "first":
						sec.FooterFirstBlocks = blocks
					case "even":
						sec.FooterEvenBlocks = blocks
						sec.EvenAndOddHeaders = true
					}
				}
				_ = dec.Skip()
			case "titlePg":
				sec.TitlePg = true
				_ = dec.Skip()
			case "type":
				sec.Type = attr(t, "val")
				_ = dec.Skip()
			case "mirrorMargins":
				sec.MirrorMargins = true
				_ = dec.Skip()
			case "bgColor", "displayBackgroundShape":
				// Background color comes via doc-level w:background; this is
				// the section-level toggle to display it.
				_ = dec.Skip()
			case "pgNumType":
				if v := attr(t, "start"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.PageNumber.Start = x
					}
				}
				if v := attr(t, "fmt"); v != "" {
					sec.PageNumber.Fmt = v
				}
				_ = dec.Skip()
			case "pgBorders":
				if err := decodePgBorders(dec, t, &sec.Borders); err != nil {
					return err
				}
			case "lnNumType":
				if v := attr(t, "countBy"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.LineNumbering.CountBy = x
					}
				}
				if v := attr(t, "start"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.LineNumbering.Start = x
					}
				}
				if v := attr(t, "distance"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.LineNumbering.Distance = x
					}
				}
				sec.LineNumbering.Restart = attr(t, "restart")
				_ = dec.Skip()
			case "cols":
				if v := attr(t, "num"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.Columns = x
					}
				}
				if v := attr(t, "space"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.ColumnSpaceTwips = x
					}
				}
				_ = dec.Skip()
			case "pgSz":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.PageSize.WidthTwips = x
					}
				}
				if v := attr(t, "h"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.PageSize.HeightTwips = x
					}
				}
				_ = dec.Skip()
			case "pgMar":
				if v := attr(t, "gutter"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.GutterTwips = x
					}
				}
				if v := attr(t, "top"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.Margins.Top = x
					}
				}
				if v := attr(t, "bottom"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.Margins.Bottom = x
					}
				}
				if v := attr(t, "left"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.Margins.Left = x
					}
				}
				if v := attr(t, "right"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.Margins.Right = x
					}
				}
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

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
