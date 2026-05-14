package docx

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// buildDocx writes a minimal docx zip containing the given document.xml body,
// numbering.xml, and rels. Returns the file path of the temp file.
func buildDocx(t *testing.T, documentXML, numberingXML, relsXML string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("word/document.xml", documentXML)
	if numberingXML != "" {
		write("word/numbering.xml", numberingXML)
	}
	if relsXML != "" {
		write("word/_rels/document.xml.rels", relsXML)
	}
	return path
}

func TestParseBasicParagraph(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:jc w:val="center"/></w:pPr>
      <w:r>
        <w:rPr><w:b/><w:i/><w:sz w:val="28"/><w:color w:val="FF0000"/></w:rPr>
        <w:t>Hello world</w:t>
      </w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocx(t, docXML, "", "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(doc.Body) != 1 {
		t.Fatalf("blocks: got %d want 1", len(doc.Body))
	}
	p, ok := doc.Body[0].(Paragraph)
	if !ok {
		t.Fatalf("block 0 is not Paragraph: %T", doc.Body[0])
	}
	if p.Alignment != AlignCenter {
		t.Errorf("alignment: got %v want center", p.Alignment)
	}
	if len(p.Runs) != 1 {
		t.Fatalf("runs: got %d want 1", len(p.Runs))
	}
	r := p.Runs[0]
	if r.Text != "Hello world" {
		t.Errorf("text: got %q", r.Text)
	}
	if !r.Props.Bold || !r.Props.Italic {
		t.Errorf("props: bold=%v italic=%v want true,true", r.Props.Bold, r.Props.Italic)
	}
	if r.Props.FontSize != 14 {
		t.Errorf("size: got %v want 14 (28 half-points)", r.Props.FontSize)
	}
	if r.Props.Color != "FF0000" {
		t.Errorf("color: got %q", r.Props.Color)
	}
}

func TestParseListWithNumbering(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>first</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>second</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`
	numXML := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0">
      <w:start w:val="1"/>
      <w:numFmt w:val="decimal"/>
      <w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>
    </w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	path := buildDocx(t, docXML, numXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(doc.Body) != 2 {
		t.Fatalf("blocks: got %d want 2", len(doc.Body))
	}
	for i, b := range doc.Body {
		p := b.(Paragraph)
		if p.List == nil {
			t.Fatalf("paragraph %d: List is nil", i)
		}
		if p.List.NumID != 1 || p.List.Level != 0 {
			t.Errorf("paragraph %d: list info = %+v", i, p.List)
		}
	}
	lv, ok := doc.Numbering.Abstract[0].Levels[0]
	if !ok {
		t.Fatal("abstract level missing")
	}
	if lv.Format != "decimal" || lv.Text != "%1." || lv.LeftTwips != 720 || lv.HangingTwips != 360 {
		t.Errorf("level def = %+v", lv)
	}
	if doc.Numbering.NumToAbs[1] != 0 {
		t.Errorf("numToAbs[1] = %d want 0", doc.Numbering.NumToAbs[1])
	}
}

func TestParseHyperlinkRels(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p>
      <w:hyperlink r:id="rIdLink">
        <w:r><w:t>click me</w:t></w:r>
      </w:hyperlink>
    </w:p>
  </w:body>
</w:document>`
	relsXML := `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdLink"
      Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://example.com" TargetMode="External"/>
</Relationships>`
	path := buildDocx(t, docXML, "", relsXML)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := doc.Hyperlink["rIdLink"]; got != "https://example.com" {
		t.Errorf("hyperlink target: got %q", got)
	}
	p := doc.Body[0].(Paragraph)
	if len(p.Runs) != 1 || p.Runs[0].LinkURL != "rIdLink" {
		t.Errorf("hyperlink run not tagged: %+v", p.Runs)
	}
}

// formatLevelText lives in render but we can sanity-check the public surface
// of NumLevel here without pulling render in: a few placeholder rules.
func TestNumLevelStartDefault(t *testing.T) {
	lv := NumLevel{Start: 1, Format: "decimal", Text: "%1."}
	if lv.Start != 1 {
		t.Errorf("unexpected default: %+v", lv)
	}
	// Compile-time check that placeholder bytes look right.
	if !bytes.Contains([]byte(lv.Text), []byte("%1")) {
		t.Error("placeholder missing")
	}
}

