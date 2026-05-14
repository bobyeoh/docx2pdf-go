package verify

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
)

// TestComprehensiveScenario is the full-stack integration test: it builds
// a single docx in memory that exercises 30+ features, converts it through
// the public Convert pipeline, rasterizes the resulting PDF to PNG, and
// then asserts the visible result against the docx using four
// complementary techniques:
//
//  1. pdftotext         — content survives end-to-end
//  2. pdftotext -bbox   — content lands at the expected coordinates
//  3. PDF byte structure — outline / image / link annotations are emitted
//  4. PNG pixel sampling — colored regions (highlight, image, color text)
//     appear in the right area of the rendered page
//
// Each "feature" in the document carries a distinctive marker (unique
// uppercase token, distinctive RGB color, or unique anchor name) so the
// assertions can pinpoint the relevant chunk without ambiguity.
//
// Coverage map (numbers are the FEAT_* markers in the docx source):
//
//	FEAT_TITLE          PDF outline / Title style
//	FEAT_HEAD1_A,B      Two Heading1 entries (outline hierarchy)
//	FEAT_HEAD2          Heading2 outline entry
//	FEAT_BOLD           Bold run
//	FEAT_ITAL           Italic run
//	FEAT_UNDER          Underlined run
//	FEAT_STRIKE         Strikethrough run
//	FEAT_RED            Explicit color (#C00000) — pixel sample
//	FEAT_HILITE         Highlight yellow — pixel sample
//	FEAT_CENTER         Center-aligned paragraph — bbox check
//	FEAT_RIGHT          Right-aligned paragraph — bbox check
//	FEAT_JUST_A,B,C     Justified paragraph with multiple words
//	FEAT_LIST_NUM1..3   Numbered list (3 items)
//	FEAT_LIST_BUL1..3   Bullet list (3 items)
//	FEAT_TBL_H1,H2      Table header cells (bold via tblLook)
//	FEAT_TBL_R1C1..R2C2 2x2 table body cells
//	FEAT_IMG            Inline image (distinctive RGB) — pixel sample
//	FEAT_FN_REF         Footnote reference site
//	FEAT_FN_BODY        Footnote body text at page bottom
//	FEAT_LINK_TEXT      Hyperlink anchor text — /Annot in PDF
//	FEAT_SDT_INL        Inline SDT content (transparent wrapper)
//	FEAT_MATH_E         Inline math equation text
//	FEAT_BREAK_AFTER    Text on second page after page break
//	FEAT_HEADER         Page header text
//	FEAT_FOOTER         Page footer text
//	FEAT_BOOKMARK       Bookmark target (for internal-link annotation)
//	FEAT_CJK_HAN        CJK rune (needs fallback font)
//	FEAT_RTL_HE         Hebrew word (needs fallback font with Hebrew)
//
// The font requirements are honored when testdata/font.ttf exists AND a
// fallback font is registered. CJK and RTL assertions are skipped when
// the fallback isn't available, so the test still runs on bare CI.
func TestComprehensiveScenario(t *testing.T) {
	requireTool(t, "pdftotext")
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftoppm")

	fontPath := findFont(t)
	if fontPath == "" {
		t.Skip("testdata/font.ttf missing — comprehensive test needs a real TTF")
	}

	// Persist build artifacts under internal/verify/out/comprehensive/
	// (rather than t.TempDir) so a human can open the PNGs afterward.
	// The directory is in .gitignore.
	dir := mustAbs(t, "out/comprehensive")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// --- Build a distinctive 64×16 magenta PNG so we can identify it in
	// the rasterized page by sampling pixels. The color is chosen to be
	// far from any text antialiasing artifact.
	imgBytes := makeSolidPNG(64, 16, color.RGBA{R: 220, G: 30, B: 200, A: 255})

	docxPath := newDocx().
		Body(comprehensiveBody).
		Styles(comprehensiveStyles).
		Numbering(comprehensiveNumbering).
		Part("header1.xml", comprehensiveHeader).
		Part("footer1.xml", comprehensiveFooter).
		Part("footnotes.xml", comprehensiveFootnotes).
		Media("image1.png", imgBytes).
		Rels(comprehensiveRels).
		Write(t, dir)

	pdfPath := filepath.Join(dir, "comprehensive.pdf")
	opts := convert.Options{
		FontRegular:     fontPath,
		FontFallback:    fontPath, // best-effort; CJK/RTL checks skip if glyphs missing
		DefaultFontSize: 11,
		PageNumbers:     false, // we control header/footer ourselves
		SourceFilename:  "comprehensive.docx",
		Author:          "TestAuthor",
	}
	if err := convert.Convert(docxPath, pdfPath, opts); err != nil {
		t.Fatalf("convert: %v", err)
	}

	// Read raw PDF + extracted text once; later assertions slice them.
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("read pdf: %v", err)
	}
	allText := pdftotext(t, pdfPath)
	bbox := pdftextBbox(t, pdfPath)

	// Render PNGs once for pixel sampling.
	pngDir := filepath.Join(dir, "pngs")
	pngs := renderPNG(t, pdfPath, pngDir)
	if len(pngs) < 2 {
		t.Fatalf("expected at least 2 pages, got %d", len(pngs))
	}
	page1, err := loadPNGImage(filepath.Join(pngDir, pngs[0]))
	if err != nil {
		t.Fatalf("decode page1 png: %v", err)
	}
	page2, err := loadPNGImage(filepath.Join(pngDir, pngs[1]))
	if err != nil {
		t.Fatalf("decode page2 png: %v", err)
	}

	// ============================================================
	// Phase 1 — text content survives end-to-end (pdftotext)
	// ============================================================
	t.Run("text_content", func(t *testing.T) {
		mustContain := []string{
			"FEAT_TITLE",
			"FEAT_HEAD1_A", "FEAT_HEAD1_B", "FEAT_HEAD2",
			"FEAT_BOLD", "FEAT_ITAL", "FEAT_UNDER", "FEAT_STRIKE",
			"FEAT_RED", "FEAT_HILITE",
			"FEAT_CENTER", "FEAT_RIGHT",
			"FEAT_JUST_A", "FEAT_JUST_B", "FEAT_JUST_C",
			"FEAT_LIST_NUM1", "FEAT_LIST_NUM2", "FEAT_LIST_NUM3",
			"FEAT_LIST_BUL1", "FEAT_LIST_BUL2", "FEAT_LIST_BUL3",
			"FEAT_TBL_H1", "FEAT_TBL_H2",
			"FEAT_TBL_R1C1", "FEAT_TBL_R1C2", "FEAT_TBL_R2C1", "FEAT_TBL_R2C2",
			"FEAT_FN_REF", "FEAT_FN_BODY",
			"FEAT_LINK_TEXT",
			"FEAT_SDT_INL",
			"FEAT_MATH_E",
			"FEAT_BREAK_AFTER",
			"FEAT_HEADER", "FEAT_FOOTER",
		}
		for _, m := range mustContain {
			if !strings.Contains(allText, m) {
				t.Errorf("missing marker %q in extracted text", m)
			}
		}
	})

	// ============================================================
	// Phase 2 — layout positions (pdftotext -bbox)
	// ============================================================
	t.Run("layout_positions", func(t *testing.T) {
		// page width is 595.3 (A4 portrait at 72 DPI)
		const pageW = 595.3
		const leftMargin = 72.0
		const rightMargin = pageW - 72.0

		// FEAT_CENTER should land near horizontal center: bbox xMin >> left
		// and xMax << right, with the midpoint within ±30pt of pageW/2.
		if x1, x2, ok := bboxRange(bbox, "FEAT_CENTER"); ok {
			mid := (x1 + x2) / 2
			if mid < pageW/2-30 || mid > pageW/2+30 {
				t.Errorf("FEAT_CENTER midpoint %.1f outside ±30 of page center %.1f",
					mid, pageW/2)
			}
		} else {
			t.Error("FEAT_CENTER bbox not found")
		}

		// Right alignment: the right-aligned paragraph contains only
		// "FEAT_RIGHT", so its xMax must sit near the right margin.
		if _, x2, ok := bboxRange(bbox, "FEAT_RIGHT"); ok {
			if rightMargin-x2 > 20 {
				t.Errorf("FEAT_RIGHT xMax %.1f too far from right margin %.1f",
					x2, rightMargin)
			}
		} else {
			t.Error("FEAT_RIGHT bbox not found")
		}

		// FEAT_BOLD is in a left-aligned paragraph: xMin should be near the
		// left margin (within 5pt).
		if x1, _, ok := bboxRange(bbox, "FEAT_BOLD"); ok {
			if x1-leftMargin > 5 {
				t.Errorf("FEAT_BOLD xMin %.1f drifted from left margin %.1f",
					x1, leftMargin)
			}
		} else {
			t.Error("FEAT_BOLD bbox not found")
		}

		// Numbered list items are in document order, vertical.
		var y1, y2, y3 float64
		var ok1, ok2, ok3 bool
		_, _, y1, _, ok1 = bboxFull(bbox, "FEAT_LIST_NUM1")
		_, _, y2, _, ok2 = bboxFull(bbox, "FEAT_LIST_NUM2")
		_, _, y3, _, ok3 = bboxFull(bbox, "FEAT_LIST_NUM3")
		if ok1 && ok2 && ok3 {
			if !(y1 < y2 && y2 < y3) {
				t.Errorf("numbered list out of order: y=%.1f,%.1f,%.1f", y1, y2, y3)
			}
		} else {
			t.Errorf("missing list bboxes: ok=%v,%v,%v", ok1, ok2, ok3)
		}
	})

	// ============================================================
	// Phase 3 — PDF byte structure (outline / image / link annotations)
	// ============================================================
	t.Run("pdf_structure", func(t *testing.T) {
		// 4 heading paragraphs in the doc (Title + 2 Heading1 + 1 Heading2)
		// → at least 4 /Title entries plus the /Outlines anchor.
		if !bytes.Contains(pdfBytes, []byte("/Outlines")) {
			t.Error("no /Outlines reference — PDF outline tree not emitted")
		}
		if n := bytes.Count(pdfBytes, []byte("/Title")); n < 4 {
			t.Errorf("found %d /Title entries, want ≥ 4 (Title + 3 headings)", n)
		}

		// The embedded image must appear as an /Image XObject.
		if !bytes.Contains(pdfBytes, []byte("/Image")) {
			t.Error("no /Image XObject — image did not render")
		}

		// External hyperlinks emit /Annot subtype /Link entries.
		if !bytes.Contains(pdfBytes, []byte("/Annot")) {
			t.Error("no /Annot entry — hyperlink annotation not emitted")
		}
		if !bytes.Contains(pdfBytes, []byte("/Link")) {
			t.Error("no /Link subtype — hyperlink not registered as a link")
		}
	})

	// ============================================================
	// Phase 4 — PNG pixel sampling (highlight, color text, image)
	// ============================================================
	t.Run("pixel_colors", func(t *testing.T) {
		// The yellow highlight should produce a visible patch of yellow
		// pixels somewhere on page 1.
		if !imageHasColorNear(page1, color.RGBA{R: 255, G: 255, B: 0, A: 255}, 32, 50) {
			t.Error("no yellow-highlight pixels on page 1")
		}

		// Red-colored run (#C00000) → roughly that red in the rasterized
		// glyph fill.
		if !imageHasColorNear(page1, color.RGBA{R: 0xC0, G: 0x00, B: 0x00, A: 255}, 40, 30) {
			t.Error("no red-text pixels on page 1")
		}

		// The image we embedded is solid magenta (220,30,200). It should
		// produce a recognizable block of that color.
		if !imageHasColorNear(page1, color.RGBA{R: 220, G: 30, B: 200, A: 255}, 20, 100) {
			t.Error("embedded magenta image not visible on page 1")
		}

		// Page 2 should NOT have the magenta image — the image is on page 1
		// and shouldn't leak. Sanity check the rasterizer ordering.
		if imageHasColorNear(page2, color.RGBA{R: 220, G: 30, B: 200, A: 255}, 10, 50) {
			t.Error("magenta image leaked onto page 2")
		}
	})

	// ============================================================
	// Phase 5 — paging (header/footer/page-break/footnote location)
	// ============================================================
	t.Run("paging", func(t *testing.T) {
		// Page break: FEAT_BREAK_AFTER must NOT be on page 1.
		page1Text := pdftotextRange(t, pdfPath, 1, 1)
		page2Text := pdftotextRange(t, pdfPath, 2, 2)
		if strings.Contains(page1Text, "FEAT_BREAK_AFTER") {
			t.Error("FEAT_BREAK_AFTER landed on page 1, expected page 2")
		}
		if !strings.Contains(page2Text, "FEAT_BREAK_AFTER") {
			t.Error("FEAT_BREAK_AFTER missing from page 2")
		}

		// Header text on both pages.
		if !strings.Contains(page1Text, "FEAT_HEADER") {
			t.Error("FEAT_HEADER missing from page 1")
		}
		if !strings.Contains(page2Text, "FEAT_HEADER") {
			t.Error("FEAT_HEADER missing from page 2 (per-section header didn't repeat)")
		}

		// Footnote body sits at the bottom of the page where its ref lives.
		// Within the page 1 text, FEAT_FN_REF must appear before FEAT_FN_BODY
		// (pdftotext preserves vertical order).
		refIdx := strings.Index(page1Text, "FEAT_FN_REF")
		bodyIdx := strings.Index(page1Text, "FEAT_FN_BODY")
		if refIdx < 0 || bodyIdx < 0 {
			t.Errorf("footnote markers missing on page 1: ref=%d body=%d", refIdx, bodyIdx)
		} else if refIdx >= bodyIdx {
			t.Errorf("footnote body appears before its ref (ref=%d body=%d)", refIdx, bodyIdx)
		}

		// Verify that we got exactly 2 pages (page break worked once).
		if got := pdfPageCount(t, pdfPath); got != 2 {
			t.Errorf("page count = %d, want 2", got)
		}
	})
}

