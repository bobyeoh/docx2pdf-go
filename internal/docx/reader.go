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
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
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
		RelTargets:  map[string]string{},
		Styles:      map[string]ParagraphStyle{},
		CharStyles:  map[string]RunProps{},
		TableStyles: map[string]TableStyle{},
		Footnotes:   map[string][]Block{},
		Endnotes:    map[string][]Block{},
		Comments:    map[string][]Block{},
		Charts:      map[string]string{},
		Diagrams:    map[string]string{},
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

	if f, ok := files["word/glossary/document.xml"]; ok {
		doc.HasGlossary = true
		_ = parseGlossaryPart(f, doc)
	}

	if f, ok := files["word/fontTable.xml"]; ok {
		fontRels := map[string]relEntry{}
		if rf, ok := files["word/_rels/fontTable.xml.rels"]; ok {
			_ = parseRels(rf, fontRels)
		}
		_ = parseFontTable(f, fontRels, files, doc)
	}

	if f, ok := files["word/styles.xml"]; ok {
		if err := parseStyles(f, doc); err != nil {
			return nil, fmt.Errorf("styles.xml: %w", err)
		}
	}
	if f, ok := files["word/settings.xml"]; ok {
		// Settings are best-effort.
		_ = parseSettings(f, doc)
	}
	if f, ok := files["docProps/custom.xml"]; ok {
		doc.CustomProperties = map[string]string{}
		_ = parseCustomProperties(f, doc.CustomProperties)
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
		if !e.External {
			doc.RelTargets[rid] = e.Target
		}
		switch {
		case e.External:
			doc.Hyperlink[rid] = e.Target
		case strings.HasPrefix(e.Target, "media/"):
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			if strings.HasSuffix(strings.ToLower(e.Target), ".svg") {
				if img, err := rasterizeSVGAsset(zf); err == nil {
					doc.Images[rid] = img
				} else if doc.UnsupportedMedia == nil {
					doc.UnsupportedMedia = map[string]string{rid: "SVG"}
				} else {
					doc.UnsupportedMedia[rid] = "SVG"
				}
				continue
			}
			if label, ok := vectorMediaFormat(e.Target, zf); ok {
				if doc.UnsupportedMedia == nil {
					doc.UnsupportedMedia = map[string]string{}
				}
				doc.UnsupportedMedia[rid] = label
				continue
			}
			img, err := loadImage(zf)
			if err != nil {
				continue
			}
			doc.Images[rid] = img
		}
	}

	// Custom XML stores + alt-chunk text parts.
	loadCustomXMLParts(rels, files, doc)
	loadAltChunks(rels, files, doc)

	if f, ok := files["word/numbering.xml"]; ok {
		if err := parseNumbering(f, &doc.Numbering); err != nil {
			return nil, fmt.Errorf("numbering.xml: %w", err)
		}
		resolveNumStyleLinks(&doc.Numbering)
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
		if err := parseComments(f, doc); err != nil {
			return nil, fmt.Errorf("comments.xml: %w", err)
		}
	}
	if f, ok := files["word/commentsExtended.xml"]; ok {
		_ = parseCommentsExtended(f, doc)
	}
	if f, ok := files["word/commentsIds.xml"]; ok {
		_ = parseCommentsIds(f, doc)
	}
	if f, ok := files["word/people.xml"]; ok {
		_ = parsePeople(f, doc)
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
			// We try structural parsing first so the renderer can paint a
			// real bar/pie/line graphic; if the chart family isn't one we
			// understand, we still keep the flat text extraction so titles
			// and labels survive in the PDF text stream.
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			txt, err := extractChartText(zf)
			if err == nil && txt != "" {
				doc.Charts[rid] = txt
			}
			data, err := extractChartStruct(zf)
			if err == nil && data.Kind != "" {
				if doc.ChartsData == nil {
					doc.ChartsData = map[string]ChartData{}
				}
				doc.ChartsData[rid] = data
			}
		case isOLEObjectRel(e.Type), isPackageRel(e.Type):
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			// Only attempt Excel extraction. Other OLE servers
			// (Equation.3, Visio.Drawing) are left alone — caller
			// keeps the preview image.
			if strings.HasSuffix(strings.ToLower(e.Target), ".xlsx") ||
				strings.HasSuffix(strings.ToLower(e.Target), ".xlsm") {
				if embed, err := extractExcelEmbed(zf); err == nil && len(embed.Cells) > 0 {
					if doc.OLEEmbeds == nil {
						doc.OLEEmbeds = map[string]ExcelEmbed{}
					}
					doc.OLEEmbeds[rid] = embed
				}
			}
		case isDiagramDataRel(e.Type):
			// SmartArt data part (word/diagrams/dataN.xml). We extract
			// the per-node text first; then try to pair with the sibling
			// drawing part (word/diagrams/drawingN.xml) Word writes when
			// it pre-renders the SmartArt visuals. When the drawing is
			// present we get a real shape tree; otherwise we still
			// surface the flat text.
			full := "word/" + e.Target
			zf, ok := files[full]
			if !ok {
				continue
			}
			txt, err := extractDiagramText(zf)
			if err != nil {
				continue
			}
			doc.Diagrams[rid] = txt
			if drawingZF := diagramSiblingDrawing(files, e.Target); drawingZF != nil {
				if sh, err := extractDiagramDrawing(drawingZF); err == nil && sh != nil {
					if doc.DiagramShapes == nil {
						doc.DiagramShapes = map[string]*VMLShape{}
					}
					doc.DiagramShapes[rid] = sh
				}
			}
			// Stash the SmartArt layout family (cycle / hierarchy / list
			// / pyramid / matrix / process) so the renderer can synthesize
			// the right shape tree when Word didn't pre-render one.
			if layoutZF := diagramSiblingLayout(files, e.Target); layoutZF != nil {
				if kind := extractDiagramLayoutKind(layoutZF); kind != "" {
					if doc.DiagramLayouts == nil {
						doc.DiagramLayouts = map[string]string{}
					}
					doc.DiagramLayouts[rid] = kind
				}
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

	// Bookmark state lives at document scope so bookmarkStart in one
	// paragraph and bookmarkEnd in another can still capture the text in
	// between for REF field resolution. Maps are nil when nothing is open.
	bmIDToName map[string]string
	bmActive   map[string]bool

	// repeatStack tracks the active OpenDoPE od:repeat scope so that
	// nested SDT XPath references resolved during a clone iteration
	// see the correct positional predicate. Top of stack = innermost
	// repeat.
	repeatStack []openDopeRepeatFrame
}

// openDopeRepeatFrame describes one active repeat scope while we're
// cloning an SDT's content. The renderer uses xpathPrefix to detect
// inner XPaths that drill into the repeat's data, and rewrites them so
// the i-th clone reads the i-th match.
type openDopeRepeatFrame struct {
	xpathPrefix string // e.g. "/Root/Items/Item"
	index       int    // 1-based iteration index
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
			case "cs":
				// Complex-script slot (Arabic, Hebrew, Bidi). Word's
				// font-selection algorithm picks this when a run carries
				// w:rFonts/@cs or the script type is RTL.
				if currentGroup != "" {
					theme.Fonts[currentGroup+"Bidi"] = attr(t, "typeface")
					theme.Fonts[currentGroup+"CS"] = attr(t, "typeface")
				}
				_ = dec.Skip()
			case "font":
				// Per-script fallback chain entry, e.g.
				// <a:font script="Arab" typeface="Arial"/>.
				if currentGroup != "" {
					script := attr(t, "script")
					face := attr(t, "typeface")
					if script != "" && face != "" {
						theme.Fonts[currentGroup+"Script:"+script] = face
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

// parseCoreProps pulls Title/Author/Subject/Description/Keywords/Category
// /LastModifiedBy/Revision plus the three Created/Modified/LastPrinted
// timestamps out of docProps/core.xml. The schema is cp:coreProperties
// with mixed dc:/cp:/dcterms: children. Timestamps are W3CDTF
// (RFC 3339-ish) so time.Parse handles them via the time.RFC3339 layout
// with a fallback to a date-only layout.
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
		case "keywords":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Keywords = s
			}
		case "description":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Comments = s
			}
		case "created":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.CreateDate = s
			}
		case "modified":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.ModifyDate = s
			}
		case "lastPrinted":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.PrintDate = s
			}
		}
	}
}

// parseCorePropsDate parses a W3CDTF / RFC3339 timestamp from
// docProps/core.xml. Returns the zero time on parse failure — callers
// then suppress the field rather than emit garbage.
func parseCorePropsDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
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
		case "Manager":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Manager = s
			}
		case "Application":
			if err := dec.DecodeElement(&s, &se); err == nil {
				p.Application = s
			}
		case "TotalTime":
			if err := dec.DecodeElement(&s, &se); err == nil {
				if x, err := strconv.Atoi(s); err == nil {
					p.TotalTime = x
				}
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

// parseSettings reads the leaf flags from word/settings.xml.
func parseSettings(f *zip.File, doc *Document) error {
	s := &doc.Settings
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
		case "autoHyphenation":
			s.AutoHyphenation = onOffValAttr(se)
			_ = dec.Skip()
		case "consecutiveHyphenLimit":
			if v := attr(se, "val"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					s.ConsecutiveHyphenLimit = x
				}
			}
			_ = dec.Skip()
		case "hyphenationZone":
			if v := attr(se, "val"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					s.HyphenationZoneTwips = x
				}
			}
			_ = dec.Skip()
		case "doNotHyphenateCaps":
			s.DoNotHyphenateCaps = onOffValAttr(se)
			_ = dec.Skip()
		case "trackChangeNumbering":
			s.TrackChangeNumbering = onOffValAttr(se)
			_ = dec.Skip()
		case "trackChanges":
			s.TrackChanges = onOffValAttr(se)
			_ = dec.Skip()
		case "mathPr":
			if err := decodeMathPr(dec, se, &s.MathProps); err != nil {
				return err
			}
		case "revisionView":
			// Each attribute defaults to 0 ("show") and is set to 1 to
			// hide. Invert so RevisionView.X == true means "show X".
			s.RevisionView.Markup = attr(se, "markup") != "1"
			s.RevisionView.Comments = attr(se, "comments") != "1"
			s.RevisionView.InsDel = attr(se, "insDel") != "1"
			s.RevisionView.Formatting = attr(se, "formatting") != "1"
			s.RevisionView.InkAnnotations = attr(se, "inkAnnotations") != "1"
			_ = dec.Skip()
		case "strictFirstAndLastChars":
			s.StrictFirstAndLastChars = onOffValAttr(se)
			_ = dec.Skip()
		case "noLineBreaksAfter":
			lang := attr(se, "lang")
			val := attr(se, "val")
			if lang != "" {
				if s.NoLineBreaksAfter == nil {
					s.NoLineBreaksAfter = map[string]string{}
				}
				s.NoLineBreaksAfter[lang] = val
			}
			_ = dec.Skip()
		case "noLineBreaksBefore":
			lang := attr(se, "lang")
			val := attr(se, "val")
			if lang != "" {
				if s.NoLineBreaksBefore == nil {
					s.NoLineBreaksBefore = map[string]string{}
				}
				s.NoLineBreaksBefore[lang] = val
			}
			_ = dec.Skip()
		case "characterSpacingControl":
			s.CharacterSpacingControl = attr(se, "val")
			_ = dec.Skip()
		case "decimalSymbol":
			if v := attr(se, "val"); v != "" {
				s.DecimalSymbol = v
			}
			_ = dec.Skip()
		case "listSeparator":
			if v := attr(se, "val"); v != "" {
				s.ListSeparator = v
			}
			_ = dec.Skip()
		case "compat":
			if err := decodeCompat(dec, se, s); err != nil {
				return err
			}
		case "documentProtection":
			s.DocumentProtection.Edit = attr(se, "edit")
			s.DocumentProtection.Enforcement = attr(se, "enforcement") == "1" || attr(se, "enforcement") == "true"
			s.DocumentProtection.FormatLockdown = attr(se, "formatting") == "1" || attr(se, "formatting") == "true"
			s.DocumentProtection.AlgorithmName = attr(se, "cryptAlgorithmName")
			s.DocumentProtection.CryptProviderTyp = attr(se, "cryptProviderType")
			_ = dec.Skip()
		case "writeProtection":
			s.WriteProtection.Recommended = attr(se, "recommended") == "1" || attr(se, "recommended") == "true"
			s.WriteProtection.Enforcement = attr(se, "enforcement") == "1" || attr(se, "enforcement") == "true"
			s.WriteProtection.AlgorithmName = attr(se, "cryptAlgorithmName")
			s.WriteProtection.CryptProviderTyp = attr(se, "cryptProviderType")
			_ = dec.Skip()
		case "docVars":
			if doc.DocVars == nil {
				doc.DocVars = map[string]string{}
			}
			if err := decodeDocVars(dec, se, doc.DocVars); err != nil {
				return err
			}
		case "mailMerge":
			depth := 1
			for depth > 0 {
				tok, err := dec.Token()
				if err != nil {
					return err
				}
				switch t := tok.(type) {
				case xml.StartElement:
					depth++
					if t.Name.Local == "mainDocumentType" {
						if v := attr(t, "val"); v != "" {
							doc.MailMerge = v
						} else {
							doc.MailMerge = "formLetter"
						}
					}
				case xml.EndElement:
					depth--
				}
			}
			if doc.MailMerge == "" {
				doc.MailMerge = "formLetter"
			}
		}
	}
}

// parseNotes parses word/footnotes.xml or word/endnotes.xml. Each
// w:footnote / w:endnote element has w:id and contains paragraphs.
// Separator notes (w:type="separator", "continuationSeparator",
// "continuationNotice") are captured into Document.FootnoteSeparators /
// EndnoteSeparators so the renderer can honor any custom separator the
// document provides; comments don't use separator semantics.
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
		if kind == "separator" || kind == "continuationSeparator" || kind == "continuationNotice" {
			blocks, err := parseNoteBody(dec, se, pctx)
			if err != nil {
				return err
			}
			switch elemName {
			case "footnote":
				if doc.FootnoteSeparators == nil {
					doc.FootnoteSeparators = map[string][]Block{}
				}
				doc.FootnoteSeparators[kind] = blocks
			case "endnote":
				if doc.EndnoteSeparators == nil {
					doc.EndnoteSeparators = map[string][]Block{}
				}
				doc.EndnoteSeparators[kind] = blocks
			}
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
			case "permStart":
				recordPermStart(pctx, t)
				_ = dec.Skip()
			case "permEnd":
				_ = dec.Skip()
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

// recordPermStart stores a w:permStart range marker on the doc's
// PermissionRanges map. The companion w:permEnd carries only the id and
// is a no-op for AST capture purposes.
func recordPermStart(pctx *parseDocContext, t xml.StartElement) {
	if pctx == nil || pctx.doc == nil {
		return
	}
	var pr PermissionRange
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "id":
			pr.ID = a.Value
		case "edGrp":
			pr.EditorGroup = a.Value
		case "ed":
			pr.Editor = a.Value
		}
	}
	if pr.ID == "" {
		return
	}
	if pctx.doc.PermissionRanges == nil {
		pctx.doc.PermissionRanges = map[string]PermissionRange{}
	}
	pctx.doc.PermissionRanges[pr.ID] = pr
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

// vectorMediaFormat detects EMF and WMF metafiles by extension or magic
// bytes. Returns a label for the placeholder. The label includes the
// intrinsic dimensions extracted from the file header when available so
// the placeholder reads "EMF 320×240" rather than just "EMF" — this
// preserves at least the layout shape even when the file's pixels are
// unreachable.
func vectorMediaFormat(target string, zf *zip.File) (string, bool) {
	lower := strings.ToLower(target)
	rc, err := zf.Open()
	if err == nil {
		defer rc.Close()
	}
	var head [40]byte
	if rc != nil {
		_, _ = io.ReadFull(rc, head[:])
	}
	kind := ""
	switch {
	case strings.HasSuffix(lower, ".emf"),
		(head[0] == 0x01 && head[1] == 0x00 && head[2] == 0x00 && head[3] == 0x00 && head[40-1] != 0 || false):
		// Light heuristic: EMR_HEADER record type 1 + size in next dword.
		if strings.HasSuffix(lower, ".emf") || (head[0] == 0x01 && head[1] == 0x00 && head[2] == 0x00 && head[3] == 0x00) {
			kind = "EMF"
		}
	case strings.HasSuffix(lower, ".wmf"),
		head[0] == 0xD7 && head[1] == 0xCD && head[2] == 0xC6 && head[3] == 0x9A,
		head[0] == 0x01 && head[1] == 0x00 && head[2] == 0x09 && head[3] == 0x00:
		kind = "WMF"
	}
	if kind == "" {
		return "", false
	}
	if w, h, ok := emfWmfDimensions(kind, head[:]); ok {
		return kind + " " + strconv.Itoa(w) + "×" + strconv.Itoa(h), true
	}
	return kind, true
}