// buildDocxWithStyles is like buildDocx but also writes styles.xml.
func buildDocxWithStyles(t *testing.T, documentXML, stylesXML string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("word/document.xml", documentXML)
	if stylesXML != "" {
		write("word/styles.xml", stylesXML)
	}
	return path
}

func TestParseStylesInheritance(t *testing.T) {
	stylesXML := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:styleId="Normal">
    <w:rPr><w:sz w:val="22"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:basedOn w:val="Normal"/>
    <w:pPr><w:jc w:val="center"/><w:spacing w:before="240" w:after="120"/></w:pPr>
    <w:rPr><w:b/><w:sz w:val="40"/><w:color w:val="2E74B5"/></w:rPr>
  </w:style>
</w:styles>`
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Big heading</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, stylesXML)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st, ok := doc.Styles["Heading1"]
	if !ok {
		t.Fatal("Heading1 style not loaded")
	}
	if !st.Run.Bold || st.Run.FontSize != 20 || st.Run.Color != "2E74B5" {
		t.Errorf("Heading1 run props after merge: %+v", st.Run)
	}
	if st.Alignment != AlignCenter || !st.HasAlignment {
		t.Errorf("Heading1 alignment: got %v has=%v", st.Alignment, st.HasAlignment)
	}
	p := doc.Body[0].(Paragraph)
	if p.Alignment != AlignCenter {
		t.Errorf("paragraph alignment: got %v want center", p.Alignment)
	}
	if p.SpacingBefore != 12 || p.SpacingAfter != 6 {
		t.Errorf("spacing: before=%v after=%v", p.SpacingBefore, p.SpacingAfter)
	}
	if !p.Runs[0].Props.Bold || p.Runs[0].Props.FontSize != 20 {
		t.Errorf("run props inherited: %+v", p.Runs[0].Props)
	}
}

func TestParseHeaderFooterParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("word/document.xml", `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p><w:r><w:t>body</w:t></w:r></w:p>
    <w:sectPr>
      <w:headerReference r:id="rH" w:type="default"/>
      <w:footerReference r:id="rF" w:type="default"/>
    </w:sectPr>
  </w:body>
</w:document>`)
	write("word/header1.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>my header</w:t></w:r></w:p>
</w:hdr>`)
	write("word/footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>my footer</w:t></w:r></w:p>
</w:ftr>`)
	write("word/_rels/document.xml.rels", `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH"
      Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header"
      Target="header1.xml"/>
  <Relationship Id="rF"
      Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer"
      Target="footer1.xml"/>
</Relationships>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(doc.HeaderBlocks) != 1 {
		t.Fatalf("HeaderBlocks: got %d want 1", len(doc.HeaderBlocks))
	}
	if got := doc.HeaderBlocks[0].(Paragraph).Runs[0].Text; got != "my header" {
		t.Errorf("header text: got %q", got)
	}
	if len(doc.FooterBlocks) != 1 {
		t.Fatalf("FooterBlocks: got %d want 1", len(doc.FooterBlocks))
	}
	if got := doc.FooterBlocks[0].(Paragraph).Runs[0].Text; got != "my footer" {
		t.Errorf("footer text: got %q", got)
	}
}

func TestParseMultiSection(t *testing.T) {
	// Two sections: portrait then landscape, separated by an inline sectPr
	// on the second paragraph.
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>first section page one</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:sectPr>
          <w:pgSz w:w="11906" w:h="16838"/>
          <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
        </w:sectPr>
      </w:pPr>
      <w:r><w:t>last paragraph of section 1</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>section 2 body</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/>
      <w:pgMar w:top="720" w:right="720" w:bottom="720" w:left="720"/>
    </w:sectPr>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("Sections: got %d want 2", len(doc.Sections))
	}
	s1 := doc.Sections[0]
	if s1.PageSize.WidthTwips != 11906 || s1.PageSize.HeightTwips != 16838 {
		t.Errorf("s1 page size: %+v", s1.PageSize)
	}
	if len(s1.Blocks) != 2 {
		t.Errorf("s1 blocks: %d want 2", len(s1.Blocks))
	}
	s2 := doc.Sections[1]
	if s2.PageSize.WidthTwips != 16838 || s2.PageSize.HeightTwips != 11906 {
		t.Errorf("s2 page size: %+v", s2.PageSize)
	}
	if s2.Margins.Top != 720 {
		t.Errorf("s2 margins.top: %d want 720", s2.Margins.Top)
	}
	if len(s2.Blocks) != 1 {
		t.Errorf("s2 blocks: %d want 1", len(s2.Blocks))
	}
	// doc.Body still aggregates everything for backward compat.
	if len(doc.Body) != 3 {
		t.Errorf("doc.Body: %d want 3", len(doc.Body))
	}
}

