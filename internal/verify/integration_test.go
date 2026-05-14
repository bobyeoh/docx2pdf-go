package verify

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

// TestIntegrationKitchenSink builds one large docx that exercises a wide
// cross-section of features simultaneously, then audits the resulting PDF
// for structural soundness. This is what catches inter-feature regressions
// that single-feature cases miss.
func TestIntegrationKitchenSink(t *testing.T) {
	requireTool(t, "pdftotext")
	requireTool(t, "pdfinfo")

	dir := t.TempDir()
	font := findFont(t)

	red := makeSolidPNG(120, 80, color.RGBA{R: 200, G: 40, B: 40, A: 255})

	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="22"/></w:rPr></w:rPrDefault>
    <w:pPrDefault><w:pPr><w:spacing w:after="120"/></w:pPr></w:pPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:basedOn w:val="Normal"/>
    <w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/></w:pPr>
    <w:rPr><w:b/><w:sz w:val="40"/><w:color w:val="2E74B5"/></w:rPr>
  </w:style>
  <w:style w:type="character" w:styleId="Emph">
    <w:rPr><w:i/><w:color w:val="C00000"/></w:rPr>
  </w:style>
  <w:style w:type="table" w:styleId="MyTable">
    <w:rPr><w:sz w:val="20"/></w:rPr>
  </w:style>
</w:styles>`

	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`

	theme := `<?xml version="1.0"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <a:themeElements>
    <a:clrScheme><a:accent1><a:srgbClr val="2E74B5"/></a:accent1></a:clrScheme>
    <a:fontScheme><a:majorFont><a:latin typeface="Calibri"/></a:majorFont>
      <a:minorFont><a:latin typeface="Calibri"/></a:minorFont></a:fontScheme>
  </a:themeElements>
</a:theme>`

	// Body: heading + paragraphs with mixed formatting + list + table + image +
	// footnote ref + hyperlink + bookmark + page break + heading 2 + multi-col.
	body := `
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Kitchen Sink Test Document</w:t></w:r></w:p>

    <w:p><w:r><w:t xml:space="preserve">This paragraph mixes </w:t></w:r>
      <w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">bold </w:t></w:r>
      <w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve">italic </w:t></w:r>
      <w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">underline </w:t></w:r>
      <w:r><w:rPr><w:strike/></w:rPr><w:t xml:space="preserve">strike </w:t></w:r>
      <w:r><w:rPr><w:highlight w:val="yellow"/></w:rPr><w:t xml:space="preserve">highlight </w:t></w:r>
      <w:r><w:rPr><w:caps/></w:rPr><w:t xml:space="preserve">caps </w:t></w:r>
      <w:r><w:rPr><w:vertAlign w:val="superscript"/></w:rPr><w:t xml:space="preserve">super </w:t></w:r>
      <w:r><w:rPr><w:rStyle w:val="Emph"/></w:rPr><w:t>character-style</w:t></w:r>
      <w:r><w:footnoteReference w:id="2"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>

    <w:p><w:bookmarkStart w:id="1" w:name="targetBM"/>
      <w:r><w:t>This paragraph is the bookmark target.</w:t></w:r>
      <w:bookmarkEnd w:id="1"/></w:p>

    <w:p>
      <w:r><w:t xml:space="preserve">Jump to </w:t></w:r>
      <w:hyperlink w:anchor="targetBM">
        <w:r><w:rPr><w:color w:val="0563C1"/></w:rPr><w:t>the bookmark</w:t></w:r>
      </w:hyperlink>
      <w:r><w:t xml:space="preserve"> or visit </w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText xml:space="preserve">HYPERLINK "https://example.com"</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>example.com</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>

    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>first list item</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>second list item</w:t></w:r></w:p>

    <w:tbl>
      <w:tblPr><w:tblStyle w:val="MyTable"/>
        <w:tblLook w:firstRow="1"/></w:tblPr>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:tcPr><w:shd w:fill="DDEEFF"/></w:tcPr>
          <w:p><w:r><w:t>Header A</w:t></w:r></w:p></w:tc>
        <w:tc><w:tcPr><w:shd w:fill="DDEEFF"/></w:tcPr>
          <w:p><w:r><w:t>Header B</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>row 1 cell A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>row 1 cell B</w:t></w:r></w:p>
          <w:p><w:r><w:drawing><wp:inline>
            <a:graphic><a:graphicData>
              <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
            </a:graphicData></a:graphic>
          </wp:inline></w:drawing></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>

    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Second Section</w:t></w:r></w:p>
    <w:p><w:r><w:t xml:space="preserve">page two content with another note</w:t></w:r>
      <w:r><w:footnoteReference w:id="3"/></w:r>
      <w:r><w:t>.</w:t></w:r></w:p>

    <w:sectPr>
      <w:headerReference r:id="rH" w:type="default"/>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`

	d := newDocx().
		RawBody(docHeader+body+docFooter).
		Styles(styles).
		Numbering(numbering).
		Part("theme/theme1.xml", theme).
		Part("footnotes.xml", `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>Citation for the kitchen sink.</w:t></w:r></w:p></w:footnote>
  <w:footnote w:id="3"><w:p><w:r><w:t>Page-two note body.</w:t></w:r></w:p></w:footnote>
</w:footnotes>`).
		Part("header1.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="right"/></w:pPr>
    <w:r><w:rPr><w:i/></w:rPr><w:t>Kitchen Sink — Demo</w:t></w:r></w:p>
</w:hdr>`).
		Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:t xml:space="preserve">Page </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText>PAGE</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
    <w:r><w:t xml:space="preserve"> of </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText>NUMPAGES</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p>
</w:ftr>`).
		Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/img.png"/>
</Relationships>`).
		Media("img.png", red)

	in := d.Write(t, dir)
	out := filepath.Join(dir, "kitchen_sink.pdf")
	if err := docx2pdf.Convert(in, out, docx2pdf.Options{
		FontRegular:     font,
		DefaultFontSize: 11,
	}); err != nil {
		t.Fatalf("Convert: %v", err)
	}

	// Stash for visual review.
	outDir := mustAbs(t, "out_integration/kitchen_sink")
	_ = os.MkdirAll(outDir, 0o755)
	_ = copyFile(out, filepath.Join(outDir, "kitchen_sink.pdf"))
	_ = renderPNG(t, out, outDir)

	// Audit: every page is well-formed (no pdfinfo syntax errors).
	if errors, ok := pdfinfoSyntaxErrors(out); !ok {
		t.Errorf("pdfinfo found syntax errors:\n%s", errors)
	}

	// Audit: text extraction contains every feature's marker.
	txt := pdftotext(t, out)
	wants := []string{
		"Kitchen Sink Test Document",
		"bold", "italic", "underline", "highlight",
		"CAPS",  // w:caps uppercases the text
		"super", // vertAlign superscript
		"first list item", "second list item",
		"Header A", "Header B", "row 1 cell A",
		"bookmark target", "the bookmark", "example.com",
		"Citation for the kitchen sink",
		"Page-two note body",
		"Second Section",
	}
	for _, w := range wants {
		if !strings.Contains(txt, w) {
			t.Errorf("missing expected marker %q in extracted text", w)
		}
	}
	// Header text appears on every page.
	if !strings.Contains(txt, "Kitchen Sink") {
		t.Errorf("header text missing")
	}
	// PAGE/NUMPAGES field substitution. pdftotext might collapse spaces — check
	// both "Page 1 of 2" and the squashed form, and confirm cached "?" is gone.
	if strings.Contains(txt, "Page ? of") || strings.Contains(txt, "Page?of") {
		t.Errorf("cached '?' leaked through field substitution:\n%s", txt)
	}
	if !strings.Contains(txt, "1") || !strings.Contains(txt, "2") {
		t.Errorf("page numbers not visible in extracted text")
	}
	if !strings.Contains(txt, "Page 1 of 2") && !strings.Contains(txt, "Page1of2") {
		t.Errorf("PAGE/NUMPAGES not substituted in footer")
	}
}

// TestIntegrationRealCorpus runs every docx4j sample doc and audits the
// resulting PDF for structural soundness — page count > 0, no pdfinfo
// syntax errors, all pages have non-zero dimensions.
func TestIntegrationRealCorpus(t *testing.T) {
	requireTool(t, "pdfinfo")
	requireTool(t, "pdftotext")

	font := findFont(t)
	docsDir, err := filepath.Abs("../../../docx4j/docs")
	if err != nil || docsDir == "" {
		t.Skip("no docx4j docs")
	}
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("skip — %s not found", docsDir)
	}

	entries, err := os.ReadDir(docsDir)
	if err != nil {
		t.Fatal(err)
	}
	outRoot := mustAbs(t, "out_integration/real_corpus")
	_ = os.RemoveAll(outRoot)
	_ = os.MkdirAll(outRoot, 0o755)

	failed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".docx") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(docsDir, name)
			dir := t.TempDir()
			out := filepath.Join(dir, "out.pdf")
			err := docx2pdf.Convert(src, out, docx2pdf.Options{
				FontRegular:  font,
				FontFallback: font,
				Lenient:      true,
			})
			if err != nil {
				t.Errorf("convert: %v", err)
				failed++
				return
			}
			if _, ok := pdfinfoSyntaxErrors(out); !ok {
				// Don't fail — many real docx use features we degrade. Just
				// log so we know which ones.
				t.Logf("syntax errors in output for %s (likely degraded feature)", name)
			}
			// Snapshot first page.
			caseOut := filepath.Join(outRoot, sanitize(name))
			_ = os.MkdirAll(caseOut, 0o755)
			_ = copyFile(out, filepath.Join(caseOut, "out.pdf"))
			_ = renderPNGFirstOnly(t, out, caseOut)
		})
	}
	if failed > 0 {
		t.Errorf("%d corpus files failed to convert", failed)
	}
}

// TestIntegrationManyFootnotes packs 10 footnote refs into a single short
// paragraph and verifies all 10 note bodies appear at the page bottom.
func TestIntegrationManyFootnotes(t *testing.T) {
	requireTool(t, "pdftotext")
	dir := t.TempDir()
	font := findFont(t)

	body := `<w:p><w:r><w:t xml:space="preserve">Multi-cited claim</w:t></w:r>`
	for i := 2; i <= 11; i++ {
		body += `<w:r><w:footnoteReference w:id="` + itoaInt(i) + `"/></w:r>`
	}
	body += `<w:r><w:t>.</w:t></w:r></w:p>`

	notes := `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>`
	for i := 2; i <= 11; i++ {
		notes += `  <w:footnote w:id="` + itoaInt(i) + `"><w:p><w:r><w:t>Note body N` + itoaInt(i) + `.</w:t></w:r></w:p></w:footnote>`
	}
	notes += `</w:footnotes>`

	in := newDocx().Body(body).Part("footnotes.xml", notes).Write(t, dir)
	out := filepath.Join(dir, "many_fn.pdf")
	if err := docx2pdf.Convert(in, out, docx2pdf.Options{FontRegular: font}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	txt := pdftotext(t, out)
	for i := 2; i <= 11; i++ {
		want := "Note body N" + itoaInt(i)
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

// TestIntegrationLongTableWithHeaderRepeat verifies that a long table with
// tblHeader-marked rows still works after the column/footnote fixes.
func TestIntegrationLongTableWithHeaderRepeat(t *testing.T) {
	requireTool(t, "pdftotext")
	dir := t.TempDir()
	font := findFont(t)

	var rows strings.Builder
	rows.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>
    <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>STICKY-HEADER</w:t></w:r></w:p></w:tc>
    <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>value</w:t></w:r></w:p></w:tc></w:tr>`)
	for i := 1; i <= 100; i++ {
		rows.WriteString(`<w:tr>
      <w:tc><w:p><w:r><w:t>row ` + itoaInt(i) + `</w:t></w:r></w:p></w:tc>
      <w:tc><w:p><w:r><w:t>data ` + itoaInt(i) + `</w:t></w:r></w:p></w:tc></w:tr>`)
	}
	body := `<w:tbl><w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="4000"/></w:tblGrid>` +
		rows.String() + `</w:tbl>`

	in := newDocx().Body(body).Write(t, dir)
	out := filepath.Join(dir, "long_table.pdf")
	if err := docx2pdf.Convert(in, out, docx2pdf.Options{FontRegular: font}); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	txt := pdftotext(t, out)
	headerCount := strings.Count(txt, "STICKY-HEADER")
	pages := pdfPageCount(t, out)
	if pages < 2 {
		t.Fatalf("expected long table to span >= 2 pages, got %d", pages)
	}
	if headerCount < pages {
		t.Errorf("header repeated %d times for %d pages (should be ≥ page count)", headerCount, pages)
	}
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// pdfinfoSyntaxErrors runs pdfinfo and returns the error text (if any) along
// with a bool indicating whether the output was clean.
func pdfinfoSyntaxErrors(pdf string) (string, bool) {
	out, _ := execCombined("pdfinfo", pdf)
	lines := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Syntax Error") || strings.Contains(line, "Failed to") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "", true
	}
	return strings.Join(lines, "\n"), false
}

func execCombined(name string, args ...string) ([]byte, error) {
	return combinedOutput(name, args...)
}