// emfWmfDimensions extracts intrinsic pixel dimensions from an EMF or WMF
// file header. For EMF the bounds rect is in the EMR_HEADER record
// (offset 8..23, four 32-bit signed integers: left/top/right/bottom in
// device units). For WMF placeable files the dimensions are at offset
// 6..13. Returns (0, 0, false) on parse error or non-placeable WMF.
func emfWmfDimensions(kind string, head []byte) (int, int, bool) {
	read32 := func(off int) int32 {
		if off+4 > len(head) {
			return 0
		}
		return int32(head[off]) | int32(head[off+1])<<8 | int32(head[off+2])<<16 | int32(head[off+3])<<24
	}
	read16 := func(off int) int16 {
		if off+2 > len(head) {
			return 0
		}
		return int16(head[off]) | int16(head[off+1])<<8
	}
	switch kind {
	case "EMF":
		// Bounds rect immediately follows the 8-byte EMR_HEADER prefix.
		left := int(read32(8))
		top := int(read32(12))
		right := int(read32(16))
		bottom := int(read32(20))
		w, h := right-left, bottom-top
		if w > 0 && h > 0 {
			return w, h, true
		}
	case "WMF":
		// Placeable WMF: header is 22 bytes, with a BoundingBox at offset 6.
		if head[0] == 0xD7 && head[1] == 0xCD {
			left := int(read16(6))
			top := int(read16(8))
			right := int(read16(10))
			bottom := int(read16(12))
			w, h := right-left, bottom-top
			if w > 0 && h > 0 {
				return w, h, true
			}
		}
	}
	return 0, 0, false
}

// resolveNumStyleLinks rewrites every abstractNum that carries a
// w:numStyleLink so it borrows the levels of the linked abstractNum.
func resolveNumStyleLinks(n *Numbering) {
	if len(n.Abstract) == 0 {
		return
	}
	byStyleLink := map[string]int{}
	for id, an := range n.Abstract {
		if an.StyleLink != "" {
			byStyleLink[an.StyleLink] = id
		}
	}
	for id, an := range n.Abstract {
		if an.NumStyleLink == "" || len(an.Levels) > 0 {
			continue
		}
		target, ok := byStyleLink[an.NumStyleLink]
		if !ok || target == id {
			continue
		}
		ref := n.Abstract[target]
		an.Levels = map[int]NumLevel{}
		for k, v := range ref.Levels {
			an.Levels[k] = v
		}
		n.Abstract[id] = an
	}
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
	// LatentStyles captures w:latentStyles — the document-level table that
	// Word writes to remember UI metadata (uiPriority, qFormat, semiHidden,
	// hidden, unhideWhenUsed, locked) for built-in styles that aren't
	// otherwise instantiated in this document.
	LatentStyles *xmlLatentStyles `xml:"latentStyles"`
	Styles       []xmlStyle       `xml:"style"`
}

type xmlLatentStyles struct {
	DefLockedState    string            `xml:"defLockedState,attr"`
	DefUIPriority     string            `xml:"defUIPriority,attr"`
	DefSemiHidden     string            `xml:"defSemiHidden,attr"`
	DefUnhideWhenUsed string            `xml:"defUnhideWhenUsed,attr"`
	DefQFormat        string            `xml:"defQFormat,attr"`
	Count             string            `xml:"count,attr"`
	Exceptions        []xmlLsdException `xml:"lsdException"`
}

type xmlLsdException struct {
	Name           string `xml:"name,attr"`
	UIPriority     string `xml:"uiPriority,attr"`
	SemiHidden     string `xml:"semiHidden,attr"`
	UnhideWhenUsed string `xml:"unhideWhenUsed,attr"`
	QFormat        string `xml:"qFormat,attr"`
	Locked         string `xml:"locked,attr"`
	PrimaryStyle   string `xml:"primaryStyle,attr"`
}

type xmlStyle struct {
	Type        string `xml:"type,attr"`
	StyleID     string `xml:"styleId,attr"`
	Default     string `xml:"default,attr"`
	CustomStyle string `xml:"customStyle,attr"`
	Name        *struct {
		Val string `xml:"val,attr"`
	} `xml:"name"`
	Aliases *struct {
		Val string `xml:"val,attr"`
	} `xml:"aliases"`
	BasedOn *struct {
		Val string `xml:"val,attr"`
	} `xml:"basedOn"`
	Next *struct {
		Val string `xml:"val,attr"`
	} `xml:"next"`
	Link *struct {
		Val string `xml:"val,attr"`
	} `xml:"link"`
	UIPriority *struct {
		Val string `xml:"val,attr"`
	} `xml:"uiPriority"`
	Hidden         *struct{}      `xml:"hidden"`
	SemiHidden     *struct{}      `xml:"semiHidden"`
	UnhideWhenUsed *struct{}      `xml:"unhideWhenUsed"`
	QFormat        *struct{}      `xml:"qFormat"`
	Locked         *struct{}      `xml:"locked"`
	PPr            *xmlStylePPr   `xml:"pPr"`
	RPr            *xmlRPr        `xml:"rPr"`
	TblPr          *xmlStyleTblPr `xml:"tblPr"`
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
	NumPr *struct {
		Ilvl *struct {
			Val string `xml:"val,attr"`
		} `xml:"ilvl"`
		NumID *struct {
			Val string `xml:"val,attr"`
		} `xml:"numId"`
	} `xml:"numPr"`
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

	// latentStyles: capture UI metadata for styles that aren't materialized.
	if s.LatentStyles != nil {
		ls := LatentStylesInfo{Exceptions: map[string]LatentStyle{}}
		ls.DefLockedState = s.LatentStyles.DefLockedState == "1" || s.LatentStyles.DefLockedState == "true"
		if v, err := strconv.Atoi(s.LatentStyles.DefUIPriority); err == nil {
			ls.DefUIPriority = v
		}
		ls.DefSemiHidden = s.LatentStyles.DefSemiHidden == "1"
		ls.DefUnhideWhenUsed = s.LatentStyles.DefUnhideWhenUsed == "1"
		ls.DefQFormat = s.LatentStyles.DefQFormat == "1"
		if v, err := strconv.Atoi(s.LatentStyles.Count); err == nil {
			ls.Count = v
		}
		for _, ex := range s.LatentStyles.Exceptions {
			le := LatentStyle{Name: ex.Name}
			if v, err := strconv.Atoi(ex.UIPriority); err == nil {
				le.UIPriority = v
			} else {
				le.UIPriority = ls.DefUIPriority
			}
			le.SemiHidden = ex.SemiHidden == "1" || (ex.SemiHidden == "" && ls.DefSemiHidden)
			le.UnhideWhenUsed = ex.UnhideWhenUsed == "1" || (ex.UnhideWhenUsed == "" && ls.DefUnhideWhenUsed)
			le.QFormat = ex.QFormat == "1" || (ex.QFormat == "" && ls.DefQFormat)
			le.Locked = ex.Locked == "1" || (ex.Locked == "" && ls.DefLockedState)
			le.PrimaryStyle = ex.PrimaryStyle == "1"
			ls.Exceptions[ex.Name] = le
		}
		doc.LatentStyles = ls
	}

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
		if xs.Link != nil {
			ps.LinkedCharStyle = xs.Link.Val
		}
		if xs.Name != nil {
			ps.Name = xs.Name.Val
		}
		if xs.Aliases != nil {
			ps.Aliases = xs.Aliases.Val
		}
		if xs.Next != nil {
			ps.NextStyleID = xs.Next.Val
		}
		ps.IsDefault = xs.Default == "1" || xs.Default == "true"
		ps.IsCustom = xs.CustomStyle == "1" || xs.CustomStyle == "true"
		if xs.UIPriority != nil {
			if v, err := strconv.Atoi(xs.UIPriority.Val); err == nil {
				ps.UIPriority = v
			}
		}
		ps.Hidden = xs.Hidden != nil
		ps.SemiHidden = xs.SemiHidden != nil
		ps.UnhideWhenUsed = xs.UnhideWhenUsed != nil
		ps.QFormat = xs.QFormat != nil
		ps.Locked = xs.Locked != nil
		if xs.RPr != nil {
			ps.Run = rPrToProps(*xs.RPr, RunProps{})
		}
		if xs.PPr != nil {
			if xs.PPr.NumPr != nil {
				if xs.PPr.NumPr.NumID != nil {
					if x, err := strconv.Atoi(xs.PPr.NumPr.NumID.Val); err == nil && x > 0 {
						ps.NumPr.NumID = x
					}
				}
				if xs.PPr.NumPr.Ilvl != nil {
					if x, err := strconv.Atoi(xs.PPr.NumPr.Ilvl.Val); err == nil {
						ps.NumPr.Level = x
					}
				}
			}
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
	if child.LinkedCharStyle != "" {
		out.LinkedCharStyle = child.LinkedCharStyle
	}
	if child.NumPr.NumID != 0 {
		out.NumPr = child.NumPr
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
	if child.UnderlineStyle != "" {
		out.UnderlineStyle = child.UnderlineStyle
	}
	if child.Strike {
		out.Strike = true
	}
	if child.DStrike {
		out.DStrike = true
	}
	if child.W14Has3D {
		out.W14Has3D = true
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

// isDiagramDataRel matches the dgm:relIds "r:dm" relationship — the
// SmartArt data part (word/diagrams/dataN.xml) carries the node text
// graph. Layout and styles live in separate parts (lo/qs/cs) we don't
// consume yet.
func isDiagramDataRel(t string) bool {
	return strings.HasSuffix(t, "/diagramData") || strings.HasSuffix(t, "/diagram/dataModel")
}

// isOLEObjectRel matches the w:object OLE blob relationship.
func isOLEObjectRel(t string) bool {
	return strings.HasSuffix(t, "/oleObject")
}

// isPackageRel matches a generic /package OOXML embedded package — Word
// uses this for embedded .xlsx / .docx / .pptx attached via w:object.
func isPackageRel(t string) bool {
	return strings.HasSuffix(t, "/package")
}

// extractDiagramText reads a SmartArt data part
// (word/diagrams/dataN.xml). The data graph carries one <dgm:pt> per
// node; each node's visible text lives in <dgm:t> descendants. We
// collect them in document order and join with " → " so the diagram's
// conceptual ordering survives even though we don't render the
// graphical shapes.
//
// Presentation nodes (dgm:pt @type="pres") and parent/non-visible
// nodes are filtered out — they're algorithmic scaffolding, not
// user-authored content.
func extractDiagramText(f *zip.File) (string, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var nodes []string
	var skipNode bool
	var inT bool
	var nodeBuf strings.Builder
	finishNode := func() {
		s := strings.TrimSpace(nodeBuf.String())
		if s != "" && !skipNode {
			nodes = append(nodes, s)
		}
		nodeBuf.Reset()
		skipNode = false
		inT = false
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return strings.Join(nodes, " → "), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pt":
				// New node — drop any previous unfinished content.
				nodeBuf.Reset()
				skipNode = false
				inT = false
				// Skip presentation scaffolding nodes.
				if v := attr(t, "type"); v == "pres" || v == "parTrans" || v == "sibTrans" {
					skipNode = true
				}
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "pt":
				finishNode()
			case "t":
				inT = false
				if nodeBuf.Len() > 0 {
					nodeBuf.WriteByte(' ')
				}
			}
		case xml.CharData:
			if inT && !skipNode {
				nodeBuf.Write(t)
			}
		}
	}
	return strings.Join(nodes, " → "), nil
}

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
			// the image. We walk the subtree looking for any rId reference,
			// then store it in Numbering.PicBullets so a level whose
			// w:lvlPicBulletId names this id renders the image as its marker.
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
			absID, overrides, err := decodeNum(dec, se)
			if err != nil {
				return err
			}
			if absID >= 0 {
				out.NumToAbs[numID] = absID
			}
			if len(overrides) > 0 {
				if out.Overrides == nil {
					out.Overrides = map[int]map[int]NumOverride{}
				}
				out.Overrides[numID] = overrides
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
			switch t.Name.Local {
			case "lvl":
				ilvl, _ := strconv.Atoi(attr(t, "ilvl"))
				lv, err := decodeLevel(dec, t)
				if err != nil {
					return an, err
				}
				an.Levels[ilvl] = lv
			case "styleLink":
				an.StyleLink = attr(t, "val")
				_ = dec.Skip()
			case "numStyleLink":
				an.NumStyleLink = attr(t, "val")
				_ = dec.Skip()
			case "multiLevelType":
				an.MultiLevelType = attr(t, "val")
				_ = dec.Skip()
			case "tmpl":
				an.Tmpl = attr(t, "val")
				_ = dec.Skip()
			case "nsid":
				an.Nsid = attr(t, "val")
				_ = dec.Skip()
			case "name":
				an.Name = attr(t, "val")
				_ = dec.Skip()
			default:
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
	lv := NumLevel{Start: 1, LvlRestart: -1}
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
				if lv.Format == "custom" {
					lv.CustomFmt = attr(t, "format")
				}
				_ = dec.Skip()
			case "hideParent":
				lv.HideParent = true
				_ = dec.Skip()
			case "legacy":
				if v := attr(t, "legacyIndent"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.LegacyIndent = x
					}
				}
				if lv.LegacyIndent == 0 {
					lv.LegacyIndent = -1 // mark presence even with no indent
				}
				_ = dec.Skip()
			case "lvlText":
				lv.Text = attr(t, "val")
				_ = dec.Skip()
			case "suff":
				lv.Suff = attr(t, "val")
				_ = dec.Skip()
			case "lvlRestart":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						lv.LvlRestart = x
					}
				}
				_ = dec.Skip()
			case "pStyle":
				lv.PStyleLink = attr(t, "val")
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
			case "lvlJc":
				lv.MarkerJc = attr(t, "val")
				_ = dec.Skip()
			case "rPr":
				// We only need the w:rFonts hint for bullet glyph
				// rendering — skip everything else.
				if err := decodeLevelRPr(dec, t, &lv); err != nil {
					return lv, err
				}
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

// decodeLevelRPr extracts only the bullet-font hint from a level's
// w:rPr. Most attributes inside a level rPr (color, size, bold) are
// irrelevant to PDF list-marker rendering — those are inherited from the
// paragraph's run properties — but the font family matters because legacy
// bullets are private-area Symbol / Wingdings glyphs that fall back to
// tofu in the default text font.
func decodeLevelRPr(dec *xml.Decoder, start xml.StartElement, lv *NumLevel) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "rFonts" {
				// Prefer w:ascii, fall back to w:hAnsi, then w:cs.
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "ascii", "hAnsi":
						if lv.MarkerFontFamily == "" {
							lv.MarkerFontFamily = a.Value
						}
					case "cs":
						if lv.MarkerFontFamily == "" {
							lv.MarkerFontFamily = a.Value
						}
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

// decodeNum returns the abstractNumId pointed to by this w:num, and any
// per-num w:lvlOverride entries the caller will key by numId.
func decodeNum(dec *xml.Decoder, start xml.StartElement) (int, map[int]NumOverride, error) {
	abs := -1
	overrides := map[int]NumOverride{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return abs, overrides, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "abstractNumId":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						abs = x
					}
				}
				_ = dec.Skip()
			case "lvlOverride":
				ilvl := -1
				if v := attr(t, "ilvl"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ilvl = x
					}
				}
				ov, err := decodeLvlOverride(dec, t)
				if err != nil {
					return abs, overrides, err
				}
				if ilvl >= 0 {
					overrides[ilvl] = ov
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return abs, overrides, nil
			}
		}
	}
}

// decodeLvlOverride parses a single <w:lvlOverride> child of a w:num.
// Recognized children: w:startOverride, w:lvl (full replacement).
func decodeLvlOverride(dec *xml.Decoder, start xml.StartElement) (NumOverride, error) {
	ov := NumOverride{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return ov, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "startOverride":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ov.StartOverride = x
					}
				}
				_ = dec.Skip()
			case "lvl":
				lv, err := decodeLevel(dec, t)
				if err != nil {
					return ov, err
				}
				ov.LvlReplace = &lv
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return ov, nil
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
	BCs       *struct{}   `xml:"bCs"` // complex-script bold
	I         *struct{}   `xml:"i"`
	ICs       *struct{}   `xml:"iCs"`
	U         *xmlValAttr `xml:"u"`
	Strike    *struct{}   `xml:"strike"`
	DStrike   *struct{}   `xml:"dstrike"` // double strikethrough
	Caps      *struct{}   `xml:"caps"`
	SmallCaps *struct{}   `xml:"smallCaps"`
	Vanish    *struct{}   `xml:"vanish"`
	// SpecVanish marks the run as hidden until a specific TOC/INDEX
	// build-step expands it (Word uses it for "Mark Entry" markers).
	// At render time it behaves the same as vanish — the marker text
	// stays out of the body and only appears when the field that
	// consumes it is laid out.
	SpecVanish *struct{}   `xml:"specVanish"`
	WebHidden  *struct{}   `xml:"webHidden"`
	NoProof    *struct{}   `xml:"noProof"`
	CS         *struct{}   `xml:"cs"`  // complex-script run
	RTL        *struct{}   `xml:"rtl"` // right-to-left
	Emboss     *struct{}   `xml:"emboss"`
	Imprint    *struct{}   `xml:"imprint"`
	Outline    *struct{}   `xml:"outline"`
	Em         *xmlValAttr `xml:"em"`     // CJK emphasis mark
	Effect     *xmlValAttr `xml:"effect"` // animation effect
	Sz         *xmlValAttr `xml:"sz"`     // half-points
	SzCs       *xmlValAttr `xml:"szCs"`
	Kern       *xmlValAttr `xml:"kern"` // half-points threshold
	Color      *struct {
		Val        string `xml:"val,attr"`
		ThemeColor string `xml:"themeColor,attr"`
		ThemeShade string `xml:"themeShade,attr"` // hex 00-FF: darken (lumMod = hex/255)
		ThemeTint  string `xml:"themeTint,attr"`  // hex 00-FF: lighten (lumOff = (255-hex)/255)
	} `xml:"color"`
	Highlight *xmlValAttr `xml:"highlight"` // named: yellow, green, ...
	Shd       *struct {
		Fill string `xml:"fill,attr"`
	} `xml:"shd"`
	Bdr *struct {
		Val   string `xml:"val,attr"`
		Sz    string `xml:"sz,attr"`
		Color string `xml:"color,attr"`
	} `xml:"bdr"`
	Lang *struct {
		Val      string `xml:"val,attr"`
		EastAsia string `xml:"eastAsia,attr"`
		Bidi     string `xml:"bidi,attr"`
	} `xml:"lang"`
	FitText *struct {
		ID  string `xml:"id,attr"`
		Val string `xml:"val,attr"` // width in twips
	} `xml:"fitText"`
	VertAlign *xmlValAttr `xml:"vertAlign"` // "superscript" | "subscript"
	Position  *xmlValAttr `xml:"position"`  // half-points; +up / -down
	WAttr     *xmlValAttr `xml:"w"`         // character scale percent (100 = normal)
	Spacing   *xmlValAttr `xml:"spacing"`   // letter spacing in 1/20 pt
	RFonts    *struct {
		ASCII      string `xml:"ascii,attr"`
		HAnsi      string `xml:"hAnsi,attr"`
		EA         string `xml:"eastAsia,attr"`
		CS         string `xml:"cs,attr"`
		AsciiTheme string `xml:"asciiTheme,attr"`
		HAnsiTheme string `xml:"hAnsiTheme,attr"`
		EATheme    string `xml:"eastAsiaTheme,attr"`
	} `xml:"rFonts"`
	EALayout *struct {
		ID       string `xml:"id,attr"`
		Combine  string `xml:"combine,attr"`
		Brackets string `xml:"combineBrackets,attr"`
		Vert     string `xml:"vert,attr"`
		VertC    string `xml:"vertCompress,attr"`
	} `xml:"eastAsianLayout"`
	W14Ligatures *struct {
		Val string `xml:"val,attr"`
	} `xml:"ligatures"`
	W14NumForm *struct {
		Val string `xml:"val,attr"`
	} `xml:"numForm"`
	W14NumSpacing *struct {
		Val string `xml:"val,attr"`
	} `xml:"numSpacing"`
	W14CntxtAlts   *xmlValAttr `xml:"cntxtAlts"`
	W14Shadow      *xmlW14Fill `xml:"shadow"`
	W14TextOutline *xmlW14Fill `xml:"textOutline"`
	// W14Scene3D / W14Props3D mark the run as having WordprocessingML
	// 2010+ 3D text effects. We capture only presence (Word stores a
	// rich scene/material tree we can't actually project in 2D); the
	// renderer paints a depth-shadow approximation when present.
	W14Scene3D *struct{} `xml:"scene3d"`
	W14Props3D *struct{} `xml:"props3d"`
	// RPrChange records w:rPrChange — tracked-change of run properties.
	// We capture author/id/date attributes; the inner <w:rPr> is the
	// pre-edit history and is intentionally ignored.
	RPrChange *xmlPrChangeAttrs `xml:"rPrChange"`
}