func TestParseAnchoredImage(t *testing.T) {
	// wp:anchor (floating) should be picked up like wp:inline by findBlipEmbed.
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:anchor>
            <a:graphic>
              <a:graphicData>
                <pic:pic>
                  <pic:blipFill>
                    <a:blip r:embed="rImg1"/>
                  </pic:blipFill>
                </pic:pic>
              </a:graphicData>
            </a:graphic>
          </wp:anchor>
        </w:drawing>
      </w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := doc.Body[0].(Paragraph)
	if len(p.Runs) == 0 || p.Runs[0].ImageID != "rImg1" {
		t.Fatalf("anchored image not picked up; runs = %+v", p.Runs)
	}
}

func TestParseLineSpacing(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:spacing w:line="360" w:lineRule="auto"/></w:pPr>
      <w:r><w:t>auto 1.5x</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:spacing w:line="300" w:lineRule="exact"/></w:pPr>
      <w:r><w:t>exact 15pt</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:spacing w:line="240"/></w:pPr>
      <w:r><w:t>auto default</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p1 := doc.Body[0].(Paragraph)
	if p1.LineHeight.Rule != "auto" || p1.LineHeight.Mul != 1.5 {
		t.Errorf("p1 LineHeight = %+v want {auto 1.5}", p1.LineHeight)
	}
	p2 := doc.Body[1].(Paragraph)
	if p2.LineHeight.Rule != "exact" || p2.LineHeight.Pt != 15 {
		t.Errorf("p2 LineHeight = %+v want {exact 15pt}", p2.LineHeight)
	}
	p3 := doc.Body[2].(Paragraph)
	// Missing w:lineRule defaults to "auto" in Word.
	if p3.LineHeight.Rule != "auto" || p3.LineHeight.Mul != 1.0 {
		t.Errorf("p3 LineHeight = %+v want {auto 1.0}", p3.LineHeight)
	}
}

func TestParseIndentAndStrike(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:ind w:left="720" w:firstLine="360"/></w:pPr>
      <w:r><w:rPr><w:strike/></w:rPr><w:t>cancelled</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:ind w:left="720" w:hanging="200"/></w:pPr>
      <w:r><w:t>hang</w:t></w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p1 := doc.Body[0].(Paragraph)
	if p1.IndentLeftPt != 36 { // 720 twips = 36 pt
		t.Errorf("p1 IndentLeft: got %v want 36", p1.IndentLeftPt)
	}
	if p1.IndentFirstLinePt != 18 { // 360 twips = 18 pt
		t.Errorf("p1 IndentFirstLine: got %v want 18", p1.IndentFirstLinePt)
	}
	if !p1.Runs[0].Props.Strike {
		t.Errorf("p1 strike not set: %+v", p1.Runs[0].Props)
	}
	p2 := doc.Body[1].(Paragraph)
	if p2.IndentFirstLinePt != -10 { // hanging 200 twips → -10 pt
		t.Errorf("p2 hanging: got %v want -10", p2.IndentFirstLinePt)
	}
}

