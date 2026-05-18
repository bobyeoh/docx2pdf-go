package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildDocxParts is a small helper for in-memory docx fixtures used by the
// extras tests. parts maps zip-relative paths to file contents.
func buildDocxParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocVarsParsed(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>hi</w:t></w:r></w:p></w:body></w:document>`,
		"word/settings.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docVars>
<w:docVar w:name="ReleaseVersion" w:val="2.4.0"/>
<w:docVar w:name="Owner" w:val="Alice"/>
</w:docVars>
</w:settings>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.DocVars["ReleaseVersion"]; got != "2.4.0" {
		t.Errorf("ReleaseVersion = %q, want 2.4.0", got)
	}
	if got := doc.DocVars["Owner"]; got != "Alice" {
		t.Errorf("Owner = %q, want Alice", got)
	}
}

func TestMailMergeDetected(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>tpl</w:t></w:r></w:p></w:body></w:document>`,
		"word/settings.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:mailMerge>
<w:mainDocumentType w:val="formLetter"/>
</w:mailMerge>
</w:settings>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.MailMerge != "formLetter" {
		t.Errorf("MailMerge = %q, want formLetter", doc.MailMerge)
	}
}

func TestCustomPropertiesParsed(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`,
		"docProps/custom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/custom-properties"
            xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
<property fmtid="{D5CDD505-2E9C-101B-9397-08002B2CF9AE}" pid="2" name="AppVersion"><vt:lpwstr>2.4.0</vt:lpwstr></property>
<property fmtid="{D5CDD505-2E9C-101B-9397-08002B2CF9AE}" pid="3" name="ApprovalStatus"><vt:lpwstr>Approved</vt:lpwstr></property>
</Properties>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.CustomProperties["AppVersion"]; got != "2.4.0" {
		t.Errorf("AppVersion = %q, want 2.4.0", got)
	}
	if got := doc.CustomProperties["ApprovalStatus"]; got != "Approved" {
		t.Errorf("ApprovalStatus = %q, want Approved", got)
	}
}

func TestRubyParsed(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r>
<w:ruby>
<w:rubyPr><w:rubyAlign w:val="center"/><w:hps w:val="10"/></w:rubyPr>
<w:rt><w:r><w:t>かんじ</w:t></w:r></w:rt>
<w:rubyBase><w:r><w:t>漢字</w:t></w:r></w:rubyBase>
</w:ruby>
</w:r></w:p></w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Body) == 0 {
		t.Fatal("empty body")
	}
	p, ok := doc.Body[0].(Paragraph)
	if !ok {
		t.Fatal("first block not a Paragraph")
	}
	found := false
	for _, r := range p.Runs {
		if r.Ruby != nil && r.Text == "漢字" && r.Ruby.Text == "かんじ" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ruby annotation not found; runs=%+v", p.Runs)
	}
}

func TestEastAsianLayoutCombine(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r>
<w:rPr><w:eastAsianLayout w:combine="lines" w:combineBrackets="round"/></w:rPr>
<w:t>株式</w:t>
</w:r></w:p></w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	if len(p.Runs) == 0 {
		t.Fatal("no runs")
	}
	ea := p.Runs[0].Props.EALayout
	if ea == nil {
		t.Fatal("EALayout is nil")
	}
	if !ea.Combine {
		t.Error("Combine = false, want true")
	}
	if ea.CombineBrackets != "round" {
		t.Errorf("CombineBrackets = %q, want round", ea.CombineBrackets)
	}
}

func TestW14NumFormSpacingCntxtAlts(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml">
<w:body><w:p><w:r>
<w:rPr>
<w14:numForm w14:val="oldStyle"/>
<w14:numSpacing w14:val="tabular"/>
<w14:cntxtAlts/>
</w:rPr>
<w:t>123</w:t>
</w:r></w:p></w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	rp := doc.Body[0].(Paragraph).Runs[0].Props
	if rp.W14NumForm != "oldStyle" {
		t.Errorf("NumForm = %q, want oldStyle", rp.W14NumForm)
	}
	if rp.W14NumSpacing != "tabular" {
		t.Errorf("NumSpacing = %q, want tabular", rp.W14NumSpacing)
	}
	if rp.W14CntxtAlts != "true" {
		t.Errorf("CntxtAlts = %q, want true (empty val defaults true)", rp.W14CntxtAlts)
	}
}

func TestW14ShadowOutline(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml">
<w:body><w:p><w:r>
<w:rPr>
<w14:shadow><w14:solidFill><w14:srgbClr w14:val="808080"/></w14:solidFill></w14:shadow>
<w14:textOutline w14:w="6350"><w14:solidFill><w14:srgbClr w14:val="FF0000"/></w14:solidFill></w14:textOutline>
<w14:ligatures w14:val="standardContextual"/>
</w:rPr>
<w:t>Title</w:t>
</w:r></w:p></w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	rp := p.Runs[0].Props
	if rp.W14ShadowColor == "" {
		t.Error("W14ShadowColor empty")
	}
	if rp.W14OutlineColor != "FF0000" {
		t.Errorf("W14OutlineColor = %q, want FF0000", rp.W14OutlineColor)
	}
	if rp.W14Ligatures != "standardContextual" {
		t.Errorf("W14Ligatures = %q", rp.W14Ligatures)
	}
}

func TestAltChunkInjected(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:r><w:t>Before</w:t></w:r></w:p>
<w:altChunk r:id="rId99"/>
<w:p><w:r><w:t>After</w:t></w:r></w:p>
</w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId99" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/aFChunk" Target="afchunk1.html"/>
</Relationships>`,
		"word/afchunk1.html": `<html><body><p>Hello</p><p>World</p></body></html>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, b := range doc.Body {
		p, ok := b.(Paragraph)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if r.Text != "" {
				found = append(found, r.Text)
			}
		}
	}
	if len(found) < 4 {
		t.Fatalf("expected at least 4 text runs, got %v", found)
	}
	helloIdx, worldIdx := -1, -1
	for i, t := range found {
		if strings.TrimSpace(t) == "Hello" {
			helloIdx = i
		}
		if strings.TrimSpace(t) == "World" {
			worldIdx = i
		}
	}
	if helloIdx < 0 || worldIdx < 0 {
		t.Errorf("altChunk text not extracted: %v", found)
	}
	if helloIdx >= worldIdx {
		t.Errorf("altChunk paragraph order broken: %v", found)
	}
}

func TestSDTDataBindingResolves(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Hello </w:t></w:r>
<w:sdt>
<w:sdtPr><w:dataBinding w:xpath="/root/customer/name"/></w:sdtPr>
<w:sdtContent><w:r><w:t>FALLBACK</w:t></w:r></w:sdtContent>
</w:sdt>
</w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId50" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/customXml" Target="../customXml/item1.xml"/>
</Relationships>`,
		"customXml/item1.xml": `<?xml version="1.0" encoding="UTF-8"?>
<root><customer><name>Acme Corp</name></customer></root>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	var sb strings.Builder
	for _, r := range p.Runs {
		sb.WriteString(r.Text)
	}
	if !strings.Contains(sb.String(), "Acme Corp") {
		t.Errorf("expected 'Acme Corp' in resolved binding; got %q", sb.String())
	}
	if strings.Contains(sb.String(), "FALLBACK") {
		t.Errorf("fallback content should be replaced; got %q", sb.String())
	}
}

func TestBibliographyParsed(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId100" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/customXml" Target="../customXml/item5.xml"/>
</Relationships>`,
		"customXml/item5.xml": `<?xml version="1.0" encoding="UTF-8"?>
<b:Sources xmlns:b="http://schemas.openxmlformats.org/officeDocument/2006/bibliography">
<b:Source>
<b:Tag>Smith2020</b:Tag>
<b:SourceType>JournalArticle</b:SourceType>
<b:Author><b:Author><b:NameList><b:Person><b:Last>Smith</b:Last><b:First>Jane</b:First></b:Person></b:NameList></b:Author></b:Author>
<b:Title>The Big Study</b:Title>
<b:Year>2020</b:Year>
<b:JournalName>Journal of Things</b:JournalName>
</b:Source>
</b:Sources>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	src, ok := doc.Bibliography["Smith2020"]
	if !ok {
		t.Fatalf("Smith2020 not in bibliography map: %+v", doc.Bibliography)
	}
	if src.Title != "The Big Study" {
		t.Errorf("Title = %q", src.Title)
	}
	if src.Year != "2020" {
		t.Errorf("Year = %q", src.Year)
	}
	if len(src.Authors) == 0 {
		t.Errorf("no authors parsed")
	}
}

func TestPermStartSkipped(t *testing.T) {
	// permStart/permEnd are empty range markers — they should not break
	// document parsing or produce extra content.
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:permStart w:id="0" w:edGrp="everyone"/>
<w:p><w:r><w:t>Locked</w:t></w:r></w:p>
<w:permEnd w:id="0"/>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Body) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Body))
	}
	p := doc.Body[0].(Paragraph)
	if len(p.Runs) == 0 || p.Runs[0].Text != "Locked" {
		t.Errorf("body content lost: %+v", p.Runs)
	}
}

func TestInkPlaceholder(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml">
<w:body><w:p><w:r>
<w14:contentPart r:id="rIdInk1"/>
</w:r></w:p></w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Body[0].(Paragraph)
	hasInk := false
	for _, r := range p.Runs {
		if r.InkPlaceholder {
			hasInk = true
		}
	}
	if !hasInk {
		t.Errorf("ink placeholder run not emitted: %+v", p.Runs)
	}
}

func TestGlossaryDetected(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p/></w:body></w:document>`,
		"word/glossary/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:glossaryDocument xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.HasGlossary {
		t.Error("HasGlossary = false, want true")
	}
}