// xmlPrChangeAttrs is the shared *PrChange attribute carrier (id, author,
// date) used by w:rPrChange / w:sectPrChange / w:tblPrChange / w:tcPrChange /
// w:trPrChange / w:tblGridChange.
type xmlPrChangeAttrs struct {
	ID     string `xml:"id,attr"`
	Author string `xml:"author,attr"`
	Date   string `xml:"date,attr"`
}

// toPrChange returns a *PrChange copy, or nil if attrs is nil.
func (x *xmlPrChangeAttrs) toPrChange(kind string) *PrChange {
	if x == nil {
		return nil
	}
	return &PrChange{Kind: kind, ID: x.ID, Author: x.Author, Date: x.Date}
}

// readPrChangeAttrs pulls (id, author, date) from a *Change element start
// token without consuming its body. Caller is responsible for skipping
// the body afterwards.
func readPrChangeAttrs(t xml.StartElement, kind string) *PrChange {
	pc := &PrChange{Kind: kind}
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "id":
			pc.ID = a.Value
		case "author":
			pc.Author = a.Value
		case "date":
			pc.Date = a.Value
		}
	}
	return pc
}

// readPrChangeWithSnapshot is readPrChangeAttrs plus a serialized
// snapshot of the pre-change property tree (the children of the
// w:*Change element). Drives PrChange.SnapshotXML so audit consumers
// can reconstruct what changed. The decoder is positioned at `start`
// and is advanced through the matching end-element on return.
func readPrChangeWithSnapshot(dec *xml.Decoder, t xml.StartElement, kind string) (*PrChange, error) {
	pc := readPrChangeAttrs(t, kind)
	var b strings.Builder
	enc := xml.NewEncoder(&strBuilderWriter{&b})
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return pc, err
		}
		if err := enc.EncodeToken(tok); err != nil {
			return pc, err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	if err := enc.Flush(); err != nil {
		return pc, err
	}
	// The encoder appends the matching end-tag, so trim it back off.
	pc.SnapshotXML = strings.TrimSpace(b.String())
	return pc, nil
}

// xmlW14Fill captures the w14:solidFill > w14:srgbClr nested chain that
// Word 2010+ uses for text-effect colors.
type xmlW14Fill struct {
	SolidFill *struct {
		SrgbClr *struct {
			Val string `xml:"val,attr"`
		} `xml:"srgbClr"`
	} `xml:"solidFill"`
}