func TestParseFieldMarkers(t *testing.T) {
	// A PAGE field: begin → instrText("PAGE") → separate → t("1") → end
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText xml:space="preserve">PAGE</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>1</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
    </w:p>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := doc.Body[0].(Paragraph)
	if len(p.Runs) != 5 {
		t.Fatalf("expected 5 raw runs (begin, instr, sep, t, end); got %d", len(p.Runs))
	}
	if !p.Runs[0].FieldBegin {
		t.Errorf("Runs[0]: %+v want FieldBegin", p.Runs[0])
	}
	if p.Runs[1].InstrText != "PAGE" {
		t.Errorf("Runs[1] InstrText = %q want PAGE", p.Runs[1].InstrText)
	}
	if !p.Runs[2].FieldSep {
		t.Errorf("Runs[2]: %+v want FieldSep", p.Runs[2])
	}
	if p.Runs[3].Text != "1" {
		t.Errorf("Runs[3] Text = %q want \"1\"", p.Runs[3].Text)
	}
	if !p.Runs[4].FieldEnd {
		t.Errorf("Runs[4]: %+v want FieldEnd", p.Runs[4])
	}
}

func TestParseGridSpanAndVMerge(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblGrid><w:gridCol w:w="1000"/><w:gridCol w:w="1000"/><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:gridSpan w:val="2"/><w:vMerge w:val="restart"/></w:tcPr>
          <w:p><w:r><w:t>spanning</w:t></w:r></w:p>
        </w:tc>
        <w:tc><w:p><w:r><w:t>third</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc>
          <w:tcPr><w:gridSpan w:val="2"/><w:vMerge/></w:tcPr>
          <w:p/>
        </w:tc>
        <w:tc><w:p><w:r><w:t>row2</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	r0c0 := tbl.Rows[0].Cells[0]
	if r0c0.GridSpan != 2 {
		t.Errorf("row0 cell0 gridSpan = %d want 2", r0c0.GridSpan)
	}
	if r0c0.VMerge != "restart" {
		t.Errorf("row0 cell0 vMerge = %q want restart", r0c0.VMerge)
	}
	r1c0 := tbl.Rows[1].Cells[0]
	if r1c0.VMerge != "continue" {
		t.Errorf("row1 cell0 vMerge = %q want continue (implicit)", r1c0.VMerge)
	}
}

func TestTblBordersPropagateIntoCells(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblPr>
        <w:tblBorders>
          <w:top w:val="single" w:sz="8" w:color="auto"/>
          <w:bottom w:val="single" w:sz="8" w:color="auto"/>
          <w:left w:val="single" w:sz="8" w:color="auto"/>
          <w:right w:val="single" w:sz="8" w:color="auto"/>
          <w:insideH w:val="double" w:sz="12" w:color="FF0000"/>
          <w:insideV w:val="dashed" w:sz="6" w:color="00FF00"/>
        </w:tblBorders>
      </w:tblPr>
      <w:tblGrid><w:gridCol w:w="1000"/><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>C</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>D</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	// Top-left cell: outer top, outer left; bottom/right are interior.
	a := tbl.Rows[0].Cells[0].Borders
	if a.Top.Style != "single" {
		t.Errorf("A.top.style=%q want single (outer)", a.Top.Style)
	}
	if a.Left.Style != "single" {
		t.Errorf("A.left.style=%q want single (outer)", a.Left.Style)
	}
	if a.Bottom.Style != "double" {
		t.Errorf("A.bottom.style=%q want double (insideH)", a.Bottom.Style)
	}
	if a.Right.Style != "dashed" {
		t.Errorf("A.right.style=%q want dashed (insideV)", a.Right.Style)
	}
	// Bottom-right cell: bottom & right are outer, top & left are interior.
	d := tbl.Rows[1].Cells[1].Borders
	if d.Top.Style != "double" {
		t.Errorf("D.top.style=%q want double (insideH)", d.Top.Style)
	}
	if d.Left.Style != "dashed" {
		t.Errorf("D.left.style=%q want dashed (insideV)", d.Left.Style)
	}
	if d.Bottom.Style != "single" {
		t.Errorf("D.bottom.style=%q want single (outer)", d.Bottom.Style)
	}
	if d.Right.Style != "single" {
		t.Errorf("D.right.style=%q want single (outer)", d.Right.Style)
	}
}

func TestBorderlessTableHasNoCellBorders(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblGrid><w:gridCol w:w="1000"/><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	for ri, row := range tbl.Rows {
		for ci, cell := range row.Cells {
			b := cell.Borders
			if b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has() {
				t.Errorf("row%d cell%d has unexpected borders: %+v — table had no tblBorders/tcBorders", ri, ci, b)
			}
		}
	}
}