func TestStripHTMLBasic(t *testing.T) {
	got := stripHTML(`<html><body><p>Hello <b>world</b>!</p><p>Line 2</p></body></html>`)
	if !strings.Contains(got, "Hello world") {
		t.Errorf("stripHTML = %q", got)
	}
	if !strings.Contains(got, "Line 2") {
		t.Errorf("stripHTML missed Line 2: %q", got)
	}
}

func TestStripRTFBasic(t *testing.T) {
	rtf := `{\rtf1\ansi\deff0{\fonttbl{\f0 Arial;}}Hello world.\par Second line.}`
	got := stripRTF(rtf)
	if !strings.Contains(got, "Hello world") {
		t.Errorf("stripRTF missed content: %q", got)
	}
	if !strings.Contains(got, "Second line") {
		t.Errorf("stripRTF missed second line: %q", got)
	}
}

func TestResolveXPathSimple(t *testing.T) {
	parts := []CustomXMLPart{{
		PartName: "test",
		Data:     []byte(`<root><a><b>VALUE</b></a></root>`),
	}}
	got, ok := resolveXPath(parts, "/root/a/b")
	if !ok || got != "VALUE" {
		t.Errorf("resolveXPath = (%q, %v), want VALUE", got, ok)
	}
	if _, ok := resolveXPath(parts, "/root/missing"); ok {
		t.Errorf("resolveXPath should miss /root/missing")
	}
}