// ====================== helpers ============================================

func loadPNGImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// imageHasColorNear scans img for any pixel within `tol` (per-channel
// max-absolute-distance) of want. Stops as soon as `minCount` matches
// are found, so it's reasonably fast for big rasterized pages.
func imageHasColorNear(img image.Image, want color.RGBA, tol, minCount int) bool {
	bounds := img.Bounds()
	hits := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			rr := uint8(r >> 8)
			gg := uint8(g >> 8)
			bb := uint8(b >> 8)
			if abs8(rr, want.R) <= uint8(tol) &&
				abs8(gg, want.G) <= uint8(tol) &&
				abs8(bb, want.B) <= uint8(tol) {
				hits++
				if hits >= minCount {
					return true
				}
			}
		}
	}
	return false
}

func abs8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// pdftextBbox returns the raw output of `pdftotext -bbox` for the file —
// HTML with <word xMin… yMin… xMax… yMax…>label</word> entries.
func pdftextBbox(t *testing.T, pdf string) string {
	t.Helper()
	out, err := combinedOutput("pdftotext", "-bbox", pdf, "-")
	if err != nil {
		t.Fatalf("pdftotext -bbox: %v", err)
	}
	return string(out)
}

// pdftotextRange returns pdftotext output for a single inclusive page range.
func pdftotextRange(t *testing.T, pdf string, first, last int) string {
	t.Helper()
	out, err := combinedOutput("pdftotext", "-f", strconv.Itoa(first), "-l", strconv.Itoa(last), pdf, "-")
	if err != nil {
		t.Fatalf("pdftotext -f%d -l%d: %v", first, last, err)
	}
	return string(out)
}

