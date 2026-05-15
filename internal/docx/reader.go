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
		Comments:    map[string][]Block{},
		Charts:      map[string]ChartData{},
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
	if f, ok := files["word/settings.xml"]; ok {
		// Settings are best-effort: a malformed settings.xml shouldn't
		// keep the document from rendering. Defaults take over.
		_ = parseSettings(f, &doc.Settings)
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
	if f, ok := files["word/comments.xml"]; ok {
		// Comments share the parseNotes structure: each <w:comment w:id="…">
		// holds inner paragraphs. We reuse the same decoder with the
		// element name "comment".
		if err := parseNotes(f, doc, "comment", doc.Comments); err != nil {
			return nil, fmt.Errorf("comments.xml: %w", err)
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
		case isChartRel(e.Type):
			// Charts live in their own part (word/charts/chartN.xml).
			// parseChartPart pulls structured series/category data
			// for bar/line/pie plots; FlatText falls back to the
			// legacy text concatenation so callers extracting prose
			// from the PDF still see the chart's text content.
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			data, err := parseChartPart(zf)
			if err != nil {
				continue // tolerate malformed chart part
			}
			doc.Charts[rid] = data
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

	// Bookmark state lives at document scope so bookmarkStart in one
	// paragraph and bookmarkEnd in another can still capture the text in
	// between for REF field resolution. Maps are nil when nothing is open.
	bmIDToName map[string]string
	bmActive   map[string]bool
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

// parseSettings reads the leaf flags from word/settings.xml that the
// renderer can consume. Word stores most settings as empty elements where
// presence = true (e.g. <w:evenAndOddHeaders/>); a few carry a w:val.
func parseSettings(f *zip.File, s *Settings) error {
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
		case "evenAndOddHeaders":
			s.EvenAndOddHeaders = true
			_ = dec.Skip()
		case "displayBackgroundShape":
			s.DisplayBackgroundShape = true
			_ = dec.Skip()
		case "defaultTabStop":
			if v := attr(se, "val"); v != "" {
				if x, err := strconv.Atoi(v); err == nil && x > 0 {
					s.DefaultTabStopTwips = x
				}
			}
			_ = dec.Skip()
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
			case "sdt":
				inner, err := decodeBlockSdt(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, inner...)
			case "AlternateContent":
				inner, err := decodeBlockAltContent(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, inner...)
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
		case "sdt":
			inner, err := decodeBlockSdt(dec, se, pctx)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, inner...)
		case "AlternateContent":
			inner, err := decodeBlockAltContent(dec, se, pctx)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, inner...)
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
	PPr   *xmlStylePPr   `xml:"pPr"`
	RPr   *xmlRPr        `xml:"rPr"`
	TblPr *xmlStyleTblPr `xml:"tblPr"`
}

// xmlStyleTblPr captures the table-level properties a w:style of
// type="table" carries. We only care about tblBorders today; the rest
// of tblPr (tblInd, tblCellMar, etc.) is intentionally ignored.
type xmlStyleTblPr struct {
	TblBorders *xmlTblBorders `xml:"tblBorders"`
}

type xmlTblBorders struct {
	Top     *xmlBorderEdge `xml:"top"`
	Bottom  *xmlBorderEdge `xml:"bottom"`
	Left    *xmlBorderEdge `xml:"left"`
	Right   *xmlBorderEdge `xml:"right"`
	InsideH *xmlBorderEdge `xml:"insideH"`
	InsideV *xmlBorderEdge `xml:"insideV"`
}

type xmlBorderEdge struct {
	Val   string `xml:"val,attr"`
	Sz    string `xml:"sz,attr"`
	Color string `xml:"color,attr"`
}

func (e *xmlBorderEdge) toBorderEdge() BorderEdge {
	if e == nil {
		return BorderEdge{}
	}
	out := BorderEdge{Style: e.Val, Color: e.Color}
	if e.Sz != "" {
		if x, err := strconv.Atoi(e.Sz); err == nil {
			out.Sz = float64(x) / 8.0 // Word stores 1/8 pt
		}
	}
	return out
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
		return MergeRunProps(parent, r.Run)
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
		if xs.TblPr != nil && xs.TblPr.TblBorders != nil {
			tb := xs.TblPr.TblBorders
			ts.TableBorders = TableBorders{
				Top:     tb.Top.toBorderEdge(),
				Bottom:  tb.Bottom.toBorderEdge(),
				Left:    tb.Left.toBorderEdge(),
				Right:   tb.Right.toBorderEdge(),
				InsideH: tb.InsideH.toBorderEdge(),
				InsideV: tb.InsideV.toBorderEdge(),
			}
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
	out.Run = MergeRunProps(parent.Run, child.Run)
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

// MergeRunProps overlays a child run-props block on top of a parent block.
// Each field's "set" predicate matches what rPrToProps actually writes.
// Exported because the renderer needs the same logic for table-style
// flattening — keeping one canonical implementation here avoids drift.
func MergeRunProps(parent, child RunProps) RunProps {
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
func isChartRel(t string) bool  { return strings.HasSuffix(t, "/chart") }

// extractChartText reads a chart XML part (chartN.xml) and concatenates
// all CharData found anywhere in the tree. Titles, axis labels, data
// labels, legend entries, and series names all end up as text nodes
// somewhere; concatenating them is rough but preserves the document's
// signal that "the chart says these things". Repeated entries (e.g.
// numeric data labels) may produce noise — acceptable for "the data
// graphic isn't lost".
func extractChartText(f *zip.File) (string, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var sb []byte
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return string(sb), err
		}
		if cd, ok := tok.(xml.CharData); ok {
			s := strings.TrimSpace(string(cd))
			if s == "" {
				continue
			}
			// Insert a single space whenever the accumulator already has
			// content. Chart text bursts come from many disjoint elements
			// (title, axis labels, data labels, series names); without a
			// separator they'd run together as "SalesQ1Q2…".
			if len(sb) > 0 {
				sb = append(sb, ' ')
			}
			sb = append(sb, s...)
		}
	}
	return string(sb), nil
}

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
		case "sdt":
			// Block-level content control — transparent wrapper. Each
			// contained block joins the current section's flow as if it
			// were written directly in the body.
			inner, err := decodeBlockSdt(dec, se, pctx)
			if err != nil {
				return err
			}
			for _, b := range inner {
				pctx.curSection.Blocks = append(pctx.curSection.Blocks, b)
				doc.Body = append(doc.Body, b)
				if pp, ok := b.(Paragraph); ok && pp.endsSection {
					pctx.finalizeSection()
				}
			}
		case "oMathPara":
			// Display math: a body-level equation. Best-effort — extract
			// the visible text and emit it as a centered, italic paragraph.
			// Loses the structural typesetting but keeps the content.
			txt, err := extractMathText(dec, se)
			if err != nil {
				return err
			}
			if txt != "" {
				p := Paragraph{
					Alignment: AlignCenter,
					Runs:      []Run{mathRun(txt, doc.Defaults)},
				}
				pctx.curSection.Blocks = append(pctx.curSection.Blocks, p)
				doc.Body = append(doc.Body, p)
			}
		case "AlternateContent":
			// Markup Compatibility wrapper at body level. Same Choice >
			// Fallback preference as in inline contexts.
			inner, err := decodeBlockAltContent(dec, se, pctx)
			if err != nil {
				return err
			}
			for _, b := range inner {
				pctx.curSection.Blocks = append(pctx.curSection.Blocks, b)
				doc.Body = append(doc.Body, b)
				if pp, ok := b.(Paragraph); ok && pp.endsSection {
					pctx.finalizeSection()
				}
			}
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
	// Base run properties for runs in this paragraph: doc-defaults plus
	// whatever the paragraph's pStyle contributes (pPr/rPr is intentionally
	// excluded — that styles only the paragraph mark glyph).
	paraRPr := doc.Defaults

	// Bookmark state lives on pctx so a bookmark that opens in one paragraph
	// and closes in another still captures all text in between.
	if pctx.bmIDToName == nil {
		pctx.bmIDToName = map[string]string{}
	}
	if pctx.bmActive == nil {
		pctx.bmActive = map[string]bool{}
	}
	bmIDToName := pctx.bmIDToName
	bmActive := pctx.bmActive

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
			case "ins", "moveTo":
				// Tracked-change INSERTION or move-to (the new location of
				// moved content) — accept mode: render child runs as normal.
				if err := decodeWrapper(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "del", "moveFrom":
				// Tracked-change DELETION or move-from (the old location of
				// moved content) — accept mode: drop entirely.
				if err := decodeWrapper(dec, t, &p, paraRPr, pctx, true); err != nil {
					return p, err
				}
			case "smartTag", "customXml":
				// Auto-recognized text / structured-doc wrapper. The wrapper
				// itself has no rendering effect — recurse into its child
				// runs so the contained text isn't lost.
				if err := decodeWrapper(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "sdt":
				// Inline content control: the actual runs live one level
				// deeper, inside <w:sdtContent>. decodeInlineSdt unwraps
				// that and hands the children to decodeWrapper.
				if err := decodeInlineSdt(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "oMath":
				// Inline math equation. Best-effort: pull the visible text
				// out of the subtree and emit it as one italic run.
				txt, err := extractMathText(dec, t)
				if err != nil {
					return p, err
				}
				if txt != "" {
					p.Runs = append(p.Runs, mathRun(txt, paraRPr))
				}
			case "fldSimple":
				// The "simple" form of a field. Its `w:instr` attribute
				// carries the field code; child runs hold the cached
				// result. Expand into the same begin/instr/sep/.../end
				// marker sequence the complex form (fldChar) produces so
				// flattenFields can handle it uniformly downstream.
				if err := decodeFldSimple(dec, t, &p, paraRPr, pctx); err != nil {
					return p, err
				}
			case "AlternateContent":
				// MC wrapper as a direct paragraph child (less common
				// than inside w:r). Treat its Choice/Fallback as
				// providing run atoms that join this paragraph.
				runs, err := decodeRunAltContent(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return p, err
				}
				p.Runs = append(p.Runs, runs...)
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

// decodeWrapper handles transparent paragraph-child wrappers: w:ins / w:del
// (tracked-changes), w:smartTag (auto-recognized text), w:customXml
// (structured document tags). The wrapper itself has no rendering effect —
// we recurse into its child runs (and nested wrappers) and append them to
// the parent paragraph, except in "drop" mode (w:del) where we discard.
//
// Bookmark text capture continues to work because pctx.bmActive is checked
// for every emitted run regardless of nesting depth.
func decodeWrapper(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "r":
				runs, err := decodeRun(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return err
				}
				if drop {
					continue
				}
				p.Runs = append(p.Runs, runs...)
				// Bookmark capture: same logic as decodeParagraph's r branch,
				// so a bookmark spanning wrapped text still captures it.
				if len(pctx.bmActive) > 0 {
					var sb strings.Builder
					for _, rn := range runs {
						sb.WriteString(rn.Text)
					}
					if s := sb.String(); s != "" {
						for name := range pctx.bmActive {
							pctx.doc.Bookmarks[name] += s
						}
					}
				}
			case "hyperlink":
				if drop {
					_ = dec.Skip()
					continue
				}
				if err := decodeHyperlink(dec, t, p, paraRPr, pctx); err != nil {
					return err
				}
			case "ins", "moveTo", "del", "moveFrom", "smartTag", "customXml":
				// Nested wrappers: a delete-flavored wrapper inside an
				// insert-flavored one still drops; parent wins via the drop
				// flag (Word's "accept all" semantics).
				childDrop := drop || t.Name.Local == "del" || t.Name.Local == "moveFrom"
				if err := decodeWrapper(dec, t, p, paraRPr, pctx, childDrop); err != nil {
					return err
				}
			case "sdt":
				// Inline SDT nested inside another wrapper (e.g. a tracked
				// insertion of a content control). Same transparency rule.
				if err := decodeInlineSdt(dec, t, p, paraRPr, pctx, drop); err != nil {
					return err
				}
			case "oMath":
				txt, err := extractMathText(dec, t)
				if err != nil {
					return err
				}
				if drop || txt == "" {
					continue
				}
				p.Runs = append(p.Runs, mathRun(txt, paraRPr))
			case "fldSimple":
				if drop {
					_ = dec.Skip()
					continue
				}
				if err := decodeFldSimple(dec, t, p, paraRPr, pctx); err != nil {
					return err
				}
			case "bookmarkStart":
				id := attr(t, "id")
				name := attr(t, "name")
				if name != "" && pctx.bmIDToName != nil {
					pctx.bmIDToName[id] = name
					pctx.bmActive[name] = true
				}
				_ = dec.Skip()
			case "bookmarkEnd":
				id := attr(t, "id")
				if pctx.bmIDToName != nil {
					if name, ok := pctx.bmIDToName[id]; ok {
						delete(pctx.bmActive, name)
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
						*paraRPr = MergeRunProps(*paraRPr, st.Run)
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
			case "pBdr":
				// <w:pBdr> wraps top/bottom/left/right edge defs that
				// describe the paragraph's own border. Markdown's "---"
				// thematic break is often encoded as an empty
				// paragraph with only the bottom edge set.
				if err := decodeParagraphBorders(dec, t, &p.Borders); err != nil {
					return err
				}
			case "pageBreakBefore":
				// Important: <w:pageBreakBefore w:val="0"/> means
				// EXPLICITLY OFF (used by styles to disable an
				// inherited break). Treating the element as
				// unconditionally-true used to produce one-page-per-
				// heading documents when the source was a markdown
				// export that set val="0" on every heading style.
				p.PageBreak = onOff(t)
				_ = dec.Skip()
			case "keepNext":
				p.KeepNext = onOff(t)
				_ = dec.Skip()
			case "keepLines":
				p.KeepLines = onOff(t)
				_ = dec.Skip()
			case "contextualSpacing":
				p.ContextualSpacing = onOff(t)
				_ = dec.Skip()
			case "bidi":
				p.Bidi = onOff(t)
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
				// Positioned-frame attributes are independent of drop-cap.
				// Build a FrameInfo only when at least one placement
				// attribute is present, so plain drop-cap paragraphs don't
				// get flagged as floating.
				if frameHasPositioning(t) {
					fi := &FrameInfo{
						HAnchor: attr(t, "hAnchor"),
						VAnchor: attr(t, "vAnchor"),
						XAlign:  attr(t, "xAlign"),
						YAlign:  attr(t, "yAlign"),
						Wrap:    attr(t, "wrap"),
						HRule:   attr(t, "hRule"),
					}
					if v := attr(t, "w"); v != "" {
						if x, err := strconv.Atoi(v); err == nil {
							fi.WidthTwips = x
						}
					}
					if v := attr(t, "h"); v != "" {
						if x, err := strconv.Atoi(v); err == nil {
							fi.HeightTwips = x
						}
					}
					if v := attr(t, "x"); v != "" {
						if x, err := strconv.Atoi(v); err == nil {
							fi.XTwips = x
						}
					}
					if v := attr(t, "y"); v != "" {
						if x, err := strconv.Atoi(v); err == nil {
							fi.YTwips = x
						}
					}
					p.Frame = fi
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
				// <w:pPr><w:rPr> styles the paragraph mark glyph (¶)
				// itself; it is NOT a default for the paragraph's runs.
				// ECMA-376: "specifies the run properties that shall be
				// applied to the paragraph mark for the paragraph." We
				// must NOT merge it into paraRPr or every run in the
				// paragraph would inherit it — e.g. a bold pilcrow
				// would faux-bold the body text. Skip the subtree.
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
						rp = MergeRunProps(sp, rp)
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
				// emit the codepoint (most fonts render it 0-width). The
				// escape form is used so the source file doesn't carry a
				// zero-width character that's invisible in editors.
				atoms = append(atoms, Run{Text: "\u00ad", Props: rp})
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
				// Text-box body (wps:txbx) accompanies any image content
				// the drawing also has — both can coexist on one shape.
				// Inline-emit as italic so the reader can distinguish
				// box content from surrounding flow text.
				if di.TxbxText != "" {
					trp := rp
					trp.Italic = true
					atoms = append(atoms, Run{Text: di.TxbxText, Props: trp})
				}
				// Chart reference: surface the pre-extracted labels so
				// the reader sees what the chart said. If the chart part
				// wasn't loadable we still emit a "[Chart]" placeholder.
				if di.ChartRID != "" {
					data := doc.Charts[di.ChartRID]
					if data.HasData() {
						// Emit a chart-bearing run; renderer draws
						// the data graphic via gopdf primitives.
						atoms = append(atoms, Run{
							ChartID:       di.ChartRID,
							ImageWidthPt:  di.WPt,
							ImageHeightPt: di.HPt,
							Props:         rp,
						})
					} else {
						// Unsupported chart type or empty data — keep
						// the legacy "[Chart: …text]" placeholder so
						// the chart's prose survives in pdftotext output.
						trp := rp
						trp.Italic = true
						txt := data.FlatText
						if txt == "" {
							txt = "[Chart]"
						} else {
							txt = "[Chart: " + txt + "]"
						}
						atoms = append(atoms, Run{Text: txt, Props: trp})
					}
				}
			case "pict":
				// Legacy VML images: <w:pict><v:shape style="..."><v:imagedata r:id="..."/>
				// </v:shape></w:pict>. Older Word docs, Excel/Outlook pastes, and
				// some converters still emit these instead of w:drawing.
				vi, err := findPictInfo(dec, t)
				if err != nil {
					return nil, err
				}
				if vi.IsHR {
					// HTML/markdown <hr> separator. Renderer turns this
					// into a horizontal line at the paragraph's position.
					atoms = append(atoms, Run{HorizontalRule: true, Props: rp})
				} else if vi.RID != "" {
					atoms = append(atoms, Run{
						ImageID:       vi.RID,
						ImageWidthPt:  vi.WPt,
						ImageHeightPt: vi.HPt,
						Props:         rp,
					})
				}
			case "AlternateContent":
				// Markup Compatibility wrapper: prefer mc:Choice over
				// mc:Fallback. Common pattern is "new shape via wps in
				// Choice, legacy VML in Fallback" — we want the Choice.
				runs, err := decodeRunAltContent(dec, t, rp, doc)
				if err != nil {
					return nil, err
				}
				atoms = append(atoms, runs...)
			case "object":
				// w:object wraps OLE/embedded content (Excel range, Visio,
				// chart, equation, ...). We can't render the foreign payload,
				// but emitting a marker run beats silently dropping it — at
				// least the reader sees "something was here".
				atoms = append(atoms, Run{
					Text:  "[Embedded object]",
					Props: rp,
				})
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
// optional explicit extent, optional a:srcRect crop percentages, and
// optional text-box / chart references.
type drawingInfo struct {
	RID                        string
	WPt, HPt                   float64
	CropT, CropB, CropL, CropR float64 // pct
	// TxbxText is the concatenated visible text inside a w:txbxContent,
	// if present. Best-effort: structural formatting inside the text box
	// is lost. Empty when the drawing is an image-only or shape-only.
	TxbxText string
	// ChartRID is set when the drawing references a chart part via
	// <c:chart r:id="…">. The renderer looks up Document.Charts[ChartRID]
	// to surface chart labels as plain text. Empty for non-chart drawings.
	ChartRID string
}

// emuPerPt is the OOXML "English Metric Unit" → PostScript point conversion.
// 1 inch = 914400 EMU, 1 pt = 1/72 inch, so 1 pt = 914400/72 = 12700 EMU.
// (The often-seen value 9525 is EMU-per-pixel-at-96dpi, not per-point.)
const emuPerPt = 12700.0

// findDrawingInfo walks a w:drawing subtree and pulls out the drawing info.
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
							info.WPt = float64(x) / emuPerPt
						}
					case "cy":
						if x, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
							info.HPt = float64(x) / emuPerPt
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
			case "chart":
				// <c:chart r:id="…"> inside a drawing — references the
				// chart part. Record the rId; renderer looks up the
				// pre-extracted text in Document.Charts.
				for _, a := range t.Attr {
					if a.Name.Local == "id" {
						info.ChartRID = a.Value
						break
					}
				}
			case "txbxContent":
				// Word text-box body. Real txbxContent holds a tree of
				// w:p/w:r/w:t; we pull out the visible text with a single
				// CharData sweep so the content survives even though
				// internal formatting (bold, lists, tables-in-textboxes)
				// is lost. Whitespace between paragraphs collapses; a
				// single space is inserted to keep adjacent words apart.
				txt, err := extractTxbxText(dec, t)
				if err != nil {
					return info, err
				}
				info.TxbxText = txt
				// extractTxbxText consumed the matching EndElement, so
				// undo the +1 we did at the top of this StartElement
				// branch — otherwise depth never returns to 0.
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return info, nil
}

// extractTxbxText concatenates the visible text inside a w:txbxContent
// subtree. We separate paragraphs with a single space rather than try to
// preserve them as standalone Paragraph blocks — text-box content lives
// inline at a run level in our model, and inline runs don't carry
// paragraph breaks. Inserting whitespace between paragraphs keeps words
// from being run together.
func extractTxbxText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb []byte
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return string(sb), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			// Treat a new w:p as a soft separator: insert a space before
			// its content joins the accumulator.
			if t.Name.Local == "p" && len(sb) > 0 {
				sb = append(sb, ' ')
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			sb = append(sb, t...)
		}
	}
	return string(sb), nil
}

// pictInfo carries the bits we extract from a VML <w:pict> subtree.
type pictInfo struct {
	RID      string
	WPt, HPt float64
	// IsHR marks a "horizontal rule" pict: <v:rect o:hr="t">. Word writes
	// these to represent HTML/markdown <hr> separators. No image data
	// involved — the renderer just draws a horizontal line.
	IsHR bool
}

// findPictInfo walks a w:pict subtree pulling out the embedded image rId
// (from v:imagedata) and the shape's declared size (from v:shape's
// CSS-style "style" attribute: width:1.5in;height:0.75in). Returns an
// empty RID when no image reference is present — caller drops the run.
func findPictInfo(dec *xml.Decoder, start xml.StartElement) (info pictInfo, err error) {
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
			case "shape", "rect", "roundrect", "oval", "line":
				// VML shape carrying the image. Parse style for size.
				if style := attr(t, "style"); style != "" {
					w, h := parseVMLSize(style)
					if w > 0 {
						info.WPt = w
					}
					if h > 0 {
						info.HPt = h
					}
				}
				// Office uses <v:rect o:hr="t"> (sometimes <v:line>) as
				// the HTML/markdown <hr> separator. No imagedata is
				// involved — flagging IsHR is the signal the renderer
				// needs to draw a horizontal line.
				if attr(t, "hr") == "t" {
					info.IsHR = true
				}
			case "imagedata":
				// r:id (or r:relid) references the image part. Accept either
				// since some converters use relid.
				for _, a := range t.Attr {
					if a.Name.Local == "id" || a.Name.Local == "relid" {
						info.RID = a.Value
						break
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return info, nil
}

// parseVMLSize extracts width and height in points from a VML CSS-like
// style string ("width:1.5in;height:0.75in"). Unrecognized units fall back
// to zero so the renderer uses the image's natural size.
func parseVMLSize(style string) (w, h float64) {
	for _, decl := range strings.Split(style, ";") {
		colon := strings.IndexByte(decl, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(decl[:colon]))
		val := strings.TrimSpace(decl[colon+1:])
		switch key {
		case "width":
			w = parseCSSLength(val)
		case "height":
			h = parseCSSLength(val)
		}
	}
	return w, h
}

// parseCSSLength converts a single CSS length token ("1.5in", "100pt",
// "300px") to PostScript points. Returns 0 on failure or unknown unit.
func parseCSSLength(s string) float64 {
	s = strings.TrimSpace(s)
	// Find where the number ends.
	end := 0
	for end < len(s) {
		c := s[end]
		if !(c >= '0' && c <= '9') && c != '.' && c != '-' && c != '+' {
			break
		}
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(s[end:]))
	switch unit {
	case "pt", "":
		return n
	case "in":
		return n * 72
	case "cm":
		return n * 72 / 2.54
	case "mm":
		return n * 72 / 25.4
	case "px":
		return n * 72 / 96
	case "pc": // pica
		return n * 12
	}
	return 0
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
				// Layer in the named tblStyle's borders BEFORE
				// propagating to cells. Word's built-in "TableGrid"
				// style declares its grid lines via the style's
				// tblBorders rather than at the table level; without
				// this layering those tables would render borderless.
				// Per-edge override order: table's own tblBorders
				// (already in tbl.Borders) wins over the style's
				// tblBorders.
				if tbl.StyleID != "" && pctx != nil && pctx.doc != nil {
					if ts, ok := pctx.doc.TableStyles[tbl.StyleID]; ok {
						tbl.Borders = mergeTableBorders(ts.TableBorders, tbl.Borders)
					}
				}
				propagateTableBorders(&tbl)
				return tbl, nil
			}
		}
	}
}

// mergeTableBorders overlays `overlay` on top of `base`: for each edge,
// the overlay value wins if it carries any styling, otherwise the base
// edge survives. Used to layer a table's explicit tblBorders on top of
// the tblStyle's defaults.
func mergeTableBorders(base, overlay TableBorders) TableBorders {
	pick := func(b, o BorderEdge) BorderEdge {
		if o.Has() {
			return o
		}
		return b
	}
	return TableBorders{
		Top:     pick(base.Top, overlay.Top),
		Bottom:  pick(base.Bottom, overlay.Bottom),
		Left:    pick(base.Left, overlay.Left),
		Right:   pick(base.Right, overlay.Right),
		InsideH: pick(base.InsideH, overlay.InsideH),
		InsideV: pick(base.InsideV, overlay.InsideV),
	}
}

// propagateTableBorders fills in each cell's CellBorders from the
// table-level <w:tblBorders> (the outer edges for cells on the table's
// rim, insideH/insideV for cells in the interior). Cells that already
// set their own tcBorders keep them — tcBorders wins over tblBorders
// per OOXML's overlay rules.
//
// Without this, a table that puts a "single 0.5pt" tblBorders at the
// table level and no per-cell tcBorders would render borderless
// (cell.Borders is zero-valued and the renderer treats empty styles as
// "draw nothing"). With this, the renderer's only job is to emit what
// CellBorders says.
func propagateTableBorders(tbl *Table) {
	if !tbl.Borders.Has() {
		return
	}
	nRows := len(tbl.Rows)
	totalCols := len(tbl.ColumnWidthsTwips)
	for ri := range tbl.Rows {
		row := &tbl.Rows[ri]
		colIdx := 0
		for ci := range row.Cells {
			cell := &row.Cells[ci]
			span := cell.GridSpan
			if span < 1 {
				span = 1
			}
			firstRow := ri == 0
			lastRow := ri == nRows-1
			firstCol := colIdx == 0
			lastCol := totalCols > 0 && colIdx+span >= totalCols
			if !cell.Borders.Top.Has() {
				if firstRow {
					cell.Borders.Top = tbl.Borders.Top
				} else {
					cell.Borders.Top = tbl.Borders.InsideH
				}
			}
			if !cell.Borders.Bottom.Has() {
				if lastRow {
					cell.Borders.Bottom = tbl.Borders.Bottom
				} else {
					cell.Borders.Bottom = tbl.Borders.InsideH
				}
			}
			if !cell.Borders.Left.Has() {
				if firstCol {
					cell.Borders.Left = tbl.Borders.Left
				} else {
					cell.Borders.Left = tbl.Borders.InsideV
				}
			}
			if !cell.Borders.Right.Has() {
				if lastCol {
					cell.Borders.Right = tbl.Borders.Right
				} else {
					cell.Borders.Right = tbl.Borders.InsideV
				}
			}
			colIdx += span
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
			case "tblBorders":
				if err := decodeTableBorders(dec, t, &tbl.Borders); err != nil {
					return err
				}
				continue
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
			case "sdt":
				inner, err := decodeBlockSdt(dec, t, pctx)
				if err != nil {
					return cell, err
				}
				cell.Blocks = append(cell.Blocks, inner...)
			case "AlternateContent":
				inner, err := decodeBlockAltContent(dec, t, pctx)
				if err != nil {
					return cell, err
				}
				cell.Blocks = append(cell.Blocks, inner...)
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
// decodeParagraphBorders parses <w:pBdr>'s edge children. Shape matches
// <w:tcBorders> well enough that we share the decoder — paragraph
// borders ALSO accept top/bottom/left/right (plus rarely-used "between"
// and "bar" which we ignore). Used to render markdown's "---" thematic
// break (empty paragraph with just w:bottom) and boxed callouts.
func decodeParagraphBorders(dec *xml.Decoder, start xml.StartElement, b *CellBorders) error {
	return decodeCellBorders(dec, start, b)
}

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

// decodeTableBorders reads <w:tblBorders>'s edge children. Same shape as
// decodeCellBorders but with two extra edges (insideH / insideV) that
// apply between rows and between columns respectively.
func decodeTableBorders(dec *xml.Decoder, start xml.StartElement, b *TableBorders) error {
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
					edge.Sz = float64(x) / 8.0
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
			case "insideH":
				b.InsideH = edge
			case "insideV":
				b.InsideV = edge
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
				// w:distance is intentionally not modeled — see LineNumbering doc.
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

// onOff interprets OOXML's two-valued attribute convention. A property
// element like <w:b/> or <w:pageBreakBefore/> turns its flag ON; the
// SAME element with w:val="0" / "false" / "off" turns it OFF. The
// distinction matters when a style sets the flag and an individual
// paragraph/run wants to disable it ("explicitly not bold despite the
// style being Bold"). Returns true for ON, false for OFF.
//
// Treats `w:val` absent the same as ON, per spec. Common ON values
// ("1", "true", "on") all map to true; everything else is OFF.
func onOff(se xml.StartElement) bool {
	v := attr(se, "val")
	switch v {
	case "", "1", "true", "on", "True", "On":
		return true
	}
	return false
}

// frameHasPositioning reports whether a w:framePr element carries any
// floating-frame placement attributes. We treat the presence of any of
// these as a signal to build a FrameInfo — purely-drop-cap framePrs (which
// only set dropCap / lines) do not need one.
func frameHasPositioning(se xml.StartElement) bool {
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "w", "h", "x", "y",
			"hAnchor", "vAnchor",
			"xAlign", "yAlign",
			"wrap", "hRule":
			return true
		}
	}
	return false
}