func (f *xmlW14Fill) color() string {
	if f == nil || f.SolidFill == nil || f.SolidFill.SrgbClr == nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToUpper(f.SolidFill.SrgbClr.Val), "#")
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
		p.UnderlineStyle = r.U.Val
	}
	if r.Strike != nil {
		p.Strike = true
	}
	if r.DStrike != nil {
		p.DStrike = true
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
	if r.Vanish != nil || r.SpecVanish != nil {
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
	if r.W14Scene3D != nil || r.W14Props3D != nil {
		p.W14Has3D = true
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
	if r.Em != nil && r.Em.Val != "" && r.Em.Val != "none" {
		p.Em = r.Em.Val
	}
	if r.NoProof != nil {
		p.NoProof = true
	}
	if r.WebHidden != nil {
		p.WebHidden = true
	}
	if r.RTL != nil {
		p.RTL = true
	}
	if r.CS != nil {
		p.CS = true
	}
	if r.BCs != nil {
		p.BCs = true
	}
	if r.ICs != nil {
		p.ICs = true
	}
	if r.SzCs != nil && r.SzCs.Val != "" {
		if hp, err := strconv.ParseFloat(r.SzCs.Val, 64); err == nil {
			p.SzCs = hp / 2
		}
	}
	if r.Kern != nil && r.Kern.Val != "" {
		if hp, err := strconv.ParseFloat(r.Kern.Val, 64); err == nil {
			p.KernThresholdPt = hp / 2
		}
	}
	if r.FitText != nil && r.FitText.Val != "" {
		if x, err := strconv.Atoi(r.FitText.Val); err == nil {
			p.FitTextWidthPt = float64(x) / 20.0
		}
		if r.FitText.ID != "" {
			if x, err := strconv.Atoi(r.FitText.ID); err == nil {
				p.FitTextID = x
			}
		}
	}
	if r.Bdr != nil {
		edge := BorderEdge{Style: r.Bdr.Val, Color: r.Bdr.Color}
		if r.Bdr.Sz != "" {
			if x, err := strconv.Atoi(r.Bdr.Sz); err == nil {
				edge.Sz = float64(x) / 8.0
			}
		}
		if edge.Style != "" && edge.Style != "none" {
			p.TextBorder = edge
		}
	}
	if r.Lang != nil {
		p.Lang = RunLang{Latin: r.Lang.Val, EastAsia: r.Lang.EastAsia, Bidi: r.Lang.Bidi}
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
	if r.EALayout != nil {
		ea := &EALayoutInfo{}
		if r.EALayout.Combine != "" && r.EALayout.Combine != "0" && r.EALayout.Combine != "none" {
			ea.Combine = true
		}
		ea.CombineBrackets = r.EALayout.Brackets
		if r.EALayout.Vert == "1" || r.EALayout.Vert == "true" {
			ea.Vert = true
		}
		if r.EALayout.VertC == "1" || r.EALayout.VertC == "true" {
			ea.VertCompress = true
		}
		p.EALayout = ea
	}
	if r.W14Ligatures != nil && r.W14Ligatures.Val != "" {
		p.W14Ligatures = r.W14Ligatures.Val
	}
	if r.W14NumForm != nil && r.W14NumForm.Val != "" {
		p.W14NumForm = r.W14NumForm.Val
	}
	if r.W14NumSpacing != nil && r.W14NumSpacing.Val != "" {
		p.W14NumSpacing = r.W14NumSpacing.Val
	}
	if r.W14CntxtAlts != nil {
		v := r.W14CntxtAlts.Val
		if v == "" {
			v = "true"
		}
		p.W14CntxtAlts = v
	}
	if r.W14Shadow != nil {
		if c := r.W14Shadow.color(); c != "" {
			p.W14ShadowColor = c
		} else {
			p.W14ShadowColor = "808080"
		}
	}
	if r.W14TextOutline != nil {
		if c := r.W14TextOutline.color(); c != "" {
			p.W14OutlineColor = c
		} else {
			p.W14OutlineColor = "000000"
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
			// the visible text and emit it as a styled paragraph. The
			// m:oMathParaPr/m:jc alignment hint is honored when present,
			// defaulting to centered (Word's display-math default).
			tree, txt, err := ExtractMathTree(dec, se)
			if err != nil {
				return err
			}
			if txt != "" || tree != nil {
				run := mathRun(txt, doc.Defaults)
				run.Math = tree
				align := AlignCenter
				if tree != nil {
					switch tree.Align {
					case "left":
						align = AlignLeft
					case "right":
						align = AlignRight
					case "centerGroup", "center":
						align = AlignCenter
					}
				}
				p := Paragraph{
					Alignment: align,
					Runs:      []Run{run},
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
		case "altChunk":
			// w:altChunk inserts an alternative-format part (HTML / RTF
			// / text). The text was already extracted to doc.AltChunks
			// by loadAltChunks — splice those blocks into the body now.
			rid := attr(se, "id")
			if rid != "" {
				if alts, ok := doc.AltChunks[rid]; ok {
					for _, b := range alts {
						pctx.curSection.Blocks = append(pctx.curSection.Blocks, b)
						doc.Body = append(doc.Body, b)
					}
				}
			}
			_ = dec.Skip()
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
			case "ins", "moveTo", "del", "moveFrom":
				// Tracked-change wrapper. Preserve the runs and tag them
				// with the revision type — the renderer decides between
				// accept mode (drop del/moveFrom) and show-revisions mode
				// (strikethrough del, underline ins, etc.).
				author := attr(t, "author")
				if err := decodeWrapperWithRev(dec, t, &p, paraRPr, pctx, false, t.Name.Local, author); err != nil {
					return p, err
				}
			case "customXmlInsRangeStart", "customXmlInsRangeEnd",
				"customXmlDelRangeStart", "customXmlDelRangeEnd",
				"customXmlMoveFromRangeStart", "customXmlMoveFromRangeEnd",
				"customXmlMoveToRangeStart", "customXmlMoveToRangeEnd":
				// Range markers track edits to custom XML wrappers. They
				// have no visible content; preserving the markup is only
				// useful for round-tripping. We skip them — the surrounded
				// inner runs already carry their own RevisionType.
				_ = dec.Skip()
			case "moveFromRangeStart", "moveFromRangeEnd",
				"moveToRangeStart", "moveToRangeEnd":
				// Word's move-range markers (companion to w:moveFrom /
				// w:moveTo). The inner runs already carry revision type,
				// so the empty range markers are skipped without losing
				// information.
				_ = dec.Skip()
			case "smartTag", "customXml":
				// Auto-recognized text / structured-doc wrapper. The wrapper
				// itself has no rendering effect — recurse into its child
				// runs so the contained text isn't lost.
				if err := decodeWrapper(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "dir", "bdo":
				// Bidi direction override (UAX#9 LRO/RLO/PDF). The w:val
				// attribute carries "ltr" or "rtl"; we stamp every inner
				// run so the renderer's reorderBidi pass treats them as
				// if they live inside a hard directional override.
				val := strings.ToLower(attr(t, "val"))
				before := len(p.Runs)
				if err := decodeWrapper(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
				if val == "ltr" || val == "rtl" {
					for i := before; i < len(p.Runs); i++ {
						if p.Runs[i].DirOverride == "" {
							p.Runs[i].DirOverride = val
						}
					}
				}
			case "subDoc":
				// Master-document sub-document reference. We can't pull
				// the external file at parse time, but surfacing a
				// labeled placeholder beats silently dropping the
				// pointer (Word/docx4j both round-trip these).
				rid := ""
				for _, a := range t.Attr {
					if a.Name.Local == "id" && (a.Name.Space == "" || strings.Contains(a.Name.Space, "relationships")) {
						rid = a.Value
					}
				}
				label := "[Sub-document]"
				if rid != "" {
					if target := pctx.doc.RelTargets[rid]; target != "" {
						label = "[Sub-document: " + target + "]"
					}
				}
				p.Runs = append(p.Runs, Run{Text: label, Props: paraRPr})
				_ = dec.Skip()
			case "sdt":
				// Inline content control: the actual runs live one level
				// deeper, inside <w:sdtContent>. decodeInlineSdt unwraps
				// that and hands the children to decodeWrapper.
				if err := decodeInlineSdt(dec, t, &p, paraRPr, pctx, false); err != nil {
					return p, err
				}
			case "oMath":
				// Inline math equation. Pull the structural tree AND a
				// textual approximation: the renderer paints the tree as
				// 2D when possible and falls back to the string for
				// search / accessibility.
				tree, txt, err := ExtractMathTree(dec, t)
				if err != nil {
					return p, err
				}
				if txt != "" || tree != nil {
					run := mathRun(txt, paraRPr)
					run.Math = tree
					p.Runs = append(p.Runs, run)
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
			case "proofErr":
				// Proofing-error span markers (Word's spelling/grammar
				// underlines). Strictly authoring metadata; drop.
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
// the parent paragraph, tagging each run with its tracked-change revision
// type so the renderer can choose between accept-mode (drop dels) and
// show-revisions mode (strikethrough dels, underline ins). The legacy
// `drop` parameter remains as a hard parser-side suppression hook for
// pathways that truly want the text gone (legacy callers); new code
// should pass false and let the renderer decide.
//
// Bookmark text capture continues to work because pctx.bmActive is checked
// for every emitted run regardless of nesting depth.
func decodeWrapper(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool) error {
	return decodeWrapperWithRev(dec, start, p, paraRPr, pctx, drop, "", "")
}

// decodeWrapperWithRev is decodeWrapper plus a revision tag that gets
// stamped onto every emitted run.
func decodeWrapperWithRev(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool, revType, revAuthor string) error {
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
				if revType != "" {
					for i := range runs {
						if runs[i].RevisionType == "" {
							runs[i].RevisionType = revType
							runs[i].RevisionAuthor = revAuthor
						}
					}
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
				// Nested tracked-change wrappers: the innermost wrapper's
				// type wins (so a `del` inside an `ins` reads as a del run,
				// matching Word's "show revisions" intent). smartTag and
				// customXml don't carry revision semantics.
				childRev := revType
				childAuthor := revAuthor
				switch t.Name.Local {
				case "ins", "moveTo", "del", "moveFrom":
					childRev = t.Name.Local
					childAuthor = attr(t, "author")
				}
				if err := decodeWrapperWithRev(dec, t, p, paraRPr, pctx, drop, childRev, childAuthor); err != nil {
					return err
				}
			case "sdt":
				// Inline SDT nested inside another wrapper (e.g. a tracked
				// insertion of a content control). Same transparency rule.
				if err := decodeInlineSdt(dec, t, p, paraRPr, pctx, drop); err != nil {
					return err
				}
			case "oMath":
				tree, txt, err := ExtractMathTree(dec, t)
				if err != nil {
					return err
				}
				if drop || (txt == "" && tree == nil) {
					continue
				}
				run := mathRun(txt, paraRPr)
				run.Math = tree
				p.Runs = append(p.Runs, run)
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
	// Word also carries w:tooltip and w:tgtFrame attributes; we capture
	// w:tooltip on each run for callers that introspect the AST. PDF
	// has no native equivalent so we don't render a tooltip surface,
	// but recording it round-trips the metadata.
	rid, anchor, tooltip := "", "", ""
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "id":
			rid = a.Value
		case "anchor":
			anchor = a.Value
		case "tooltip":
			tooltip = a.Value
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
					if tooltip != "" {
						runs[i].LinkTooltip = tooltip
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
						// Linked character style: a paragraph style with
						// w:link should also apply its linked rPr so
						// "Heading 1 Char" defaults flow through when
						// the doc applies Heading 1 to a paragraph.
						if st.LinkedCharStyle != "" {
							if cs, ok := doc.CharStyles[st.LinkedCharStyle]; ok {
								*paraRPr = MergeRunProps(*paraRPr, cs)
							}
						}
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
						// pStyle→numPr inheritance: a style with a
						// numPr makes paragraphs of that style auto-list.
						// Paragraph-level numPr (decoded later) wins.
						if st.NumPr.NumID > 0 && p.List == nil {
							li := st.NumPr
							p.List = &li
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
			case "mirrorIndents":
				p.MirrorIndents = onOff(t)
				_ = dec.Skip()
			case "adjustRightInd":
				p.AdjustRightInd = onOff(t)
				_ = dec.Skip()
			case "snapToGrid":
				v := onOff(t)
				p.SnapToGrid = &v
				_ = dec.Skip()
			case "widowControl":
				v := onOff(t)
				p.WidowControl = &v
				_ = dec.Skip()
			case "outlineLvl":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						// Word stores 0..8 here (Heading1..Heading9); 9 means
						// "body text". Use +1 internally to distinguish 0 from
						// "unset" (the zero value of int).
						p.OutlineLvl = x + 1
					}
				}
				_ = dec.Skip()
			case "textDirection":
				p.TextDirection = attr(t, "val")
				_ = dec.Skip()
			case "textAlignment":
				p.TextAlignment = attr(t, "val")
				_ = dec.Skip()
			case "kinsoku":
				v := onOff(t)
				p.Kinsoku = &v
				_ = dec.Skip()
			case "wordWrap":
				v := onOff(t)
				p.WordWrap = &v
				_ = dec.Skip()
			case "overflowPunct":
				v := onOff(t)
				p.OverflowPunct = &v
				_ = dec.Skip()
			case "topLinePunct":
				v := onOff(t)
				p.TopLinePunct = &v
				_ = dec.Skip()
			case "autoSpaceDE":
				v := onOff(t)
				p.AutoSpaceDE = &v
				_ = dec.Skip()
			case "autoSpaceDN":
				v := onOff(t)
				p.AutoSpaceDN = &v
				_ = dec.Skip()
			case "suppressLineNumbers":
				p.SuppressLineNumbers = onOff(t)
				_ = dec.Skip()
			case "suppressAutoHyphens":
				p.SuppressAutoHyphens = onOff(t)
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
				// negative = hanging indent). The *Chars variants
				// (firstLineChars / leftChars / hangingChars / startChars /
				// endChars / rightChars) are in 1/100 of a character: with
				// the doc-default font size S (points), 100 char-units = S
				// points of width (CJK characters being roughly square).
				defFontPt := 10.5
				if doc != nil && doc.Defaults.FontSize > 0 {
					defFontPt = doc.Defaults.FontSize
				}
				charsToPt := func(v string) (float64, bool) {
					x, err := strconv.Atoi(v)
					if err != nil {
						return 0, false
					}
					return float64(x) * defFontPt / 100.0, true
				}
				if v := attr(t, "left"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentLeftPt = float64(x) / 20.0
					}
				} else if v := attr(t, "start"); v != "" {
					// w:start is the bidi-neutral alias for w:left.
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentLeftPt = float64(x) / 20.0
					}
				}
				if v := attr(t, "leftChars"); v != "" {
					if pt, ok := charsToPt(v); ok {
						p.IndentLeftPt = pt
					}
				} else if v := attr(t, "startChars"); v != "" {
					if pt, ok := charsToPt(v); ok {
						p.IndentLeftPt = pt
					}
				}
				if v := attr(t, "firstLine"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentFirstLinePt = float64(x) / 20.0
					}
				}
				if v := attr(t, "firstLineChars"); v != "" {
					if pt, ok := charsToPt(v); ok {
						p.IndentFirstLinePt = pt
					}
				}
				if v := attr(t, "hanging"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						p.IndentFirstLinePt = -float64(x) / 20.0
					}
				}
				if v := attr(t, "hangingChars"); v != "" {
					if pt, ok := charsToPt(v); ok {
						p.IndentFirstLinePt = -pt
					}
				}
				// w:right / w:rightChars / w:end / w:endChars are parsed
				// only to be silently consumed — the layout currently uses
				// IndentLeftPt + paragraph width without a right indent.
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
			case "pPrChange":
				// Tracked property change: holds the OLD pPr before the
				// user changed it. Accept-changes semantics = ignore the
				// inner pPr (it's history-only) — but expose the change
				// marker so the renderer can paint a margin change bar
				// when ShowRevisions is on.
				if p != nil {
					p.PrChange = readPrChangeAttrs(t, "pPr")
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

// decodeRun returns one or more Run atoms because a single w:r may carry a
// text node, a w:br, and a w:drawing in any order.
func decodeRun(dec *xml.Decoder, start xml.StartElement, paraRPr RunProps, doc *Document) ([]Run, error) {
	rp := paraRPr
	var atoms []Run
	var rPrChange *PrChange

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
				if r.RPrChange != nil {
					rPrChange = r.RPrChange.toPrChange("rPr")
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
			case "ptab":
				// Positional tab — stop is computed at layout time from
				// alignment + relativeTo. Encode as a regular "\t" so the
				// renderer's tab path picks it up; the PtabInfo overrides
				// the stop lookup.
				pt := &PtabInfo{
					Alignment:  attr(t, "alignment"),
					RelativeTo: attr(t, "relativeTo"),
					Leader:     attr(t, "leader"),
				}
				atoms = append(atoms, Run{Text: "\t", Props: rp, Ptab: pt})
				_ = dec.Skip()
			case "pgNum", "dayLong", "dayShort", "monthLong", "monthShort", "yearLong", "yearShort":
				// Run-internal date / page placeholders. Word emits these
				// as standalone child elements (separate from PAGE / DATE
				// field markers). We rewrite them as a synthetic simple-
				// field marker stream so flattenFields handles them with
				// the same code path that resolves PAGE and DATE.
				instr := ""
				switch t.Name.Local {
				case "pgNum":
					instr = "PAGE"
				case "yearLong":
					instr = `DATE \@ "yyyy"`
				case "yearShort":
					instr = `DATE \@ "yy"`
				case "monthLong":
					instr = `DATE \@ "MMMM"`
				case "monthShort":
					instr = `DATE \@ "MMM"`
				case "dayLong":
					instr = `DATE \@ "dddd"`
				case "dayShort":
					instr = `DATE \@ "ddd"`
				}
				atoms = append(atoms,
					Run{FieldBegin: true, Props: rp, InlineDateKind: t.Name.Local},
					Run{InstrText: instr, Props: rp},
					Run{FieldSep: true, Props: rp},
					Run{FieldEnd: true, Props: rp},
				)
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
					customMark := ""
					if v := attr(t, "customMarkFollows"); v == "1" || v == "true" {
						// Word's convention: the next w:t in the
						// same run carries the literal mark glyph.
						// We mark the reference so the renderer can
						// pick it up when laying out the marker.
						customMark = "custom"
					}
					atoms = append(atoms, Run{
						Text:          "[" + id + "]",
						Props:         srp,
						FootnoteID:    id,
						IsEndnote:     t.Name.Local == "endnoteReference",
						CustomRefMark: customMark,
					})
				}
				_ = dec.Skip()
			case "footnoteRef", "endnoteRef":
				// Auto-number marker inside a footnote/endnote body that
				// represents "insert the current note's id here". The
				// runtime ID isn't known at parse time; mark with a
				// placeholder run that decodeNoteBody can substitute, or
				// fall back to rendering with the note id from rels
				// context. We emit a sentinel that the renderer treats as
				// the note's own id when it later wraps the body.
				srp := rp
				srp.VertAlign = "superscript"
				atoms = append(atoms, Run{Text: "​", Props: srp, FootnoteID: ""})
				_ = dec.Skip()
			case "annotationRef":
				// Reviewer-comment auto-number marker (similar to
				// footnoteRef but for comments). Surface a thin space so
				// the marker doesn't widen the line — the comments
				// section already includes its own id label.
				atoms = append(atoms, Run{Text: "​", Props: rp})
				_ = dec.Skip()
			case "separator":
				// Footnote separator marker — Word renders a horizontal
				// rule. We don't know we're inside a footnote body here,
				// but emit a U+23AF (HORIZONTAL LINE EXTENSION) glyph as a
				// best-effort visual placeholder.
				atoms = append(atoms, Run{Text: "⎯", Props: rp})
				_ = dec.Skip()
			case "continuationSeparator":
				atoms = append(atoms, Run{Text: "⎯⎯", Props: rp})
				_ = dec.Skip()
			case "sym":
				// <w:sym w:font="Wingdings" w:char="F0E0"/> — code point in the
				// referenced font. Best-effort: convert the hex code to a rune
				// and translate from the legacy private-use range (F020-F0FF)
				// to a canonical Unicode equivalent when w:font names Symbol
				// or Wingdings (the docs we ship can't embed the actual font
				// metric data, so PUA glyphs would render as tofu otherwise).
				if v := attr(t, "char"); v != "" {
					if cp, err := strconv.ParseUint(v, 16, 32); err == nil {
						mapped := mapSymbolGlyph(attr(t, "font"), rune(cp))
						atoms = append(atoms, Run{Text: string(mapped), Props: rp})
					}
				}
				_ = dec.Skip()
			case "fldChar":
				fldType := attr(t, "fldCharType")
				// A "begin" fldChar may carry a nested w:ffData (legacy
				// form fields: FORMTEXT / FORMCHECKBOX / FORMDROPDOWN).
				// Capture that state alongside the marker run so the
				// renderer can synthesize visible glyphs when Word didn't
				// cache a result.
				var ff *FormFieldInfo
				if fldType == "begin" {
					ff = scanFFData(dec, t)
				} else {
					_ = dec.Skip()
				}
				switch fldType {
				case "begin":
					r := Run{FieldBegin: true, Props: rp}
					if ff != nil {
						r.FormField = ff
					}
					atoms = append(atoms, r)
				case "separate":
					atoms = append(atoms, Run{FieldSep: true, Props: rp})
				case "end":
					atoms = append(atoms, Run{FieldEnd: true, Props: rp})
				}
			case "instrText":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				atoms = append(atoms, Run{InstrText: s, Props: rp})
			case "drawing":
				di, err := findDrawingInfo(dec, t, doc)
				if err != nil {
					return nil, err
				}
				if di.RID != "" {
					// wp:wrapTopAndBottom forces line breaks above and
					// below the image so surrounding text doesn't sit
					// next to it. Other wrap modes (square / tight /
					// through / none) currently render as inline; the
					// flag is preserved on Run.WrapMode for future
					// work but isn't honored by the line breaker yet.
					if di.WrapType == "topAndBottom" {
						atoms = append(atoms, Run{IsBreak: true, Props: rp})
					}
					atoms = append(atoms, Run{
						ImageID:             di.RID,
						ImageWidthPt:        di.WPt,
						ImageHeightPt:       di.HPt,
						ImageRotationDeg:    di.RotationDeg,
						ImageFlipH:          di.FlipH,
						ImageFlipV:          di.FlipV,
						CropTopPct:          di.CropT,
						CropBottomPct:       di.CropB,
						CropLeftPct:         di.CropL,
						CropRightPct:        di.CropR,
						ImageEffects:        di.ImageEffects,
						ImageAnchored:       di.IsAnchor,
						AnchorAlignH:        di.PosH.Align,
						AnchorAlignV:        di.PosV.Align,
						AnchorOffsetXPt:     di.PosH.OffsetPt(),
						AnchorOffsetYPt:     di.PosV.OffsetPt(),
						AnchorWrap:          di.WrapType,
						AnchorWrapPolygon:   di.WrapPolygon,
						AnchorSimplePosUsed: di.SimplePosUsed,
						AnchorSimplePosXPt:  di.SimplePosXPt,
						AnchorSimplePosYPt:  di.SimplePosYPt,
						AltText:             di.AltText,
						Props:               rp,
					})
				} else if di.IsGroup && di.GroupChildShapeCount > 1 && di.ShapePrst == "" && di.CustPath == "" {
					// Group drawing whose children were heterogeneous enough
					// that findDrawingInfo couldn't collapse them into a
					// single shape. Surface a labeled placeholder so the
					// reader sees something at the source's footprint.
					sh := &VMLShape{
						Kind:           "rect",
						WidthPt:        di.WPt,
						HeightPt:       di.HPt,
						StrokeColor:    "808080",
						StrokeWeightPt: 0.5,
						TextBox:        fmt.Sprintf("[Group: %d shapes]", di.GroupChildShapeCount),
					}
					if sh.WidthPt <= 0 {
						sh.WidthPt = 192
					}
					if sh.HeightPt <= 0 {
						sh.HeightPt = 96
					}
					atoms = append(atoms, Run{
						VMLShape:        sh,
						AltText:         di.AltText,
						ImageAnchored:   di.IsAnchor,
						AnchorAlignH:    di.PosH.Align,
						AnchorAlignV:    di.PosV.Align,
						AnchorOffsetXPt: di.PosH.OffsetPt(),
						AnchorOffsetYPt: di.PosV.OffsetPt(),
						AnchorWrap:      di.WrapType,
						Props:           rp,
					})
					di.TxbxText = ""
				} else if di.ShapePrst != "" || di.CustPath != "" {
					// DrawingML vector shape (no image). Synthesize a
					// VMLShape so the renderer's shape path draws it.
					sh := &VMLShape{
						Kind:              shapeKindForPrst(di.ShapePrst),
						WidthPt:           di.WPt,
						HeightPt:          di.HPt,
						FillColor:         di.ShapeFill,
						StrokeColor:       di.ShapeStroke,
						StrokeWeightPt:    di.ShapeStrokeWeightPt,
						CustomPath:        di.CustPath,
						GradientKind:      di.ShapeGradientKind,
						GradientAngle:     di.ShapeGradientAngle,
						GradientStops:     di.ShapeGradientStops,
						Shadow:            di.ShapeShadow,
						Pattern:           di.ShapePattern,
						TextBox:           di.TxbxText,
						TextBoxBlocks:     di.TxbxBlocks,
						HeadEnd:           di.ShapeHeadEnd,
						TailEnd:           di.ShapeTailEnd,
						TextAnchor:        di.TextAnchor,
						TextLeftInsetPt:   di.TextLeftInsetPt,
						TextTopInsetPt:    di.TextTopInsetPt,
						TextRightInsetPt:  di.TextRightInsetPt,
						TextBottomInsetPt: di.TextBottomInsetPt,
						TextVertical:      di.TextVertical,
						TextAutoFit:       di.TextAutoFit,
					}
					if sh.WidthPt <= 0 {
						sh.WidthPt = 96
					}
					if sh.HeightPt <= 0 {
						sh.HeightPt = 48
					}
					atoms = append(atoms, Run{
						VMLShape:            sh,
						AltText:             di.AltText,
						ImageAnchored:       di.IsAnchor,
						AnchorAlignH:        di.PosH.Align,
						AnchorAlignV:        di.PosV.Align,
						AnchorOffsetXPt:     di.PosH.OffsetPt(),
						AnchorOffsetYPt:     di.PosV.OffsetPt(),
						AnchorWrap:          di.WrapType,
						AnchorWrapPolygon:   di.WrapPolygon,
						AnchorSimplePosUsed: di.SimplePosUsed,
						AnchorSimplePosXPt:  di.SimplePosXPt,
						AnchorSimplePosYPt:  di.SimplePosYPt,
						Props:               rp,
					})
					// Done — text-box content is carried by the shape
					// itself, no separate inline italic dump needed.
					di.TxbxText = ""
				}
				// Text-box body (wps:txbx) on a drawing that's NOT a
				// vector shape (image-only or empty drawing). Inline-
				// emit as italic so the reader can distinguish box
				// content from surrounding flow text.
				if di.TxbxText != "" {
					trp := rp
					trp.Italic = true
					atoms = append(atoms, Run{Text: di.TxbxText, Props: trp})
				}
				// Chart reference: paint a rectangular placeholder
				// where the chart would sit, then surface the
				// pre-extracted labels next to it. Pixel-accurate
				// chart drawing (bars, slices, axes) would require
				// parsing the c:ser/c:val data — best-effort here is
				// the labeled rect so the reader sees the chart's
				// dimensions plus its title/data prose.
				if di.ChartRID != "" {
					txt := doc.Charts[di.ChartRID]
					sh := &VMLShape{
						Kind:           "rect",
						WidthPt:        di.WPt,
						HeightPt:       di.HPt,
						StrokeColor:    "808080",
						StrokeWeightPt: 0.5,
						TextBox:        "Chart",
					}
					if sh.WidthPt <= 0 {
						sh.WidthPt = 240
					}
					if sh.HeightPt <= 0 {
						sh.HeightPt = 160
					}
					// Prefer the structured chart data when available so
					// the renderer can paint real bars/pies. The text
					// dump stays as the placeholder label.
					if cd, ok := doc.ChartsData[di.ChartRID]; ok {
						sh.Chart = &cd
						sh.TextBox = "" // chart renderer paints its own labels
					} else if txt != "" {
						sh.TextBox = "Chart: " + truncateText(txt, 80)
					}
					atoms = append(atoms, Run{VMLShape: sh, Props: rp})
					if sh.Chart == nil && txt != "" {
						// Fallback: dump the chart's text content inline
						// so prose around the chart still makes sense.
						trp := rp
						trp.Italic = true
						atoms = append(atoms, Run{Text: "[Chart: " + txt + "]", Props: trp})
					}
				}
				// SmartArt diagram reference: paint a labeled box for
				// the visual footprint and surface the node-text graph
				// as italic prose so the conceptual content survives.
				// Full diagram layout requires running the layout/colors/
				// quickStyle algorithm — out of scope.
				if di.DiagramRID != "" {
					txt := doc.Diagrams[di.DiagramRID]
					// When the SmartArt drawing part was pre-rendered by
					// Word, use that whole group as the shape — every
					// node renders as a real geometric box with its
					// caption. Otherwise synthesize a minimal process
					// layout from the node-text list so a diagram still
					// shows up as boxes-with-arrows rather than a single
					// gray placeholder rect.
					var sh *VMLShape
					if drew := doc.DiagramShapes[di.DiagramRID]; drew != nil {
						scaled := *drew
						scaled.WidthPt = di.WPt
						scaled.HeightPt = di.HPt
						if scaled.WidthPt <= 0 {
							scaled.WidthPt = 320
						}
						if scaled.HeightPt <= 0 {
							scaled.HeightPt = 200
						}
						sh = &scaled
					} else if synth := synthesizeSmartArtLayoutKind(txt, doc.DiagramLayouts[di.DiagramRID], di.WPt, di.HPt); synth != nil {
						sh = synth
					} else {
						sh = &VMLShape{
							Kind:           "rect",
							WidthPt:        di.WPt,
							HeightPt:       di.HPt,
							StrokeColor:    "B0B0B0",
							StrokeWeightPt: 0.5,
							TextBox:        "SmartArt",
						}
						if sh.WidthPt <= 0 {
							sh.WidthPt = 240
						}
						if sh.HeightPt <= 0 {
							sh.HeightPt = 160
						}
					}
					atoms = append(atoms, Run{VMLShape: sh, Props: rp})
					if doc.DiagramShapes[di.DiagramRID] == nil && sh.Kind != "group" && txt != "" {
						trp := rp
						trp.Italic = true
						atoms = append(atoms, Run{Text: "[Diagram: " + txt + "]", Props: trp})
					}
				}
			case "pict":
				// Legacy VML images: <w:pict><v:shape style="..."><v:imagedata r:id="..."/>
				// </v:shape></w:pict>. Older Word docs, Excel/Outlook pastes, and
				// some converters still emit these instead of w:drawing.
				vi, err := findPictInfo(dec, t, doc)
				if err != nil {
					return nil, err
				}
				if vi.IsHR {
					atoms = append(atoms, Run{HorizontalRule: true, Props: rp})
				} else if vi.RID != "" {
					atoms = append(atoms, Run{
						ImageID:       vi.RID,
						ImageWidthPt:  vi.WPt,
						ImageHeightPt: vi.HPt,
						Props:         rp,
					})
				} else if vi.Shape != nil {
					atoms = append(atoms, Run{VMLShape: vi.Shape, Props: rp})
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
				// chart, equation, ...). Word usually pairs the OLE blob
				// with a v:imagedata preview. Order of preference for the
				// output:
				//   1. Decoded Excel grid (rendered as a small table).
				//   2. The preview image / VML shape.
				//   3. "[Embedded object]" textual placeholder.
				vi, err := findPictInfo(dec, t, doc)
				if err != nil {
					return nil, err
				}
				if vi.OLEObjectRID != "" && doc != nil {
					if xl, ok := doc.OLEEmbeds[vi.OLEObjectRID]; ok && len(xl.Cells) > 0 {
						// Emit the grid as a text run so it lives inside
						// the paragraph; a real Table block would require
						// closing the current paragraph, which we cannot
						// safely do from here.
						txt := flattenExcelGrid(xl)
						atoms = append(atoms, Run{Text: txt, Props: rp})
						continue
					}
				}
				// OLE-type-specific fallback labels: when no preview image
				// or shape is available, pick a label that reflects the
				// embed kind so the result isn't a generic "[Embedded object]"
				// (which is uninformative when, say, an old MathType
				// equation surfaces as a blank in the PDF).
				oleLabel := func(progID string) string {
					switch {
					case strings.HasPrefix(progID, "Equation.DSMT"):
						return "[MathType equation — original markup not preserved]"
					case progID == "Equation.3":
						return "[Legacy equation — original markup not preserved]"
					case strings.HasPrefix(progID, "Excel."):
						return "[Embedded spreadsheet]"
					case strings.HasPrefix(progID, "PowerPoint."):
						return "[Embedded presentation]"
					case strings.HasPrefix(progID, "Visio."):
						return "[Embedded Visio diagram]"
					case strings.HasPrefix(progID, "Word."):
						return "[Embedded Word document]"
					case strings.HasPrefix(progID, "Package"):
						return "[Embedded file]"
					case progID == "":
						return "[Embedded object]"
					default:
						return "[Embedded " + progID + " object]"
					}
				}
				switch {
				case vi.RID != "":
					atoms = append(atoms, Run{
						ImageID:       vi.RID,
						ImageWidthPt:  vi.WPt,
						ImageHeightPt: vi.HPt,
						Props:         rp,
					})
				case vi.Shape != nil:
					atoms = append(atoms, Run{VMLShape: vi.Shape, Props: rp})
				default:
					atoms = append(atoms, Run{Text: oleLabel(vi.OLEProgID), Props: rp})
				}
			case "ruby":
				ruby, base, err := decodeRuby(dec, t)
				if err != nil {
					return nil, err
				}
				if base != "" {
					atoms = append(atoms, Run{Text: base, Ruby: &ruby, Props: rp})
				}
			case "contentPart":
				// w14:ink — we can't rasterize the stroke data, emit a
				// placeholder so the marker isn't lost.
				atoms = append(atoms, Run{Text: "[Ink]", InkPlaceholder: true, Props: rp})
				_ = dec.Skip()
			case "lastRenderedPageBreak":
				// Word's cached pagination hint — purely informational
				// (the renderer paginates itself). Drop silently so the
				// element doesn't fall through default-skip with noise.
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if rPrChange != nil {
					for i := range atoms {
						if atoms[i].PrChange == nil {
							atoms[i].PrChange = rPrChange
						}
					}
				}
				return atoms, nil
			}
		}
	}
}

// decodeRuby parses a <w:ruby> element. Returns the ruby annotation
// info and the base text it decorates.
func decodeRuby(dec *xml.Decoder, start xml.StartElement) (RubyInfo, string, error) {
	var ri RubyInfo
	var base, rt string
	for {
		tok, err := dec.Token()
		if err != nil {
			return ri, "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "rubyPr":
				if err := decodeRubyPr(dec, t, &ri); err != nil {
					return ri, "", err
				}
			case "rt":
				rt = drainRunsForText(dec, t)
			case "rubyBase":
				base = drainRunsForText(dec, t)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				ri.Text = rt
				return ri, base, nil
			}
		}
	}
}

func decodeRubyPr(dec *xml.Decoder, start xml.StartElement, ri *RubyInfo) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "rubyAlign":
				ri.Align = attr(t, "val")
			case "hps":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						ri.Hps = x
					}
				}
			case "hpsRaise":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						ri.HpsRaise = x
					}
				}
			case "lid":
				ri.LangAttr = attr(t, "val")
			}
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

func drainRunsForText(dec *xml.Decoder, start xml.StartElement) string {
	var b strings.Builder
	depth := 1
	inT := false
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return b.String()
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
			depth--
		case xml.CharData:
			if inT {
				b.Write(t)
			}
		}
	}
	return b.String()
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
	// TxbxBlocks is the structured paragraph/table tree inside the same
	// w:txbxContent. Populated by extractTxbxRich so the renderer can
	// draw rich content (lists, bold runs, nested tables) instead of a
	// single flat string.
	TxbxBlocks []Block
	// ChartRID is set when the drawing references a chart part via
	// <c:chart r:id="…">. The renderer looks up Document.Charts[ChartRID]
	// to surface chart labels as plain text. Empty for non-chart drawings.
	ChartRID string
	// DiagramRID is set when the drawing references a SmartArt diagram
	// via <dgm:relIds r:dm="…">. The renderer looks up
	// Document.Diagrams[DiagramRID] for the flattened node text.
	DiagramRID string
	// IsAnchor is true when the drawing comes from a wp:anchor (floating)
	// rather than wp:inline. We don't currently implement text wrap so
	// the run still renders inline, but the flag is preserved so the
	// renderer can apply best-effort offset / alignment from PosH/PosV.
	IsAnchor bool
	// PosH / PosV mirror wp:positionH / wp:positionV.
	PosH, PosV DrawingPos
	// WrapType is "" (no wrap = the wp:anchor used wrapNone or no wrap
	// element), "square", "tight", "through", "topAndBottom", "behind",
	// "inFront".
	WrapType string
	// AltText comes from wp:docPr's w:descr / w:title attributes —
	// accessibility metadata for tagged PDFs.
	AltText string
	// ShapePrst is the DrawingML <a:prstGeom prst="…"> preset name when
	// the drawing is a vector shape rather than an image.
	ShapePrst string
	// ShapeFill / ShapeStroke / ShapeStrokeWeightPt are scraped from
	// <a:spPr><a:solidFill>/<a:ln>.
	ShapeFill           string
	ShapeStroke         string
	ShapeStrokeWeightPt float64
	// ShapeHeadEnd / ShapeTailEnd carry the <a:headEnd type="..."/> and
	// <a:tailEnd type="..."/> values when the line declares arrows.
	ShapeHeadEnd string
	ShapeTailEnd string
	// ShapeDashStyle / ShapeCapStyle / ShapeCompoundLn mirror the rest of
	// <a:ln>: dash preset, cap (flat/rnd/sq), and compound line (sng/dbl
	// /thickThin/thinThick/tri). When empty the renderer treats the line
	// as solid / flat / single.
	ShapeDashStyle  string
	ShapeCapStyle   string
	ShapeCompoundLn string
	// ShapeGradientKind / ShapeGradientAngle / ShapeGradientStops capture
	// <a:gradFill> when the shape uses a gradient instead of solid fill.
	ShapeGradientKind  string
	ShapeGradientAngle float64
	ShapeGradientStops []GradientStop
	// ShapeShadow captures <a:effectLst><a:outerShdw>.
	ShapeShadow *ShadowEffect
	// ShapeInnerShadow captures <a:effectLst><a:innerShdw>.
	ShapeInnerShadow *ShadowEffect
	// ShapeGlow captures <a:effectLst><a:glow> — halo around the edge.
	ShapeGlow *GlowEffect
	// ShapeReflection captures <a:effectLst><a:reflection> — mirror below.
	ShapeReflection *ReflectionEffect
	// ShapeSoftEdgePt captures <a:effectLst><a:softEdge rad="…"/> — gaussian
	// edge softening radius in points. The renderer approximates this by
	// drawing a thin lighter outline.
	ShapeSoftEdgePt float64
	// ShapePattern captures <a:pattFill prst="…"> when present. Renderer
	// can use the preset to tile a real pattern; falls back to ShapeFill
	// when nil.
	ShapePattern *PatternFill
	// CustPath is the flattened path list when the shape declares
	// <a:custGeom><a:pathLst>. Uses simple m/l/c/q/z + numeric tokens.
	CustPath string
	// OLEPreviewRID is the relationship id of the preview image inside a
	// w:object/o:OLEObject — extracted from the nested v:imagedata r:id
	// when w:object wraps embedded content.
	OLEPreviewRID string
	// ImageEffects lists per-pixel filters captured from <a:blip>'s effect
	// children (alphaModFix / lum / biLevel / duotone / grayscl / blur).
	ImageEffects []ImageEffect
	// RotationDeg / FlipH / FlipV mirror the a:xfrm attributes — degrees
	// clockwise + axis-flip flags applied to the drawing as a whole.
	RotationDeg float64
	FlipH       bool
	FlipV       bool
	// TextAnchor / TextInset* / TextVertical / TextAutoFit mirror
	// <a:bodyPr> attributes. Captured per-drawing so VMLShape carries
	// them onto the rendered shape.
	TextAnchor        string
	TextLeftInsetPt   float64
	TextTopInsetPt    float64
	TextRightInsetPt  float64
	TextBottomInsetPt float64
	TextVertical      string
	TextAutoFit       string
	// WrapPolygon carries the wp:wrapPolygon vertices when wrapTight /
	// wrapThrough specifies a custom contour. Empty when wrap is
	// rectangular.
	WrapPolygon []WrapPathPoint
	// IsGroup reports that the drawing's a:graphicData wrapped a
	// wpg:wgp / a:grpSp / wpc:wpc container — a grouped collection of
	// child shapes. The renderer treats group drawings as a single
	// labeled placeholder when no single child shape's geometry was
	// captured.
	IsGroup bool
	// GroupChildShapeCount is the number of immediate wsp/sp/pic/cxnSp
	// children discovered inside a wgp/grpSp container. The renderer
	// uses it to label the placeholder box ("[Group: N shapes]").
	GroupChildShapeCount int
	// SimplePosUsed reflects the wp:anchor@simplePos attribute. When true,
	// SimplePosXPt / SimplePosYPt are absolute page-relative coordinates
	// pulled from <wp:simplePos> instead of positionH/positionV.
	SimplePosUsed              bool
	SimplePosXPt, SimplePosYPt float64
	// AllowOverlap mirrors wp:anchor@allowOverlap. When false, the
	// drawing must not overlap other floating drawings on the same
	// page. (Renderer currently treats this as a layering hint only.)
	AllowOverlap bool
	// BehindDoc mirrors wp:anchor@behindDoc — when true the drawing is
	// painted *behind* the text layer. Watermarks set this.
	BehindDoc bool
	// LayoutInCell is wp:anchor@layoutInCell — when true a drawing
	// anchored inside a table cell respects the cell boundaries.
	LayoutInCell bool
	// LockedAnchor is wp:anchor@locked — anchor cannot be moved by the
	// user. Pure metadata for renderers.
	LockedAnchor bool
	// RelativeHeight is wp:anchor@relativeHeight — the Z-ordering key
	// among overlapping anchors; larger wins.
	RelativeHeight int
	// WrapDistT / B / L / R store the inter-text distance attributes
	// wp:anchor@distT/distB/distL/distR — extra space (in EMU, kept as
	// pt) text keeps around the drawing when wrap is square / tight.
	WrapDistTPt, WrapDistBPt, WrapDistLPt, WrapDistRPt float64
	// WrapSide stores wp:wrapSquare/@wrapText — "bothSides", "leftOnly",
	// "rightOnly", or "largest" — controlling which side(s) of the
	// drawing text flows around.
	WrapSide string
}