func TestParseFFData_Checkbox(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r>
<w:fldChar w:fldCharType="begin">
  <w:ffData>
    <w:name w:val="Subscribe"/>
    <w:checkBox><w:default w:val="1"/></w:checkBox>
  </w:ffData>
</w:fldChar>
<w:instrText> FORMCHECKBOX </w:instrText>
<w:fldChar w:fldCharType="separate"/>
<w:fldChar w:fldCharType="end"/>
</w:r></w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Walk runs and find the FieldBegin run that carries a FormField.
	var found *FormFieldInfo
	for _, b := range doc.Body {
		p, ok := b.(Paragraph)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if r.FieldBegin && r.FormField != nil {
				found = r.FormField
			}
		}
	}
	if found == nil {
		t.Fatal("FFData not captured on FieldBegin")
	}
	if found.Kind != "checkbox" {
		t.Errorf("Kind = %q, want checkbox", found.Kind)
	}
	if !found.Checked {
		t.Errorf("Checked = false, want true (default=1)")
	}
}

func TestParseSdtDate(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:sdt>
  <w:sdtPr>
    <w:date w:fullDate="2024-03-07T00:00:00Z">
      <w:dateFormat w:val="M/d/yyyy"/>
    </w:date>
  </w:sdtPr>
  <w:sdtContent>
    <w:r><w:t>placeholder</w:t></w:r>
  </w:sdtContent>
</w:sdt></w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, b := range doc.Body {
		p, ok := b.(Paragraph)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			got += r.Text
		}
	}
	if got != "3/7/2024" {
		t.Errorf("rendered = %q, want 3/7/2024", got)
	}
}