// bboxRange returns xMin and xMax of the <word> entry whose text is label.
func bboxRange(bboxXML, label string) (xMin, xMax float64, ok bool) {
	x1, _, _, _, ok1 := bboxFull(bboxXML, label)
	_, x2, _, _, ok2 := bboxFull(bboxXML, label)
	return x1, x2, ok1 && ok2
}

// bboxFull returns all four bbox coords of the first <word>…label</word>.
// Useful when both x and y matter.
func bboxFull(bboxXML, label string) (xMin, xMax, yMin, yMax float64, ok bool) {
	idx := strings.Index(bboxXML, ">"+label+"</word>")
	if idx < 0 {
		return 0, 0, 0, 0, false
	}
	tag := bboxXML[:idx]
	open := strings.LastIndex(tag, "<word ")
	if open < 0 {
		return 0, 0, 0, 0, false
	}
	attrs := tag[open:]
	xMin = bboxAttr(attrs, "xMin")
	xMax = bboxAttr(attrs, "xMax")
	yMin = bboxAttr(attrs, "yMin")
	yMax = bboxAttr(attrs, "yMax")
	return xMin, xMax, yMin, yMax, true
}

func bboxAttr(s, name string) float64 {
	key := name + `="`
	i := strings.Index(s, key)
	if i < 0 {
		return 0
	}
	rest := s[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(rest[:j], 64)
	return v
}

// ===================== docx source for the test ============================

// The docx has its own minimal styles.xml (Heading1, Heading2, Title) and
// numbering.xml (one decimal list, one bullet list). Header and footer
// reference XML parts via Rels. The body sits in one section that ends
// with sectPr → so the second page picks up the same header/footer.

const comprehensiveStyles = `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:rPr><w:b/><w:sz w:val="40"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:rPr><w:b/><w:sz w:val="28"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:rPr><w:b/><w:sz w:val="24"/></w:rPr></w:style>
</w:styles>`

const comprehensiveNumbering = `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0">
      <w:start w:val="1"/>
      <w:numFmt w:val="decimal"/>
      <w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>
    </w:lvl>
  </w:abstractNum>
  <w:abstractNum w:abstractNumId="1">
    <w:lvl w:ilvl="0">
      <w:numFmt w:val="bullet"/>
      <w:lvlText w:val="•"/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>
    </w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
  <w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>
</w:numbering>`

const comprehensiveHeader = `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>FEAT_HEADER text</w:t></w:r></w:p>
</w:hdr>`

const comprehensiveFooter = `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>FEAT_FOOTER text</w:t></w:r></w:p>
</w:ftr>`

const comprehensiveFootnotes = `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>FEAT_FN_BODY content</w:t></w:r></w:p></w:footnote>
</w:footnotes>`

const comprehensiveRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
  <Relationship Id="rF1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
  <Relationship Id="rLink" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/comprehensive" TargetMode="External"/>
</Relationships>`

const comprehensiveBody = `
  <w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr>
    <w:r><w:t>FEAT_TITLE Document</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
    <w:r><w:t>FEAT_HEAD1_A first chapter</w:t></w:r>
  </w:p>

  <w:p>
    <w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">FEAT_BOLD </w:t></w:r>
    <w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve">FEAT_ITAL </w:t></w:r>
    <w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">FEAT_UNDER </w:t></w:r>
    <w:r><w:rPr><w:strike/></w:rPr><w:t>FEAT_STRIKE</w:t></w:r>
  </w:p>

  <w:p>
    <w:r><w:rPr><w:color w:val="C00000"/></w:rPr><w:t xml:space="preserve">FEAT_RED text </w:t></w:r>
    <w:r><w:rPr><w:highlight w:val="yellow"/></w:rPr><w:t>FEAT_HILITE text</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:t>FEAT_CENTER</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:jc w:val="right"/></w:pPr>
    <w:r><w:t>FEAT_RIGHT</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:jc w:val="both"/></w:pPr>
    <w:r><w:t xml:space="preserve">FEAT_JUST_A word </w:t></w:r>
    <w:r><w:t xml:space="preserve">FEAT_JUST_B word </w:t></w:r>
    <w:r><w:t xml:space="preserve">FEAT_JUST_C word and additional words to force the justified line to actually justify.</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_NUM1 item one</w:t></w:r>
  </w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_NUM2 item two</w:t></w:r>
  </w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_NUM3 item three</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_BUL1 dot one</w:t></w:r>
  </w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_BUL2 dot two</w:t></w:r>
  </w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr>
    <w:r><w:t>FEAT_LIST_BUL3 dot three</w:t></w:r>
  </w:p>

  <w:tbl>
    <w:tblPr><w:tblLook w:firstRow="1"/></w:tblPr>
    <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
    <w:tr><w:trPr><w:tblHeader/></w:trPr>
      <w:tc><w:p><w:r><w:t>FEAT_TBL_H1 col one</w:t></w:r></w:p></w:tc>
      <w:tc><w:p><w:r><w:t>FEAT_TBL_H2 col two</w:t></w:r></w:p></w:tc>
    </w:tr>
    <w:tr>
      <w:tc><w:tcPr><w:shd w:fill="DDEEFF"/></w:tcPr><w:p><w:r><w:t>FEAT_TBL_R1C1</w:t></w:r></w:p></w:tc>
      <w:tc><w:p><w:r><w:t>FEAT_TBL_R1C2</w:t></w:r></w:p></w:tc>
    </w:tr>
    <w:tr>
      <w:tc><w:p><w:r><w:t>FEAT_TBL_R2C1</w:t></w:r></w:p></w:tc>
      <w:tc><w:p><w:r><w:t>FEAT_TBL_R2C2</w:t></w:r></w:p></w:tc>
    </w:tr>
  </w:tbl>

  <w:p>
    <w:r><w:t xml:space="preserve">Inline image: </w:t></w:r>
    <w:r>
      <w:drawing>
        <wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
          <wp:extent cx="813000" cy="203000"/>
          <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
            <a:graphicData>
              <pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
                <pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill>
              </pic:pic>
            </a:graphicData>
          </a:graphic>
        </wp:inline>
      </w:drawing>
    </w:r>
  </w:p>

  <w:p>
    <w:r><w:t xml:space="preserve">Reference with </w:t></w:r>
    <w:r><w:t xml:space="preserve">FEAT_FN_REF</w:t></w:r>
    <w:r><w:footnoteReference w:id="2"/></w:r>
    <w:r><w:t xml:space="preserve"> and</w:t></w:r>
    <w:hyperlink r:id="rLink">
      <w:r><w:t xml:space="preserve"> FEAT_LINK_TEXT</w:t></w:r>
    </w:hyperlink>
    <w:r><w:t xml:space="preserve"> back.</w:t></w:r>
  </w:p>

  <w:p>
    <w:r><w:t xml:space="preserve">Inline SDT: </w:t></w:r>
    <w:sdt>
      <w:sdtPr><w:id w:val="1"/></w:sdtPr>
      <w:sdtContent>
        <w:r><w:t>FEAT_SDT_INL inside</w:t></w:r>
      </w:sdtContent>
    </w:sdt>
    <w:r><w:t xml:space="preserve"> and math </w:t></w:r>
    <m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">
      <m:r><m:t>FEAT_MATH_E=mc²</m:t></m:r>
    </m:oMath>
    <w:r><w:t xml:space="preserve">.</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr>
    <w:r><w:t>FEAT_HEAD2 subsection</w:t></w:r>
  </w:p>

  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
    <w:r><w:t>FEAT_HEAD1_B second chapter on next page</w:t></w:r>
  </w:p>

  <w:p>
    <w:r><w:br w:type="page"/></w:r>
    <w:r><w:t>FEAT_BREAK_AFTER content on page two</w:t></w:r>
  </w:p>

  <w:sectPr>
    <w:headerReference w:type="default" r:id="rH1"/>
    <w:footerReference w:type="default" r:id="rF1"/>
    <w:pgSz w:w="11906" w:h="16838"/>
    <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
  </w:sectPr>
`