// DrawingPos mirrors wp:positionH / wp:positionV — either an absolute
// offset in EMU OR an alignment keyword, relative to one of several
// anchor frames.
type DrawingPos struct {
	RelativeFrom string // "page" / "margin" / "column" / "paragraph" / "character" / "line"
	Align        string // "left" / "center" / "right" / "inside" / "outside" / "top" / "bottom"
	OffsetEMU    int64  // when Align is empty, the absolute offset in EMU
}

// OffsetPt returns the offset in PostScript points (0 if Align is set).
func (d DrawingPos) OffsetPt() float64 {
	return float64(d.OffsetEMU) / emuPerPt
}

// emuPerPt is the OOXML "English Metric Unit" → PostScript point conversion.
// 1 inch = 914400 EMU, 1 pt = 1/72 inch, so 1 pt = 914400/72 = 12700 EMU.
// (The often-seen value 9525 is EMU-per-pixel-at-96dpi, not per-point.)
const emuPerPt = 12700.0

// parseWrapPolygon reads a <wp:wrapPolygon> subtree, collecting <wp:start>
// and <wp:lineTo> vertices into a polygon. Coordinates are kept as integers
// in the path's own coordinate space (per spec, 1/21600 of the shape's
// bounding box). Returns the closed polygon — duplicate trailing vertex
// matching the start is dropped so callers can wrap-iterate cleanly.
func parseWrapPolygon(dec *xml.Decoder, start xml.StartElement) ([]WrapPathPoint, error) {
	depth := 1
	var pts []WrapPathPoint
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return pts, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "start", "lineTo":
				var x, y int
				if v := attr(t, "x"); v != "" {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						x = n
					}
				}
				if v := attr(t, "y"); v != "" {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						y = n
					}
				}
				pts = append(pts, WrapPathPoint{X: x, Y: y})
			}
		case xml.EndElement:
			depth--
		}
	}
	if len(pts) >= 2 && pts[0] == pts[len(pts)-1] {
		pts = pts[:len(pts)-1]
	}
	return pts, nil
}

