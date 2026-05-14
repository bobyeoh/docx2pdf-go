//go:build ignore

// Generates testdata/sample_hf.docx — a small docx with explicit header /
// footer parts referenced from sectPr, used to verify the renderer stamps
// them onto every page. Run with:
//
//	go run testdata/make_hf.go
package main

import (
	"archive/zip"
	"log"
	"os"
)

var docXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="H1"/></w:pPr>
      <w:r><w:t>Headers + Footers demo</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t xml:space="preserve">This document spans more than one page so we can verify the header text appears at the top of every page and the footer text appears at the bottom. The body content here is filler.</w:t></w:r></w:p>` +
	repeatParagraph(120, "Line ", " — filler text to push content to the next page so header/footer rendering is exercised.") + `
    <w:sectPr>
      <w:headerReference r:id="rH" w:type="default"/>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>
  </w:body>
</w:document>`

const headerXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="right"/></w:pPr>
    <w:r><w:rPr><w:i/><w:color w:val="808080"/></w:rPr><w:t>docx2pdf-go demo · header</w:t></w:r>
  </w:p>
</w:hdr>`

// footerXML exercises the field machinery: PAGE and NUMPAGES wrapped in
// w:fldChar / w:instrText, with cached "?" results that the renderer will
// rewrite to the actual current/total pages.
const footerXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:rPr><w:color w:val="808080"/></w:rPr><w:t xml:space="preserve">Page </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">PAGE   \* MERGEFORMAT</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:rPr><w:color w:val="808080"/></w:rPr><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
    <w:r><w:rPr><w:color w:val="808080"/></w:rPr><w:t xml:space="preserve"> of </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">NUMPAGES   \* MERGEFORMAT</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:rPr><w:color w:val="808080"/></w:rPr><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p>
</w:ftr>`

const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="H1">
    <w:pPr><w:spacing w:before="200" w:after="200"/></w:pPr>
    <w:rPr><w:b/><w:sz w:val="36"/><w:color w:val="2E74B5"/></w:rPr>
  </w:style>
</w:styles>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH"
      Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header"
      Target="header1.xml"/>
  <Relationship Id="rF"
      Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer"
      Target="footer1.xml"/>
</Relationships>`

func repeatParagraph(n int, prefix, suffix string) string {
	out := ""
	for i := 1; i <= n; i++ {
		out += "<w:p><w:r><w:t xml:space=\"preserve\">" + prefix + itoa(i) + suffix + "</w:t></w:r></w:p>\n"
	}
	return out
}

func itoa(n int) string {
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

func main() {
	f, err := os.Create("testdata/sample_hf.docx")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			log.Fatal(err)
		}
	}
	write("word/document.xml", docXML)
	write("word/styles.xml", stylesXML)
	write("word/header1.xml", headerXML)
	write("word/footer1.xml", footerXML)
	write("word/_rels/document.xml.rels", relsXML)
}
