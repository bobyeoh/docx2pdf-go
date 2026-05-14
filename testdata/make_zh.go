//go:build ignore

// Generates testdata/sample_zh.docx — a tiny Chinese-text docx for verifying
// the CJK fallback + per-character line breaking. Run with:
//
//	go run testdata/make_zh.go
package main

import (
	"archive/zip"
	"log"
	"os"
)

const docXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:jc w:val="center"/></w:pPr>
      <w:r><w:rPr><w:b/><w:sz w:val="40"/></w:rPr><w:t>docx2pdf-go 中文渲染测试</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:spacing w:line="360" w:lineRule="auto"/></w:pPr>
      <w:r><w:t xml:space="preserve">这是一段较长的中文文字(1.5 倍行距),目的是验证段落能在没有空格的情况下被正确折行,以及 w:spacing w:line 的多倍行距生效。本工具会把每个汉字当作独立的断点候选。混排英文 like this still wraps on whitespace 也应当正常。</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:spacing w:line="480" w:lineRule="auto"/></w:pPr>
      <w:r><w:t xml:space="preserve">这是另一段(2.0 倍行距)。在长文档里,行距是最影响可读性的设置之一,docx 里通过 w:spacing w:line 控制,w:lineRule="auto" 时表示倍数(240 = 单倍,360 = 1.5,480 = 双倍)。</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>第一项:支持加粗、斜体、颜色</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>第二项:支持简单表格</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>第三项:行内图片</w:t></w:r>
    </w:p>
    <w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="6000"/></w:tblGrid>
      <w:tr><w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>项目</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>说明</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr><w:tc><w:p><w:r><w:t>解析器</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>用 encoding/xml 流式解析 word/document.xml</w:t></w:r></w:p></w:tc></w:tr>
      <w:tr><w:tc><w:p><w:r><w:t>渲染器</w:t></w:r></w:p></w:tc>
            <w:tc><w:p><w:r><w:t>用 gopdf 直渲;CJK 走 fallback 字体</w:t></w:r></w:p></w:tc></w:tr>
    </w:tbl>
  </w:body>
</w:document>`

const numXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
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

func main() {
	f, err := os.Create("testdata/sample_zh.docx")
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
	write("word/numbering.xml", numXML)
}