// findDrawingInfo walks a w:drawing subtree and pulls out the drawing info.
// srcRect: each side is in 1/1000ths of a percent (so 10000 = 10%).
//
// doc, when non-nil, is used to fully parse rich text inside w:txbxContent
// (paragraphs / tables / lists, including formatting). When nil the parser
// still records the flattened text into info.TxbxText.
func findDrawingInfo(dec *xml.Decoder, start xml.StartElement, doc *Document) (info drawingInfo, err error) {
	depth := 1
	// Position-decoding context: when we're inside a positionH / positionV
	// element we capture children into the appropriate DrawingPos.
	posTarget := "" // "" / "H" / "V"
	for depth > 0 {
		tok, e := dec.Token()
		if e != nil {
			return info, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "anchor":
				info.IsAnchor = true
				if v := attr(t, "simplePos"); v == "1" || v == "true" {
					info.SimplePosUsed = true
				}
				if v := attr(t, "allowOverlap"); v == "1" || v == "true" {
					info.AllowOverlap = true
				}
				if v := attr(t, "behindDoc"); v == "1" || v == "true" {
					info.BehindDoc = true
				}
				if v := attr(t, "layoutInCell"); v == "1" || v == "true" {
					info.LayoutInCell = true
				}
				if v := attr(t, "locked"); v == "1" || v == "true" {
					info.LockedAnchor = true
				}
				if v := attr(t, "relativeHeight"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						info.RelativeHeight = x
					}
				}
				toPt := func(name string) float64 {
					v := attr(t, name)
					if v == "" {
						return 0
					}
					x, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return 0
					}
					return float64(x) / emuPerPt
				}
				info.WrapDistTPt = toPt("distT")
				info.WrapDistBPt = toPt("distB")
				info.WrapDistLPt = toPt("distL")
				info.WrapDistRPt = toPt("distR")
			case "simplePos":
				// wp:simplePos (legacy Word 2003 absolute positioning).
				// Only honored when the enclosing wp:anchor declared
				// simplePos="1"; otherwise it's a hint Word ignores.
				if v := attr(t, "x"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						info.SimplePosXPt = float64(x) / emuPerPt
					}
				}
				if v := attr(t, "y"); v != "" {
					if y, err := strconv.ParseInt(v, 10, 64); err == nil {
						info.SimplePosYPt = float64(y) / emuPerPt
					}
				}
			case "wrapPolygon":
				poly, err := parseWrapPolygon(dec, t)
				if err != nil {
					return info, err
				}
				info.WrapPolygon = poly
				depth--
			case "wgp", "grpSp", "wpc":
				// wpg:wgp / a:grpSp / wpc:wpc — grouped shapes container.
				// We still walk into it so any single nested geometry can
				// surface, but flag the drawing so the renderer can label
				// the placeholder rather than silently rendering only the
				// last child whose prstGeom/blip we happened to capture.
				info.IsGroup = true
			case "wsp", "sp", "cxnSp", "pic":
				if info.IsGroup {
					info.GroupChildShapeCount++
				}
			case "positionH":
				posTarget = "H"
				info.PosH.RelativeFrom = attr(t, "relativeFrom")
			case "positionV":
				posTarget = "V"
				info.PosV.RelativeFrom = attr(t, "relativeFrom")
			case "align":
				val, _ := readElementText(dec, t)
				depth-- // readElementText consumed the EndElement
				switch posTarget {
				case "H":
					info.PosH.Align = val
				case "V":
					info.PosV.Align = val
				}
			case "posOffset":
				val, _ := readElementText(dec, t)
				depth--
				if x, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
					switch posTarget {
					case "H":
						info.PosH.OffsetEMU = x
					case "V":
						info.PosV.OffsetEMU = x
					}
				}
			case "wrapNone":
				info.WrapType = "none"
			case "wrapSquare":
				info.WrapType = "square"
				if v := attr(t, "wrapText"); v != "" {
					info.WrapSide = v
				}
			case "wrapTight":
				info.WrapType = "tight"
				if v := attr(t, "wrapText"); v != "" {
					info.WrapSide = v
				}
			case "wrapThrough":
				info.WrapType = "through"
				if v := attr(t, "wrapText"); v != "" {
					info.WrapSide = v
				}
			case "wrapTopAndBottom":
				info.WrapType = "topAndBottom"
			case "docPr":
				// wp:docPr w:descr / w:title — accessibility alt text.
				if v := attr(t, "descr"); v != "" {
					info.AltText = v
				} else if v := attr(t, "title"); v != "" {
					info.AltText = v
				}
			case "xfrm":
				// a:xfrm carries rotation + flip flags for the entire
				// drawing. rot is in 60000ths of a degree per DrawingML.
				if v := attr(t, "rot"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil && x != 0 {
						info.RotationDeg = float64(x) / 60000.0
					}
				}
				if v := attr(t, "flipH"); v == "1" || v == "true" {
					info.FlipH = true
				}
				if v := attr(t, "flipV"); v == "1" || v == "true" {
					info.FlipV = true
				}
			case "prstGeom":
				if v := attr(t, "prst"); v != "" {
					info.ShapePrst = v
				}
			case "custGeom":
				// Scoop the custGeom path tokens as a flat string the
				// renderer can parse. We don't try to honor avLst /
				// gdLst — those drive parametric overrides we don't model.
				p, err := flattenCustGeomPath(dec, t)
				if err != nil {
					return info, err
				}
				info.CustPath = p
				depth--
			case "solidFill":
				// Best-effort scrape: first color leaf wins. schemeClr is
				// resolved against doc.Theme; modifier chain (lumMod/tint/
				// shade/etc.) applied.
				if c := scanSolidFillColorTheme(dec, t, themeFor(doc)); c != "" {
					if info.ShapeFill == "" {
						info.ShapeFill = c
					}
				}
				depth--
			case "gradFill":
				stops, angle, kind, err := parseGradFillTheme(dec, t, themeFor(doc))
				if err != nil {
					return info, err
				}
				depth--
				if len(stops) > 0 && info.ShapeGradientKind == "" {
					info.ShapeGradientKind = kind
					info.ShapeGradientAngle = angle
					info.ShapeGradientStops = stops
				}
			case "pattFill":
				// Pattern fills — we now record the preset name so the
				// renderer can tile a real pattern. Fallback color is
				// recorded for renderers that don't know the preset.
				fillSpec, err := parsePattFillSpec(dec, t, themeFor(doc))
				if err != nil {
					return info, err
				}
				depth--
				if fillSpec.AvgColor != "" && info.ShapeFill == "" {
					info.ShapeFill = fillSpec.AvgColor
				}
				if info.ShapePattern == nil && fillSpec.Preset != "" {
					info.ShapePattern = &PatternFill{
						Preset: fillSpec.Preset,
						FgHex:  fillSpec.FgHex,
						BgHex:  fillSpec.BgHex,
					}
				}
			case "effectLst":
				effs, err := parseEffectListExt(dec, t, themeFor(doc))
				if err != nil {
					return info, err
				}
				depth--
				if effs.Shadow != nil && info.ShapeShadow == nil {
					info.ShapeShadow = effs.Shadow
				}
				if effs.Glow != nil && info.ShapeGlow == nil {
					info.ShapeGlow = effs.Glow
				}
				if effs.Reflection != nil && info.ShapeReflection == nil {
					info.ShapeReflection = effs.Reflection
				}
				if effs.SoftEdgePt > 0 && info.ShapeSoftEdgePt == 0 {
					info.ShapeSoftEdgePt = effs.SoftEdgePt
				}
				if effs.InnerShadow != nil && info.ShapeInnerShadow == nil {
					info.ShapeInnerShadow = effs.InnerShadow
				}
			case "fillRef":
				// DrawingML style-matrix reference. We don't resolve the
				// full a:fillStyleLst entry (which can be a gradient or
				// blip fill), but the embedded schemeClr/srgbClr child
				// gives us the placeholder color (Word's `phClr` slot)
				// that would have been substituted. Use it as the
				// shape's fill when nothing more specific was supplied.
				if c := scanSolidFillColorTheme(dec, t, themeFor(doc)); c != "" && info.ShapeFill == "" {
					info.ShapeFill = c
				}
				depth--
			case "lnRef":
				if c := scanSolidFillColorTheme(dec, t, themeFor(doc)); c != "" && info.ShapeStroke == "" {
					info.ShapeStroke = c
				}
				depth--
			case "effectRef":
				// Style-matrix effect reference — typically points to a
				// drop-shadow entry. We pull the embedded color so any
				// downstream effect can pick it up; without resolving the
				// effect list itself, we don't synthesize a shadow.
				_ = scanSolidFillColorTheme(dec, t, themeFor(doc))
				depth--
			case "ln":
				// DrawingML <a:ln w="…"> line weight (in EMU). Scrape
				// stroke color from nested solidFill (theme-aware), plus
				// headEnd / tailEnd arrow decorations.
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						info.ShapeStrokeWeightPt = float64(x) / emuPerPt
					}
				}
				props := parseLinePropsExt(dec, t, themeFor(doc))
				if props.Color != "" {
					info.ShapeStroke = props.Color
				}
				if props.HeadEnd != "" {
					info.ShapeHeadEnd = props.HeadEnd
				}
				if props.TailEnd != "" {
					info.ShapeTailEnd = props.TailEnd
				}
				if props.DashStyle != "" {
					info.ShapeDashStyle = props.DashStyle
				}
				if props.CapStyle != "" {
					info.ShapeCapStyle = props.CapStyle
				}
				if props.CompoundLn != "" {
					info.ShapeCompoundLn = props.CompoundLn
				}
				depth--
			case "blip":
				rasterRID := ""
				for _, a := range t.Attr {
					if a.Name.Local == "embed" {
						rasterRID = a.Value
					}
				}
				// Drain a:blip's effect children (alphaModFix / lum / biLevel /
				// duotone / clrChange / grayscl / blur) AND scan a:extLst for
				// the asvg:svgBlip extension. Word writes both a raster
				// preview rId and an SVG rId; we prefer the SVG when one is
				// present so vector graphics render sharply, falling back to
				// the raster rId if SVG parsing failed at load time.
				effs, svgRID := parseBlipEffects(dec, t, themeFor(doc))
				if len(effs) > 0 {
					info.ImageEffects = append(info.ImageEffects, effs...)
				}
				if svgRID != "" {
					if _, ok := doc.Images[svgRID]; ok {
						info.RID = svgRID
					} else {
						info.RID = rasterRID
					}
				} else {
					info.RID = rasterRID
				}
				depth--
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
			case "relIds":
				// <dgm:relIds r:dm="…" r:lo="…" r:qs="…" r:cs="…"/>
				// — SmartArt diagram reference. r:dm points to the
				// data part (node text); other rIds are layout / style
				// parts we don't consume.
				for _, a := range t.Attr {
					if a.Name.Local == "dm" {
						info.DiagramRID = a.Value
						break
					}
				}
			case "txbxContent":
				// Word text-box body. The rich extractor parses the
				// full paragraph/table tree (so bold runs, bullets, and
				// nested tables survive) while also producing a flat
				// fallback string for callers that only need plain text.
				txt, blocks, err := extractTxbxRich(dec, t, doc)
				if err != nil {
					return info, err
				}
				info.TxbxText = txt
				info.TxbxBlocks = blocks
				// extractTxbxRich consumed the matching EndElement, so
				// undo the +1 we did at the top of this StartElement
				// branch — otherwise depth never returns to 0.
				depth--
			case "bodyPr":
				// <a:bodyPr> — text-frame attributes for the shape's
				// txbxContent. Captures anchor + insets + vertical text +
				// autoFit hints; the renderer applies them at draw time
				// in vmlshape.go's textbox-content path.
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "anchor":
						info.TextAnchor = a.Value
					case "lIns":
						if x, e := strconv.ParseFloat(a.Value, 64); e == nil {
							info.TextLeftInsetPt = x / emuPerPt
						}
					case "tIns":
						if x, e := strconv.ParseFloat(a.Value, 64); e == nil {
							info.TextTopInsetPt = x / emuPerPt
						}
					case "rIns":
						if x, e := strconv.ParseFloat(a.Value, 64); e == nil {
							info.TextRightInsetPt = x / emuPerPt
						}
					case "bIns":
						if x, e := strconv.ParseFloat(a.Value, 64); e == nil {
							info.TextBottomInsetPt = x / emuPerPt
						}
					case "vert":
						info.TextVertical = a.Value
					}
				}
				// Walk children for normAutofit / spAutoFit / noAutofit —
				// they're empty elements but exist as children of bodyPr.
				bpDepth := 1
				for bpDepth > 0 {
					innerTok, err := dec.Token()
					if err != nil {
						break
					}
					switch it := innerTok.(type) {
					case xml.StartElement:
						bpDepth++
						switch it.Name.Local {
						case "normAutofit", "spAutoFit":
							info.TextAutoFit = it.Name.Local
						}
					case xml.EndElement:
						bpDepth--
					}
				}
				depth-- // we consumed the EndElement ourselves
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "positionH", "positionV":
				posTarget = ""
			}
		}
	}
	return info, nil
}

// scanFFData walks the children of an <w:fldChar w:fldCharType="begin">
// looking for a <w:ffData> sub-element. Returns nil if none found.
// Consumes up to and including the fldChar EndElement.
func scanFFData(dec *xml.Decoder, start xml.StartElement) *FormFieldInfo {
	var ff *FormFieldInfo
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return ff
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "ffData":
				if ff == nil {
					ff = &FormFieldInfo{}
				}
			case "name":
				if ff != nil {
					ff.Name = attr(t, "val")
				}
			case "textInput":
				if ff != nil {
					ff.Kind = "text"
				}
			case "checkBox":
				if ff != nil {
					ff.Kind = "checkbox"
				}
			case "ddList":
				if ff != nil {
					ff.Kind = "dropdown"
				}
			case "default":
				if ff != nil {
					v := attr(t, "val")
					switch ff.Kind {
					case "text":
						ff.Default = v
					case "checkbox":
						switch v {
						case "1", "true", "on":
							ff.Checked = true
						}
					case "dropdown":
						if x, err := strconv.Atoi(v); err == nil {
							ff.Selected = x
						}
					}
				}
			case "checked":
				if ff != nil && ff.Kind == "checkbox" {
					v := attr(t, "val")
					switch v {
					case "", "1", "true", "on":
						ff.Checked = true
					}
				}
			case "result":
				if ff != nil && ff.Kind == "dropdown" {
					v := attr(t, "val")
					if x, err := strconv.Atoi(v); err == nil {
						ff.Selected = x
					}
				}
			case "listEntry":
				if ff != nil && ff.Kind == "dropdown" {
					ff.Choices = append(ff.Choices, attr(t, "val"))
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return ff
}

// truncateText returns s clipped to maxRunes runes followed by an ellipsis.
func truncateText(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// shapeKindForPrst maps a DrawingML prstGeom preset to our VMLShape.Kind
// taxonomy. Unrecognized names route to "rect" so the renderer still
// draws SOMETHING in the right place.
func shapeKindForPrst(prst string) string {
	switch prst {
	case "rect":
		return "rect"
	case "roundRect", "round1Rect", "round2DiagRect", "round2SameRect":
		return "roundrect"
	case "ellipse":
		return "oval"
	case "line", "straightConnector1":
		return "line"
	case "bentConnector2", "bentConnector3", "bentConnector4", "bentConnector5":
		// Elbow connectors degrade to a straight line — we'd need the
		// per-connector geometry handles to plot the actual bend, which
		// require the avLst's adjustments table. A straight line keeps
		// the "X is linked to Y" intent visible at the correct endpoints.
		return "line"
	case "curvedConnector2", "curvedConnector3", "curvedConnector4", "curvedConnector5":
		// Curved connectors approximate with a straight line for the
		// same reason as the bent variants.
		return "line"
	case "triangle":
		return "triangle"
	case "rtTriangle":
		return "rtTriangle"
	case "parallelogram":
		return "parallelogram"
	case "trapezoid":
		return "trapezoid"
	case "diamond":
		return "diamond"
	case "pentagon":
		return "pentagon"
	case "hexagon":
		return "hexagon"
	case "heptagon":
		return "heptagon"
	case "octagon":
		return "octagon"
	case "star4":
		return "star4"
	case "star5":
		return "star5"
	case "star6":
		return "star6"
	case "star7":
		return "star7"
	case "star8":
		return "star8"
	case "star10":
		return "star10"
	case "star12":
		return "star12"
	case "star16":
		return "star16"
	case "star24":
		return "star24"
	case "star32":
		return "star32"
	case "rightArrow":
		return "rightArrow"
	case "leftArrow":
		return "leftArrow"
	case "upArrow":
		return "upArrow"
	case "downArrow":
		return "downArrow"
	case "leftRightArrow":
		return "leftRightArrow"
	case "upDownArrow":
		return "upDownArrow"
	case "bentArrow":
		return "bentArrow"
	case "callout1", "callout2", "callout3", "borderCallout1", "borderCallout2", "borderCallout3":
		return "callout"
	case "wedgeRectCallout":
		return "calloutRect"
	case "wedgeRoundRectCallout":
		return "calloutRoundRect"
	case "wedgeEllipseCallout":
		return "calloutEllipse"
	case "plus":
		return "plus"
	case "minus":
		return "minus"
	case "cloud":
		return "cloud"
	case "heart":
		return "heart"
	case "smileyFace":
		return "smiley"
	case "moon":
		return "moon"
	case "sun":
		return "sun"
	case "lightningBolt":
		return "lightning"
	case "noSmoking":
		return "noEntry"
	case "donut":
		return "donut"
	case "chevron":
		return "chevron"
	case "homePlate":
		return "homePlate"
	case "can":
		return "can"
	case "cube":
		return "cube"
	}
	if prst != "" {
		// Unknown but explicitly named — surface as a named primitive so
		// the renderer can decide. Defaults to rect outline.
		return "prst:" + prst
	}
	return "rect"
}

// scanSolidFillColor walks the children of an <a:solidFill> / <a:ln>
// looking for a color leaf and returns its hex value without the leading
// '#'. Recognizes a:srgbClr (direct hex), a:schemeClr (theme slot — only
// resolves to "" because callers without theme can't bind it), a:sysClr
// (lastClr), and a:prstClr. For schemeClr resolution use
// scanSolidFillColorTheme. Returns "" if no color found.
func scanSolidFillColor(dec *xml.Decoder, start xml.StartElement) string {
	return scanSolidFillColorTheme(dec, start, Theme{})
}

// scanSolidFillColorTheme is the theme-aware variant that resolves
// a:schemeClr against theme.Colors and applies any modifier chain
// (lumMod/lumOff/satMod/satOff/tint/shade) that sits under the color leaf.
func scanSolidFillColorTheme(dec *xml.Decoder, start xml.StartElement, theme Theme) string {
	return ScanColor(dec, start, theme)
}

// parseLineProps walks an <a:ln> subtree, capturing the stroke color from
// a nested <a:solidFill> plus the head/tail arrow types from <a:headEnd>
// and <a:tailEnd>. Coordinates inside the line are not consumed — only the
// attribute values we honor. On return the decoder has consumed up through
// </a:ln>.
func parseLineProps(dec *xml.Decoder, start xml.StartElement, theme Theme) (color, headEnd, tailEnd string) {
	props := parseLinePropsExt(dec, start, theme)
	return props.Color, props.HeadEnd, props.TailEnd
}

// LineProps mirrors a DrawingML <a:ln> block: stroke color, the head/tail
// arrow types, dash style, line cap and compound. Used by shape renderers
// to translate OOXML stroke semantics to PDF graphics state.
type LineProps struct {
	Color      string
	HeadEnd    string // none/triangle/stealth/diamond/oval/arrow
	TailEnd    string
	DashStyle  string // prstDash w:val: solid/dash/dashDot/lgDash/lgDashDot/lgDashDotDot/sysDash/sysDashDot/sysDashDotDot/sysDot/dot
	CapStyle   string // a:ln@cap: flat/rnd/sq
	CompoundLn string // a:ln@cmpd: sng/dbl/thickThin/thinThick/tri
}

// parseLinePropsExt is the modern entry point; parseLineProps is kept as
// a thin shim because many call sites only want color + arrow ends.
func parseLinePropsExt(dec *xml.Decoder, start xml.StartElement, theme Theme) LineProps {
	out := LineProps{}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "cap":
			out.CapStyle = a.Value
		case "cmpd":
			out.CompoundLn = a.Value
		}
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "solidFill":
				if c := scanSolidFillColorTheme(dec, t, theme); c != "" && out.Color == "" {
					out.Color = c
				}
				depth--
			case "headEnd":
				if v := attr(t, "type"); v != "" {
					out.HeadEnd = v
				}
				_ = dec.Skip()
			case "tailEnd":
				if v := attr(t, "type"); v != "" {
					out.TailEnd = v
				}
				_ = dec.Skip()
			case "prstDash":
				if v := attr(t, "val"); v != "" {
					out.DashStyle = v
				}
				_ = dec.Skip()
			case "custDash":
				// A custom dash pattern. We map it to "dash" generically;
				// renderers that want true custom stops can re-parse the
				// nested ds elements.
				if out.DashStyle == "" {
					out.DashStyle = "dash"
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

// flattenCustGeomPath walks an <a:custGeom>/<a:pathLst>/<a:path> subtree
// and returns a compact token string: "M x y L x y C x1 y1 x2 y2 x3 y3 Z".
// All coordinates are EMU-relative to the shape's <a:path @w @h> bounding
// box; the renderer normalizes to local 0..1 space at draw time.
func flattenCustGeomPath(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var pathW, pathH int64
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return b.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "path":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						pathW = x
					}
				}
				if v := attr(t, "h"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						pathH = x
					}
				}
			case "moveTo":
				appendPathCmd(dec, t, &b, "M", 1, pathW, pathH)
				depth--
			case "lnTo":
				appendPathCmd(dec, t, &b, "L", 1, pathW, pathH)
				depth--
			case "cubicBezTo":
				appendPathCmd(dec, t, &b, "C", 3, pathW, pathH)
				depth--
			case "quadBezTo":
				appendPathCmd(dec, t, &b, "Q", 2, pathW, pathH)
				depth--
			case "close":
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString("Z")
			}
		case xml.EndElement:
			depth--
		}
	}
	return b.String(), nil
}