func TestParseTblInd(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:tbl>
<w:tblPr><w:tblInd w:w="720" w:type="dxa"/></w:tblPr>
<w:tblGrid><w:gridCol w:w="2000"/></w:tblGrid>
<w:tr><w:tc><w:p><w:r><w:t>X</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Body) == 0 {
		t.Fatal("no body blocks")
	}
	tbl, ok := doc.Body[0].(Table)
	if !ok {
		t.Fatalf("body[0] = %T, want Table", doc.Body[0])
	}
	if tbl.IndentTwips != 720 {
		t.Errorf("IndentTwips = %d, want 720", tbl.IndentTwips)
	}
}

func TestParsePageBordersOffset(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>x</w:t></w:r></w:p>
<w:sectPr>
  <w:pgBorders w:offsetFrom="text">
    <w:top w:val="single" w:sz="8" w:space="20"/>
  </w:pgBorders>
</w:sectPr>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) == 0 {
		t.Fatal("no sections")
	}
	b := doc.Sections[0].Borders
	if !b.OffsetFromText {
		t.Error("OffsetFromText = false, want true")
	}
	if b.OffsetTopPt != 20 {
		t.Errorf("OffsetTopPt = %v, want 20", b.OffsetTopPt)
	}
}

func TestParsePrstGeom(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:r><w:drawing><wp:inline>
  <wp:extent cx="914400" cy="457200"/>
  <wp:docPr id="1" name="star" descr="Yellow star"/>
  <a:graphic><a:graphicData>
    <a:sp>
      <a:spPr><a:prstGeom prst="star5"/><a:solidFill><a:srgbClr val="FFFF00"/></a:solidFill></a:spPr>
    </a:sp>
  </a:graphicData></a:graphic>
</wp:inline></w:drawing></w:r></w:p>
</w:body></w:document>`,
	}
	data := buildDocxParts(t, parts)
	doc, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var sh *VMLShape
	var altText string
	for _, b := range doc.Body {
		p, ok := b.(Paragraph)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if r.VMLShape != nil {
				sh = r.VMLShape
				altText = r.AltText
			}
		}
	}
	if sh == nil {
		t.Fatal("no VMLShape captured from prstGeom")
	}
	if sh.Kind != "star5" {
		t.Errorf("Kind = %q, want star5", sh.Kind)
	}
	if sh.FillColor != "FFFF00" {
		t.Errorf("FillColor = %q, want FFFF00", sh.FillColor)
	}
	if altText != "Yellow star" {
		t.Errorf("AltText = %q, want Yellow star", altText)
	}
}