func TestTblStyleBordersFlowToCells(t *testing.T) {
	// Style "TableGrid" declares a single-line grid in its tblPr.
	// A table that uses <w:tblStyle w:val="TableGrid"/> but has no
	// inline <w:tblBorders> must inherit the style's grid lines.
	stylesXML := `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="table" w:styleId="TableGrid">
    <w:tblPr>
      <w:tblBorders>
        <w:top w:val="single" w:sz="4" w:color="auto"/>
        <w:bottom w:val="single" w:sz="4" w:color="auto"/>
        <w:left w:val="single" w:sz="4" w:color="auto"/>
        <w:right w:val="single" w:sz="4" w:color="auto"/>
        <w:insideH w:val="single" w:sz="4" w:color="auto"/>
        <w:insideV w:val="single" w:sz="4" w:color="auto"/>
      </w:tblBorders>
    </w:tblPr>
  </w:style>
</w:styles>`
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblPr><w:tblStyle w:val="TableGrid"/></w:tblPr>
      <w:tblGrid><w:gridCol w:w="1000"/><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, stylesXML)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	a := tbl.Rows[0].Cells[0].Borders
	if a.Top.Style != "single" || a.Bottom.Style != "single" ||
		a.Left.Style != "single" || a.Right.Style != "single" {
		t.Errorf("TableGrid style borders did not reach cell A: %+v", a)
	}
}

func TestInlineTblBordersOverrideTblStyle(t *testing.T) {
	// Style declares "single"; table's own tblBorders override to
	// "double" on top edge only. Other edges keep the style's "single".
	stylesXML := `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="table" w:styleId="MyGrid">
    <w:tblPr>
      <w:tblBorders>
        <w:top w:val="single" w:sz="4" w:color="auto"/>
        <w:bottom w:val="single" w:sz="4" w:color="auto"/>
        <w:left w:val="single" w:sz="4" w:color="auto"/>
        <w:right w:val="single" w:sz="4" w:color="auto"/>
      </w:tblBorders>
    </w:tblPr>
  </w:style>
</w:styles>`
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblPr>
        <w:tblStyle w:val="MyGrid"/>
        <w:tblBorders><w:top w:val="double" w:sz="12" w:color="FF0000"/></w:tblBorders>
      </w:tblPr>
      <w:tblGrid><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr><w:tc><w:p><w:r><w:t>cell</w:t></w:r></w:p></w:tc></w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, stylesXML)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	c := tbl.Rows[0].Cells[0].Borders
	if c.Top.Style != "double" || c.Top.Color != "FF0000" {
		t.Errorf("inline tblBorders.top did not override style.top: %+v", c.Top)
	}
	if c.Bottom.Style != "single" {
		t.Errorf("style.bottom did not survive when inline did not set it: %+v", c.Bottom)
	}
}

func TestTcBordersOverrideTblBorders(t *testing.T) {
	docXML := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:tbl>
      <w:tblPr>
        <w:tblBorders>
          <w:top w:val="single" w:sz="8" w:color="auto"/>
          <w:bottom w:val="single" w:sz="8" w:color="auto"/>
          <w:left w:val="single" w:sz="8" w:color="auto"/>
          <w:right w:val="single" w:sz="8" w:color="auto"/>
        </w:tblBorders>
      </w:tblPr>
      <w:tblGrid><w:gridCol w:w="1000"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr>
            <w:tcBorders>
              <w:top w:val="double" w:sz="16" w:color="FF0000"/>
            </w:tcBorders>
          </w:tcPr>
          <w:p><w:r><w:t>cell</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`
	path := buildDocxWithStyles(t, docXML, "")
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl := doc.Body[0].(Table)
	c := tbl.Rows[0].Cells[0].Borders
	if c.Top.Style != "double" || c.Top.Color != "FF0000" {
		t.Errorf("tcBorders.top did not override tblBorders.top: %+v", c.Top)
	}
	if c.Bottom.Style != "single" {
		t.Errorf("tblBorders.bottom did not propagate: %+v", c.Bottom)
	}
}