// appendPathCmd consumes a path command subtree (moveTo/lnTo/...) and
// appends "<cmd> x y …" to b, normalizing each point to the 0..1 range
// against the path's declared w/h.
func appendPathCmd(dec *xml.Decoder, start xml.StartElement, b *strings.Builder, cmd string, points int, pathW, pathH int64) {
	pts := make([]string, 0, points*2)
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "pt" {
				normalize := func(name string, denom int64) string {
					v := attr(t, name)
					if v == "" {
						return "0"
					}
					if denom <= 0 {
						return v
					}
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						return strconv.FormatFloat(float64(x)/float64(denom), 'f', 4, 64)
					}
					return v
				}
				pts = append(pts, normalize("x", pathW))
				pts = append(pts, normalize("y", pathH))
			}
		case xml.EndElement:
			depth--
		}
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(cmd)
	for _, p := range pts {
		b.WriteByte(' ')
		b.WriteString(p)
	}
}

// readElementText returns the concatenated CharData inside the current
// element, consuming up to and including its EndElement. Used for
// alignment / offset values that live as text content.
func readElementText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb []byte
	for {
		tok, err := dec.Token()
		if err != nil {
			return string(sb), err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb = append(sb, t...)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return string(sb), nil
			}
		case xml.StartElement:
			// Unexpected nested element — skip to keep walker simple.
			_ = dec.Skip()
		}
	}
}

// extractTxbxRich parses a w:txbxContent subtree into a structured block
// tree (paragraphs / tables, with full run formatting) and also returns
// a flattened plain-text approximation for callers that only need the
// inline fallback. Consumes up to and including the matching EndElement.
//
// When doc is nil the parser still walks the tree, but resolved styles
// won't apply — block content is still produced because each paragraph
// stands on its own data.
func extractTxbxRich(dec *xml.Decoder, start xml.StartElement, doc *Document) (string, []Block, error) {
	pctx := &parseDocContext{doc: doc}
	if doc == nil {
		// parseNoteBody calls into decoders that nil-check doc; the few
		// that don't (style merges) will be no-ops without it.
		pctx.doc = &Document{}
	}
	blocks, err := parseNoteBody(dec, start, pctx)
	if err != nil {
		return "", nil, err
	}
	// Flatten visible text from the parsed blocks so the legacy TextBox
	// string fallback stays populated. Walk paragraphs in order; each
	// paragraph contributes its run text joined with no separator and
	// paragraphs are separated by a single space.
	var sb strings.Builder
	var walk func(bs []Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			switch v := b.(type) {
			case Paragraph:
				if sb.Len() > 0 {
					sb.WriteByte(' ')
				}
				for _, run := range v.Runs {
					if run.Text != "" && !run.FieldBegin && !run.FieldSep && !run.FieldEnd {
						sb.WriteString(run.Text)
					}
				}
			case Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	walk(blocks)
	return strings.TrimSpace(sb.String()), blocks, nil
}

// extractTxbxText concatenates the visible text inside a w:txbxContent
// subtree. Kept for callers that only need the flat fallback.
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
	IsHR     bool
	Shape    *VMLShape
	// OLEObjectRID, when non-empty, is the r:id of an embedded OLE object
	// (Excel range, equation, Visio). The caller can look it up in the
	// document's relationship map and dispatch to a specific reader (we
	// currently extract Excel ranges to small text grids).
	OLEObjectRID string
	// OLEProgID is the o:OLEObject@ProgID attribute when present (e.g.
	// "Excel.Sheet.12" / "Equation.3"). Used to dispatch the OLE blob to
	// a type-specific reader.
	OLEProgID string
}

// findPictInfo walks a w:pict subtree pulling out imagedata + shape info.
// Now handles v:group by recursing into each child shape. The doc handle
// is passed down so v:textbox can parse rich w:txbxContent.
func findPictInfo(dec *xml.Decoder, start xml.StartElement, doc *Document) (info pictInfo, err error) {
	// First, drain the immediate children: imagedata (RID) wins over shape;
	// a single shape element captures the outermost VMLShape, including any
	// nested group children.
	depth := 1
	for depth > 0 {
		tok, e := dec.Token()
		if e != nil {
			return info, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "shape", "rect", "roundrect", "oval", "line", "polyline", "group":
				sh, hr, rid, wpt, hpt := decodeVMLShape(dec, t, doc)
				if hr {
					info.IsHR = true
				}
				if rid != "" && info.RID == "" {
					info.RID = rid
				}
				if wpt > 0 {
					info.WPt = wpt
				}
				if hpt > 0 {
					info.HPt = hpt
				}
				if sh != nil && info.Shape == nil && info.RID == "" {
					info.Shape = sh
				}
				continue
			case "OLEObject", "objectLink", "objectEmbed":
				// Inside w:object: <o:OLEObject r:id="…" ProgID="…"/>
				// tells us the OLE blob's relationship id and its server
				// program. Capture both so the caller can look up the
				// embedded part and emit type-specific content (Excel
				// range, equation, etc.).
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						if info.OLEObjectRID == "" {
							info.OLEObjectRID = a.Value
						}
					case "ProgID":
						info.OLEProgID = a.Value
					}
				}
				_ = dec.Skip()
				continue
			case "imagedata":
				// Bare imagedata directly under pict — uncommon but
				// possible; capture rid.
				for _, a := range t.Attr {
					if a.Name.Local == "id" || a.Name.Local == "relid" {
						info.RID = a.Value
						break
					}
				}
				_ = dec.Skip()
				continue
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return info, nil
}

// decodeVMLShape parses a single VML shape element subtree (recursively
// for v:group). Returns the constructed shape (nil if it was a pure
// imagedata), an isHorizontalRule flag, a captured imagedata RID, and
// the shape's width/height in points. Consumes the matching EndElement.
// doc, when non-nil, is forwarded to the textbox content extractor so
// rich w:txbxContent (paragraphs / tables / formatting) survives.
func decodeVMLShape(dec *xml.Decoder, start xml.StartElement, doc *Document) (*VMLShape, bool, string, float64, float64) {
	kind := start.Name.Local
	if kind == "shape" {
		kind = "rect"
	}
	sh := &VMLShape{Kind: kind}
	var rid string
	var isHR bool

	if style := attr(start, "style"); style != "" {
		w, h := parseVMLSize(style)
		if w > 0 {
			sh.WidthPt = w
		}
		if h > 0 {
			sh.HeightPt = h
		}
	}
	if attr(start, "hr") == "t" {
		isHR = true
	}
	if v := attr(start, "fillcolor"); v != "" {
		sh.FillColor = strings.TrimPrefix(strings.ToUpper(v), "#")
	}
	if v := attr(start, "strokecolor"); v != "" {
		sh.StrokeColor = strings.TrimPrefix(strings.ToUpper(v), "#")
	}
	if v := attr(start, "strokeweight"); v != "" {
		sh.StrokeWeightPt = parseCSSLength(v)
	}
	if v := attr(start, "filled"); v == "false" || v == "f" {
		sh.FillColor = ""
	}
	if v := attr(start, "stroked"); v == "false" || v == "f" {
		sh.StrokeColor = ""
	}
	if v := attr(start, "points"); v != "" {
		sh.Points = v
	}
	if v := attr(start, "arcsize"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			if x > 1 {
				x /= 65536.0
			}
			sh.CornerArc = x
		}
	}

	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "shape", "rect", "roundrect", "oval", "line", "polyline", "group":
				// Nested child shape (most common inside v:group).
				child, childHR, childRID, _, _ := decodeVMLShape(dec, t, doc)
				if childHR {
					isHR = true
				}
				if rid == "" {
					rid = childRID
				}
				if child != nil {
					sh.Children = append(sh.Children, *child)
				}
				continue
			case "imagedata":
				for _, a := range t.Attr {
					if a.Name.Local == "id" || a.Name.Local == "relid" {
						rid = a.Value
						break
					}
				}
				_ = dec.Skip()
				continue
			case "fill":
				if v := attr(t, "color"); v != "" {
					sh.FillColor = strings.TrimPrefix(strings.ToUpper(v), "#")
				}
				_ = dec.Skip()
				continue
			case "stroke":
				if v := attr(t, "color"); v != "" {
					sh.StrokeColor = strings.TrimPrefix(strings.ToUpper(v), "#")
				}
				if v := attr(t, "weight"); v != "" {
					sh.StrokeWeightPt = parseCSSLength(v)
				}
				_ = dec.Skip()
				continue
			case "textbox":
				txt, blocks := readVMLTextbox(dec, t, doc)
				if txt != "" {
					if sh.TextBox != "" {
						sh.TextBox += " "
					}
					sh.TextBox += txt
				}
				if len(blocks) > 0 {
					sh.TextBoxBlocks = append(sh.TextBoxBlocks, blocks...)
				}
				continue
			case "textpath":
				// Legacy WordArt: <v:textpath string="..."/>. We extract the
				// text and append it to the shape's TextBox; the surrounding
				// curved-path rendering is lost (we'd need a path-aware glyph
				// drawer), but the readable content survives.
				if s := attr(t, "string"); s != "" {
					if sh.TextBox != "" {
						sh.TextBox += " "
					}
					sh.TextBox += s
				}
				_ = dec.Skip()
				continue
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	if start.Name.Local == "group" {
		sh.Kind = "group"
	}
	if rid != "" {
		return nil, isHR, rid, sh.WidthPt, sh.HeightPt
	}
	return sh, isHR, "", sh.WidthPt, sh.HeightPt
}

// readVMLTextbox drains a <v:textbox> subtree, returning both a flat
// text string and the structured w:txbxContent block tree (when
// present). The block path lets the renderer paint bold/list/table
// content inside legacy VML textboxes too — Word still emits these for
// Outlook / older converters.
func readVMLTextbox(dec *xml.Decoder, start xml.StartElement, doc *Document) (string, []Block) {
	var flat strings.Builder
	var blocks []Block
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return strings.TrimSpace(flat.String()), blocks
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "txbxContent" {
				txt, bs, e := extractTxbxRich(dec, t, doc)
				if e != nil {
					return strings.TrimSpace(flat.String()), blocks
				}
				if txt != "" {
					if flat.Len() > 0 {
						flat.WriteByte(' ')
					}
					flat.WriteString(txt)
				}
				if len(bs) > 0 {
					blocks = append(blocks, bs...)
				}
				continue
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			flat.Write(t)
			flat.WriteByte(' ')
		}
	}
	return strings.TrimSpace(flat.String()), blocks
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
			case "sdt":
				// w:sdtRow / w:sdtBlock wrapped around table rows. Word
				// uses this for row-level content controls (typically
				// w15:repeatingSection inside a sdtPr). We unwrap the
				// sdtContent and pick up the contained tr elements.
				rows, err := decodeSdtRowsInTable(dec, t, pctx)
				if err != nil {
					return tbl, err
				}
				tbl.Rows = append(tbl.Rows, rows...)
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

// decodeSdtRowsInTable unwraps a w:sdt that sits between rows of a table
// (the row-level content control). We walk down through w:sdtContent to
// pick out the w:tr children, decoding them as normal rows. w:sdtPr is
// skipped — the styling metadata it carries isn't visualized for PDF
// export. When the sdt is a w15:repeatingSection we still produce only
// the literal rows it contains; the OpenDoPE path duplicates rows
// data-bound elsewhere.
func decodeSdtRowsInTable(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]TableRow, error) {
	var rows []TableRow
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return rows, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tr":
				row, err := decodeRow(dec, t, pctx)
				if err != nil {
					return rows, err
				}
				rows = append(rows, row)
			case "sdtContent", "sdt":
				// Recurse: nested sdt wrappers (e.g. repeatingSection holding
				// rows that themselves contain sdt-wrapped cells) need to be
				// unwrapped before reaching the tr nodes.
				depth++
				_ = t // consumed by re-entering the loop
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			depth--
		}
	}
	return rows, nil
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
		// We may still need to apply per-row tblPrEx borders below.
		anyEx := false
		for ri := range tbl.Rows {
			if tbl.Rows[ri].TblPrEx != nil && tbl.Rows[ri].TblPrEx.HasBorders {
				anyEx = true
				break
			}
		}
		if !anyEx {
			return
		}
	}
	nRows := len(tbl.Rows)
	totalCols := len(tbl.ColumnWidthsTwips)
	for ri := range tbl.Rows {
		row := &tbl.Rows[ri]
		// Per ECMA-376 §17.4.39 precedence:
		//   cell tcBorders > row tblPrEx > tblBorders.
		// trPr itself has no border block, so it slots between tblPrEx
		// and tblPr only via tblPrEx's own borders (which the row
		// declares).
		effective := tbl.Borders
		if row.TblPrEx != nil && row.TblPrEx.HasBorders {
			effective = mergeTableBorders(tbl.Borders, row.TblPrEx.Borders)
		}
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
					cell.Borders.Top = effective.Top
				} else {
					cell.Borders.Top = effective.InsideH
				}
			}
			if !cell.Borders.Bottom.Has() {
				if lastRow {
					cell.Borders.Bottom = effective.Bottom
				} else {
					cell.Borders.Bottom = effective.InsideH
				}
			}
			if !cell.Borders.Left.Has() {
				if firstCol {
					cell.Borders.Left = effective.Left
				} else {
					cell.Borders.Left = effective.InsideV
				}
			}
			if !cell.Borders.Right.Has() {
				if lastCol {
					cell.Borders.Right = effective.Right
				} else {
					cell.Borders.Right = effective.InsideV
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
			case "tblLayout":
				tbl.Layout = attr(t, "type")
			case "tblOverlap":
				tbl.Overlap = attr(t, "val")
			case "tblCaption":
				tbl.Caption = attr(t, "val")
			case "tblDescription":
				tbl.Description = attr(t, "val")
			case "tblW":
				tbl.TableWidthType = attr(t, "type")
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						tbl.TableWidthTwips = x
					}
				}
			case "tblpPr":
				fp := &TableFloatPos{
					HAnchor: attr(t, "horzAnchor"),
					VAnchor: attr(t, "vertAnchor"),
					XAlign:  attr(t, "tblpXSpec"),
					YAlign:  attr(t, "tblpYSpec"),
				}
				parseInt := func(name string) int {
					if v := attr(t, name); v != "" {
						if x, err := strconv.Atoi(v); err == nil {
							return x
						}
					}
					return 0
				}
				fp.XTwips = parseInt("tblpX")
				fp.YTwips = parseInt("tblpY")
				fp.LeftFromTextTwips = parseInt("leftFromText")
				fp.RightFromTextTwips = parseInt("rightFromText")
				fp.TopFromTextTwips = parseInt("topFromText")
				fp.BottomFromTextTwips = parseInt("bottomFromText")
				tbl.FloatPos = fp
			case "tblCellMar":
				if err := decodeTblCellMar(dec, t, &tbl.DefaultCellMargins); err != nil {
					return err
				}
				continue
			case "tblInd":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						tbl.IndentTwips = x
					}
				}
			case "tblCellSpacing":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						tbl.CellSpacingTwips = x
					}
				}
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
			case "jc":
				tbl.Alignment = attr(t, "val")
			case "bidiVisual":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" || v == "on" {
					tbl.BidiVisual = true
				}
			case "shd":
				if v := attr(t, "fill"); v != "" && v != "auto" {
					tbl.Shading = v
				}
			case "tblStyleRowBandSize":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						tbl.RowBandSize = x
					}
				}
			case "tblStyleColBandSize":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						tbl.ColBandSize = x
					}
				}
			case "tblPrChange":
				tbl.PrChange = readPrChangeAttrs(t, "tblPr")
			case "tblPrExChange":
				if tbl.PrChange == nil {
					tbl.PrChange = readPrChangeAttrs(t, "tblPrEx")
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

// decodeTblCellMar reads w:tblCellMar — table-default cell margins.
func decodeTblCellMar(dec *xml.Decoder, start xml.StartElement, m *CellMargins) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			pt := 0.0
			if v := attr(t, "w"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					pt = float64(x) / 20.0
				}
			}
			switch t.Name.Local {
			case "top":
				m.Top = pt
			case "bottom":
				m.Bottom = pt
			case "left", "start":
				m.Left = pt
			case "right", "end":
				m.Right = pt
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
			switch t.Name.Local {
			case "gridCol":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						tbl.ColumnWidthsTwips = append(tbl.ColumnWidthsTwips, x)
					}
				}
			case "tblGridChange":
				// Tracked change of the grid column list. We don't replace
				// our current widths — caller wants the post-edit grid —
				// but record the change so a change bar can be drawn.
				if tbl.PrChange == nil {
					tbl.PrChange = readPrChangeAttrs(t, "tblGrid")
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
			case "tblPrEx":
				ex, err := decodeTblPrEx(dec, t)
				if err != nil {
					return row, err
				}
				row.TblPrEx = ex
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

// decodeTblPrEx reads w:tblPrEx — per-row table-property exceptions.
// Word writes these when a row carries property overrides that don't apply
// to the rest of the table (typically after copy/paste from a foreign
// table). Border conflict resolution uses these in precedence:
//
//	tcBorders > trPr > tblPrEx > tblPr  (per ECMA-376 §17.4.39).
func decodeTblPrEx(dec *xml.Decoder, start xml.StartElement) (*TblPrEx, error) {
	ex := &TblPrEx{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return ex, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			flagSet := func(a string) bool { return a == "1" || a == "true" }
			switch t.Name.Local {
			case "tblBorders":
				if err := decodeTableBorders(dec, t, &ex.Borders); err != nil {
					return ex, err
				}
				ex.HasBorders = true
				continue
			case "tblCellMar":
				if err := decodeTblCellMar(dec, t, &ex.CellMargins); err != nil {
					return ex, err
				}
				ex.HasCellMargins = true
				continue
			case "tblCellSpacing":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ex.CellSpacingTwips = x
					}
				}
			case "tblInd":
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						ex.IndentTwips = x
					}
				}
			case "tblLayout":
				ex.Layout = attr(t, "type")
			case "shd":
				if v := attr(t, "fill"); v != "" && v != "auto" {
					ex.Shading = v
				}
			case "tblLook":
				if flagSet(attr(t, "firstRow")) {
					ex.Look.FirstRow = true
				}
				if flagSet(attr(t, "lastRow")) {
					ex.Look.LastRow = true
				}
				if flagSet(attr(t, "firstColumn")) {
					ex.Look.FirstColumn = true
				}
				if flagSet(attr(t, "lastColumn")) {
					ex.Look.LastColumn = true
				}
				if flagSet(attr(t, "noHBand")) {
					ex.Look.NoHBand = true
				}
				if flagSet(attr(t, "noVBand")) {
					ex.Look.NoVBand = true
				}
				ex.HasLook = true
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return ex, nil
			}
		}
	}
}

func decodeTrPr(dec *xml.Decoder, start xml.StartElement, row *TableRow) error {
	parseTwips := func(t xml.StartElement) int {
		if v := attr(t, "w"); v != "" {
			if x, err := strconv.Atoi(v); err == nil {
				return x
			}
		}
		return 0
	}
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
				row.HeightRule = attr(t, "hRule")
				if row.HeightRule == "exact" {
					row.HeightRuleExact = true
				}
			case "cantSplit":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" {
					row.CantSplit = true
				}
			case "wBefore":
				row.WBeforeTwips = parseTwips(t)
			case "wAfter":
				row.WAfterTwips = parseTwips(t)
			case "gridBefore":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						row.GridBefore = x
					}
				}
			case "gridAfter":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil && x > 0 {
						row.GridAfter = x
					}
				}
			case "tblCellSpacing":
				row.CellSpacingTwips = parseTwips(t)
			case "jc":
				row.Alignment = attr(t, "val")
			case "hidden":
				v := attr(t, "val")
				if v == "" || v == "1" || v == "true" || v == "on" {
					row.Hidden = true
				}
			case "cnfStyle":
				row.CnfStyle = parseCnfStyle(t)
			case "trPrChange":
				row.PrChange = readPrChangeAttrs(t, "trPr")
			case "trPrExChange":
				if row.PrChange == nil {
					row.PrChange = readPrChangeAttrs(t, "trPrEx")
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
			case "hMerge":
				v := attr(t, "val")
				if v == "" {
					v = "continue"
				}
				cell.HMerge = v
				_ = dec.Skip()
			case "tcW":
				cell.CellWidthType = attr(t, "type")
				if v := attr(t, "w"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						cell.CellWidthTwips = x
					}
				}
				_ = dec.Skip()
			case "shd":
				if v := attr(t, "fill"); v != "" && v != "auto" {
					cell.Shading = v
				}
				_ = dec.Skip()
			case "vAlign":
				cell.VAlign = attr(t, "val")
				_ = dec.Skip()
			case "textDirection":
				cell.TextDirection = attr(t, "val")
				_ = dec.Skip()
			case "noWrap":
				cell.NoWrap = onOff(t)
				_ = dec.Skip()
			case "hideMark":
				cell.HideMark = onOff(t)
				_ = dec.Skip()
			case "tcFitText":
				cell.FitText = onOff(t)
				_ = dec.Skip()
			case "tcBorders":
				if err := decodeCellBorders(dec, t, &cell.Borders); err != nil {
					return err
				}
			case "tcMar":
				if err := decodeTcMar(dec, t, cell); err != nil {
					return err
				}
			case "cnfStyle":
				cell.CnfStyle = parseCnfStyle(t)
				_ = dec.Skip()
			case "tcPrChange":
				cell.PrChange = readPrChangeAttrs(t, "tcPr")
				_ = dec.Skip()
			case "cellIns", "cellDel", "cellMerge":
				kind := "ins"
				switch t.Name.Local {
				case "cellDel":
					kind = "del"
				case "cellMerge":
					kind = "merge"
				}
				rev := &CellRevision{
					Kind:     kind,
					ID:       attr(t, "id"),
					Author:   attr(t, "author"),
					Date:     attr(t, "date"),
					Vertical: attr(t, "vMerge"),
				}
				cell.CellRevision = rev
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

// parseCnfStyle reads a w:cnfStyle element. Per ECMA-376 the field is
// either a 12-character binary string in w:val (firstRow / lastRow /
// firstColumn / lastColumn / band1Vert / band2Vert / band1Horz / band2Horz
// / nwCell / neCell / swCell / seCell, leftmost = firstRow) or a set of
// individual on/off attributes. We support both since both occur in real
// documents.
func parseCnfStyle(t xml.StartElement) CnfStyle {
	flagOn := func(a string) bool {
		switch a {
		case "1", "true", "on":
			return true
		}
		return false
	}
	c := CnfStyle{
		FirstRow:    flagOn(attr(t, "firstRow")),
		LastRow:     flagOn(attr(t, "lastRow")),
		FirstColumn: flagOn(attr(t, "firstColumn")),
		LastColumn:  flagOn(attr(t, "lastColumn")),
		Band1Vert:   flagOn(attr(t, "oddVBand")) || flagOn(attr(t, "band1Vert")),
		Band2Vert:   flagOn(attr(t, "evenVBand")) || flagOn(attr(t, "band2Vert")),
		Band1Horz:   flagOn(attr(t, "oddHBand")) || flagOn(attr(t, "band1Horz")),
		Band2Horz:   flagOn(attr(t, "evenHBand")) || flagOn(attr(t, "band2Horz")),
		NWCell:      flagOn(attr(t, "firstRowFirstColumn")) || flagOn(attr(t, "nwCell")),
		NECell:      flagOn(attr(t, "firstRowLastColumn")) || flagOn(attr(t, "neCell")),
		SWCell:      flagOn(attr(t, "lastRowFirstColumn")) || flagOn(attr(t, "swCell")),
		SECell:      flagOn(attr(t, "lastRowLastColumn")) || flagOn(attr(t, "seCell")),
	}
	if v := attr(t, "val"); len(v) == 12 {
		on := func(i int) bool { return v[i] == '1' }
		if on(0) {
			c.FirstRow = true
		}
		if on(1) {
			c.LastRow = true
		}
		if on(2) {
			c.FirstColumn = true
		}
		if on(3) {
			c.LastColumn = true
		}
		if on(4) {
			c.Band1Vert = true
		}
		if on(5) {
			c.Band2Vert = true
		}
		if on(6) {
			c.Band1Horz = true
		}
		if on(7) {
			c.Band2Horz = true
		}
		if on(8) {
			c.NECell = true
		}
		if on(9) {
			c.NWCell = true
		}
		if on(10) {
			c.SECell = true
		}
		if on(11) {
			c.SWCell = true
		}
	}
	return c
}

// decodePgBorders is structurally identical to decodeCellBorders, just over
// a PageBorders target — Word reuses the border-edge XML schema. Reads
// the parent element's w:offsetFrom attribute and each edge's w:space to
// compute the inset.
func decodePgBorders(dec *xml.Decoder, start xml.StartElement, b *PageBorders) error {
	if v := attr(start, "offsetFrom"); v == "text" {
		b.OffsetFromText = true
	}
	if v := attr(start, "display"); v != "" {
		b.Display = v
	}
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
			// w:space is the inset from the reference edge (page or text)
			// in POINTS, not twips.
			offsetPt := 24.0
			if v := attr(t, "space"); v != "" {
				if x, err := strconv.Atoi(v); err == nil {
					offsetPt = float64(x)
				}
			}
			switch t.Name.Local {
			case "top":
				b.Top = edge
				b.OffsetTopPt = offsetPt
			case "bottom":
				b.Bottom = edge
				b.OffsetBottomPt = offsetPt
			case "left":
				b.Left = edge
				b.OffsetLeftPt = offsetPt
			case "right":
				b.Right = edge
				b.OffsetRightPt = offsetPt
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
			case "tl2br":
				b.TL2BR = edge
			case "tr2bl":
				b.TR2BL = edge
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
				if v := attr(t, "chapStyle"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.PageNumber.ChapStyle = x
					}
				}
				if v := attr(t, "chapSep"); v != "" {
					sec.PageNumber.ChapSep = v
				}
				_ = dec.Skip()
			case "vAlign":
				sec.VAlign = attr(t, "val")
				_ = dec.Skip()
			case "docGrid":
				sec.DocGrid.Type = attr(t, "type")
				if v := attr(t, "linePitch"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.DocGrid.LinePitch = x
					}
				}
				if v := attr(t, "charSpace"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						sec.DocGrid.CharSpace = x
					}
				}
				_ = dec.Skip()
			case "formProt":
				sec.FormProt = onOff(t)
				_ = dec.Skip()
			case "rtlGutter":
				sec.RtlGutter = onOff(t)
				_ = dec.Skip()
			case "footnotePr":
				nc, err := decodeNoteConfig(dec, t)
				if err != nil {
					return err
				}
				sec.FootnotePr = nc
			case "endnotePr":
				nc, err := decodeNoteConfig(dec, t)
				if err != nil {
					return err
				}
				sec.EndnotePr = nc
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
				if v := attr(t, "sep"); v == "1" || v == "true" {
					sec.ColumnSeparator = true
				}
				if v := attr(t, "equalWidth"); v == "0" || v == "false" {
					sec.ColumnEqualWidth = false
				} else {
					sec.ColumnEqualWidth = true
				}
				// Children: <w:col w:w="…" w:space="…"/> — per-column widths
				// when equalWidth=false. We collect them and stash on the
				// section; renderer falls back to equal distribution when
				// the slice is empty.
				if err := decodeColsChildren(dec, t, sec); err != nil {
					return err
				}
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
			case "sectPrChange":
				sec.PrChange = readPrChangeAttrs(t, "sectPr")
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

// decodeColsChildren reads <w:col w:w="…" w:space="…"/> children of a
// w:cols element into sec.ColumnSpecs. The decoder cursor must already be
// positioned past the w:cols start tag.
func decodeColsChildren(dec *xml.Decoder, start xml.StartElement, sec *Section) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "col" {
				spec := ColumnSpec{}
				if v := attr(t, "w"); v != "" {
					if x, e := strconv.Atoi(v); e == nil {
						spec.WidthTwips = x
					}
				}
				if v := attr(t, "space"); v != "" {
					if x, e := strconv.Atoi(v); e == nil {
						spec.SpaceTwips = x
					}
				}
				sec.ColumnSpecs = append(sec.ColumnSpecs, spec)
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeMathPr reads <m:mathPr> from settings.xml into MathProps. Each
// child stores its value in @m:val; some are valueless flags. Captures
// the subset that affects layout / font choice.
func decodeMathPr(dec *xml.Decoder, start xml.StartElement, out *MathProps) error {
	intVal := func(t xml.StartElement) int {
		if v := attr(t, "val"); v != "" {
			if x, err := strconv.Atoi(v); err == nil {
				return x
			}
		}
		return 0
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "mathFont":
				if v := attr(t, "val"); v != "" {
					out.MathFont = v
				}
			case "brkBin":
				out.BrkBin = attr(t, "val")
			case "brkBinSub":
				out.BrkBinSub = attr(t, "val")
			case "smallFrac":
				out.SmallFrac = attr(t, "val") != "0" && attr(t, "val") != "false"
			case "dispDef":
				out.DispDef = attr(t, "val") != "0" && attr(t, "val") != "false"
			case "lMargin":
				out.LMargin = intVal(t)
			case "rMargin":
				out.RMargin = intVal(t)
			case "defJc":
				out.DefJc = attr(t, "val")
			case "wrapIndent":
				out.WrapIndent = intVal(t)
			case "wrapRight":
				out.WrapRight = true
			case "intLim":
				out.IntLim = attr(t, "val")
			case "naryLim":
				out.NaryLim = attr(t, "val")
			case "preSp":
				out.PreSp = intVal(t)
			case "postSp":
				out.PostSp = intVal(t)
			case "interSp":
				out.InterSp = intVal(t)
			case "intraSp":
				out.IntraSp = intVal(t)
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeNoteConfig parses a w:footnotePr / w:endnotePr block into NoteConfig.
// Children we recognize: w:pos, w:numFmt, w:numStart, w:numRestart.
func decodeNoteConfig(dec *xml.Decoder, start xml.StartElement) (*NoteConfig, error) {
	nc := &NoteConfig{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nc, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pos":
				nc.Pos = attr(t, "val")
			case "numFmt":
				nc.NumFmt = attr(t, "val")
			case "numStart":
				if v := attr(t, "val"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						nc.NumStart = x
					}
				}
			case "numRestart":
				nc.Restart = attr(t, "val")
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nc, nil
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

// onOffValAttr returns the boolean reading of a w:val attribute. Same
// rules as onOff except this is intended for elements where the attr is
// the only data the caller cares about.
func onOffValAttr(se xml.StartElement) bool { return onOff(se) }

// onOffPtr returns a pointer to the boolean reading of w:val so callers
// can distinguish "absent" (nil) from "explicit false". Used for
// CompatOptions where each flag carries semantic meaning either way.
func onOffPtr(se xml.StartElement) *bool {
	v := onOff(se)
	return &v
}

// decodeCompat reads a w:compat block, picking up the subset of options
// we model on CompatOptions. Unknown children are silently skipped.
func decodeCompat(dec *xml.Decoder, start xml.StartElement, s *Settings) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "doNotExpandShiftReturn":
				s.Compat.DoNotExpandShiftReturn = onOffPtr(t)
			case "useSingleBorderforContiguousCells":
				s.Compat.UseSingleBorderForContiguousCells = onOffPtr(t)
			case "growAutofit":
				s.Compat.GrowAutofit = onOffPtr(t)
			case "noLeading":
				s.Compat.NoLeading = onOffPtr(t)
			case "spacingInWholePoints":
				s.Compat.SpacingInWholePoints = onOffPtr(t)
			case "balanceSingleByteDoubleByteWidth":
				s.Compat.BalanceSingleByteDoubleByteWidth = onOffPtr(t)
			case "doNotUseEastAsianBreakRules":
				s.Compat.DoNotUseEastAsianBreakRules = onOffPtr(t)
			case "suppressTopSpacing":
				s.Compat.SuppressTopSpacing = onOffPtr(t)
			case "ulTrailSpace":
				s.Compat.UlTrailSpace = onOffPtr(t)
			case "doNotLeaveBackslashAlone":
				s.Compat.DoNotLeaveBackslashAlone = onOffPtr(t)
			case "useFELayout":
				s.Compat.UseFELayout = onOffPtr(t)
			case "spaceForUL":
				s.Compat.SpaceForUL = onOffPtr(t)
			case "underlineTabInNumList":
				s.Compat.UnderlineTabInNumList = onOffPtr(t)
			case "doNotBreakWrappedTables":
				s.Compat.DoNotBreakWrappedTables = onOffPtr(t)
			case "doNotUseHTMLParagraphAutoSpacing":
				s.Compat.DoNotUseHTMLParagraphAutoSpacing = onOffPtr(t)
			case "adjustLineHeightInTable":
				s.Compat.AdjustLineHeightInTable = onOffPtr(t)
			}
			_ = dec.Skip()
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
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
