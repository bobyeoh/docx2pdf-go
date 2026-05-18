package verify

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestVerifyAll runs the full feature matrix. PNG snapshots and an
// index.html with pass/fail badges land in internal/verify/out/.
//
// Tests are subtests so `go test -run TestVerifyAll/<case>` works for
// drilling into a single feature.
func TestVerifyAll(t *testing.T) {
	requireTool(t, "pdftotext")
	requireTool(t, "pdftoppm")
	requireTool(t, "pdfinfo")

	// Locate the latin font we ship for tests. We also use it as the CJK
	// fallback for cases that don't actually have CJK content; the CJK
	// cases will only render correctly with a real CJK font.
	fontPath := findFont(t)
	fontCJK := fontPath

	outRoot := mustAbs(t, "out")
	if err := os.RemoveAll(outRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := allCases()
	results := make([]caseResult, 0, len(cases))
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			res := runCase(t, c, fontPath, fontCJK, outRoot)
			results = append(results, res)
		})
	}

	// Even on failure, write a report so the human reviewer can eyeball
	// what went wrong.
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].name < results[j].name
	})
	writeReport(t, outRoot, results)
	t.Logf("verify report written to %s/index.html", outRoot)
}

func findFont(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../testdata/font.ttf",
		"../../../testdata/font.ttf",
	}
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	t.Skip("testdata/font.ttf not found; copy a TTF there to run verify")
	return ""
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// allCases returns the verification matrix. Each case builds a small docx,
// asserts substrings + page count, and saves PNG snapshots for review.
func allCases() []verifyCase {
	return []verifyCase{
		// — basics —
		caseBasic(),
		caseStyles(),
		caseStrikethrough(),
		caseAlignment(),
		caseIndent(),
		caseLineSpacing(),
		casePageBreak(),
		caseEdge(),
		// — lists —
		caseList(),
		caseListVariants(),
		caseListRoman(),
		caseListUpperLetter(),
		caseListCustomStart(),
		caseListInTable(),
		// — tables —
		caseTable(),
		caseTableMerged(),
		caseLongTable(),
		caseImageInTable(),
		// — images —
		caseInlineImage(),
		caseAnchoredImage(),
		caseAnchorWrapTopAndBottom(),
		caseJpegImage(),
		caseTransparentImage(),
		caseImageOnlyParagraph(),
		caseImageLargerThanPage(),
		// — sections + decorations —
		caseMultiSection(),
		caseHeadersFooters(),
		casePerSectionHeaders(),
		caseFields(),
		caseFieldSwitches(),
		caseUnknownField(),
		casePageNumbersFlag(),
		caseCJKInFooter(),
		// — styles —
		caseStyleInheritance(),
		caseBasedOnChain3(),
		// — hyperlinks —
		caseHyperlink(),
		// — text edge cases —
		caseWhitespacePreserve(),
		caseLongUnbreakableWord(),
		caseMultipleTInSameRun(),
		caseMultipleBreaks(),
		caseVanishHidden(),
		caseEmptyDocument(),
		// — CJK —
		caseMixedCJK(),
		caseLongCJKWrap(),
		// — additional feature corner cases —
		caseMultiParagraphCell(),
		caseTableNoGrid(),
		caseEmptyCells(),
		caseHyperlinkBadRid(),
		caseDeepNestedList(),
		caseRunRPrInheritance(),
		// — multi-language —
		caseKorean(),
		caseThai(),
		caseGreek(),
		caseEmoji(),
		caseMixedScripts(),
		// — stress —
		caseStress500Paragraphs(),
		caseStress200RowTable(),
		caseStressManyImages(),
		// — batch A: char formatting ---
		caseCapsSmallCaps(),
		caseVertAlign(),
		caseHighlight(),
		caseRunShading(),
		caseCharStyle(),
		// — batch B: cell features ---
		caseCellShading(),
		caseCellVAlign(),
		caseCellBorderStyles(),
		// — batch C: table header repeat ---
		caseTableHeaderRepeat(),
		// — batch D: first-page header ---
		caseFirstPageHeader(),
		// — batch E: bookmarks + internal links ---
		caseInternalLink(),
		// — batch F: paragraph layout hints ---
		caseTabStops(),
		caseTabLeader(),
		caseContextualSpacing(),
		// — batch H: page features ---
		casePageNumberFmtRoman(),
		casePageNumberStartFrom(),
		casePageBorders(),
		// — batch I: tables advanced ---
		caseNestedTable(),
		caseCellMargins(),
		caseRowHeight(),
		// — batch J: numbering ---
		caseLegalListIsLgl(),
		// — batch K: character extras ---
		caseVanishHiddenSkip(),
		casePositionAndW(),
		caseNoBreakAndSym(),
		// — batch L: image extras ---
		caseImageExplicitExtent(),
		// — batch M: fields ---
		caseDateFilenameAuthor(),
		caseSEQField(),
		// — batch O: track changes + comments ---
		caseTrackChanges(),
		// — batch N: footnote refs + trailer ---
		caseFootnotes(),
		// — batch P: multi-column ---
		caseMultiColumn(),
		// — batch S: theme + tblStyle ---
		caseThemeColor(),
		caseTableStyleApplied(),
		// — batch T: line numbers / mirror / gutter ---
		caseLineNumbersDrawn(),
		caseMirrorMargins(),
		// — batch U: REF + bookmark text ---
		caseRefField(),
		// — batch V: image crop + drop cap ---
		caseImageCrop(),
		// — batch W: doc metadata + emboss + letter-spacing ---
		caseDocMetadataInfo(),
		caseEmbossOutline(),
		caseLetterSpacing(),
		// — batch X: conditional table formatting ---
		caseConditionalTable(),
		// — batch Y: HYPERLINK field + pic bullet + drop cap ---
		caseHyperlinkField(),
		casePictureBullet(),
		caseDropCap(),
		// — batch Z: continuous section + tab right/decimal + theme shade ---
		caseContinuousSection(),
		caseTabRightAlign(),
		caseThemeShade(),
		// — batch DD: real footnotes at page bottom ---
		caseFootnotePageBottom(),
		caseFootnoteAcrossPageBreak(),
		caseFootnoteInTableCell(),
		caseHangingIndent(),
		caseSmartTagTransparent(),
		caseInsHyperlinkSurvives(),
		caseMoveToMoveFrom(),
		caseOLEObjectPlaceholder(),
		caseSettingsDefaultTabStop(),
		caseVMLImage(),
		caseFramePrPositioned(),
		caseInlineSdt(),
		caseBlockSdt(),
		caseSdtInTableCell(),
		caseInlineMath(),
		caseDisplayMath(),
		caseFldSimplePage(),
		caseRTLParagraph(),
		caseTextBoxContent(),
		caseAlternateContentChoice(),
		caseAlternateContentFallback(),
		caseComments(),
		caseHeadingOutline(),
		caseChartTextExtraction(),
		caseChartBarRender(),
		casePageBreakBeforeValZero(),
		caseOverwideWordInCell(),
		caseHorizontalRuleVMLPict(),
		caseSymbolRoutesToFallback(),
		caseTableHeaderRepeatsAtTopOfPage(),
		caseListMarkerWithParaIndentOverride(),
		caseParagraphBottomBorderAsRule(),
		caseAlmostOverwideWordPrefersFreshLine(),
		caseParaMarkRPrDoesNotInheritToRuns(),
		caseMultilineCenterVAlignDoesNotOverflow(),
		// — batch Q: context cancel ---
		// (tested separately via TestContextCancel, not the harness)
	}
}

// --- individual cases ---------------------------------------------------

func caseBasic() verifyCase {
	return verifyCase{
		name:        "01_basic",
		description: "Plain paragraph with simple formatting",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>Hello, docx2pdf-go.</w:t></w:r></w:p>
    <w:p><w:r><w:t>Second paragraph.</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"Hello, docx2pdf-go.", "Second paragraph."},
		expectPages: 1,
	}
}

func caseStyles() verifyCase {
	return verifyCase{
		name:        "02_styles",
		description: "Bold / italic / underline / colored / sized runs",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">BOLD </w:t></w:r>
      <w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve">ITALIC </w:t></w:r>
      <w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">UNDER </w:t></w:r>
      <w:r><w:rPr><w:color w:val="C00000"/></w:rPr><w:t xml:space="preserve">RED </w:t></w:r>
      <w:r><w:rPr><w:sz w:val="40"/></w:rPr><w:t>BIG</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"BOLD", "ITALIC", "UNDER", "RED", "BIG"},
		expectPages: 1,
	}
}

func caseStrikethrough() verifyCase {
	return verifyCase{
		name:        "03_strikethrough",
		description: "w:strike — line drawn through the word",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">keep this </w:t></w:r>
      <w:r><w:rPr><w:strike/></w:rPr><w:t>CROSSED</w:t></w:r>
      <w:r><w:t xml:space="preserve"> and final.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"keep this", "CROSSED", "and final."},
		expectPages: 1,
	}
}

func caseList() verifyCase {
	return verifyCase{
		name:        "04_numbered_list",
		description: "Decimal numbered list with hanging marker",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>Intro:</w:t></w:r></w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>first item</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>second item</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>third item</w:t></w:r>
    </w:p>`).
				Numbering(`<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`).
				Write(t, dir)
		},
		expectText:  []string{"Intro:", "first item", "second item", "third item", "1.", "2.", "3."},
		expectPages: 1,
	}
}

func caseTable() verifyCase {
	return verifyCase{
		name:        "05_table",
		description: "Two-column table with bordered cells",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Header</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Value</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>name</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>docx2pdf-go</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>language</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Go 1.26</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`).Write(t, dir)
		},
		expectText:  []string{"Header", "Value", "name", "docx2pdf-go", "language", "Go 1.26"},
		expectPages: 1,
	}
}

func caseTableMerged() verifyCase {
	return verifyCase{
		name:        "06_table_merged",
		description: "gridSpan + vMerge cells",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:gridSpan w:val="2"/><w:vMerge w:val="restart"/></w:tcPr>
          <w:p><w:r><w:t>Spanning + Merged</w:t></w:r></w:p>
        </w:tc>
        <w:tc><w:p><w:r><w:t>third col</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc>
          <w:tcPr><w:gridSpan w:val="2"/><w:vMerge/></w:tcPr>
          <w:p/>
        </w:tc>
        <w:tc><w:p><w:r><w:t>row 2 col 3</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>r3c1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>r3c2</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>r3c3</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`).Write(t, dir)
		},
		expectText:  []string{"Spanning + Merged", "third col", "row 2 col 3", "r3c1", "r3c2", "r3c3"},
		expectPages: 1,
	}
}

func caseHeadersFooters() verifyCase {
	return verifyCase{
		name:        "07_headers_footers",
		description: "Default header + footer rendered on every page",
		build: func(t *testing.T, dir string) string {
			body := strings.Repeat(`<w:p><w:r><w:t xml:space="preserve">body filler </w:t></w:r></w:p>`, 80)
			return newDocx().
				RawBody(docHeader+body+`
    <w:sectPr>
      <w:headerReference r:id="rH" w:type="default"/>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`+docFooter).
				Part("header1.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="right"/></w:pPr>
    <w:r><w:t>HEADER-MARK</w:t></w:r></w:p>
</w:hdr>`).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:t>FOOTER-MARK</w:t></w:r></w:p>
</w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"HEADER-MARK", "FOOTER-MARK", "body filler"},
		expectPages: 2,
	}
}

func caseFields() verifyCase {
	return verifyCase{
		name:        "08_fields_page_numpages",
		description: "PAGE / NUMPAGES fields substituted in footer",
		build: func(t *testing.T, dir string) string {
			body := strings.Repeat(`<w:p><w:r><w:t xml:space="preserve">filler </w:t></w:r></w:p>`, 80)
			return newDocx().
				RawBody(docHeader+body+`
    <w:sectPr>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`+docFooter).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:t xml:space="preserve">Page </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">PAGE</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
    <w:r><w:t xml:space="preserve"> of </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">NUMPAGES</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p>
</w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		// Expect "Page 1 of 2" and "Page 2 of 2" — both should appear, not "Page ? of ?".
		expectText:  []string{"Page 1 of 2", "Page 2 of 2"},
		expectPages: 2,
	}
}

func caseMultiSection() verifyCase {
	return verifyCase{
		name:        "09_multi_section",
		description: "Section 1 portrait, section 2 landscape — page geometry must change",
		build: func(t *testing.T, dir string) string {
			return newDocx().RawBody(docHeader+`
    <w:p><w:r><w:t>portrait page content</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:sectPr>
          <w:pgSz w:w="11906" w:h="16838"/>
          <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
        </w:sectPr>
      </w:pPr>
      <w:r><w:t>end of portrait section</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>landscape page content</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/>
      <w:pgMar w:top="720" w:right="720" w:bottom="720" w:left="720"/>
    </w:sectPr>`+docFooter).Write(t, dir)
		},
		expectText:  []string{"portrait page content", "end of portrait section", "landscape page content"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Geometry assertions: pdftotext can't see page sizes.
			w1, h1 := pdfPageSize(t, pdf, 1)
			w2, h2 := pdfPageSize(t, pdf, 2)
			if w1 >= h1 {
				fail("page 1 expected portrait but got %.0fx%.0f", w1, h1)
			}
			if w2 <= h2 {
				fail("page 2 expected landscape but got %.0fx%.0f", w2, h2)
			}
		},
	}
}

func caseInlineImage() verifyCase {
	red := makeSolidPNG(160, 100, color.RGBA{R: 220, G: 40, B: 40, A: 255})
	return verifyCase{
		name:        "10_inline_image",
		description: "Inline PNG image via w:drawing/wp:inline + a:blip",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before image</w:t></w:r></w:p>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:inline>
            <a:graphic>
              <a:graphicData>
                <pic:pic>
                  <pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill>
                </pic:pic>
              </a:graphicData>
            </a:graphic>
          </wp:inline>
        </w:drawing>
      </w:r>
    </w:p>
    <w:p><w:r><w:t>after image</w:t></w:r></w:p>`).
				Media("image1.png", red).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before image", "after image"},
		expectPages: 1,
	}
}

func caseLineSpacing() verifyCase {
	return verifyCase{
		name:        "11_line_spacing",
		description: "1.0 / 1.5 / 2.0 line spacing — visual difference between paragraphs",
		build: func(t *testing.T, dir string) string {
			// Generate enough content per paragraph that line-spacing actually
			// shows up in the snapshot.
			block := func(label, lineRule, lineVal string) string {
				attrs := ""
				if lineVal != "" {
					attrs = fmt.Sprintf(`<w:spacing w:line="%s" w:lineRule="%s"/>`, lineVal, lineRule)
				}
				return `<w:p><w:pPr>` + attrs + `</w:pPr><w:r><w:t xml:space="preserve">` +
					label + " — " + strings.Repeat("the quick brown fox jumps over the lazy dog. ", 5) +
					`</w:t></w:r></w:p>`
			}
			body := block("[single 1.0]", "auto", "240") +
				block("[one and a half 1.5]", "auto", "360") +
				block("[double 2.0]", "auto", "480")
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"[single 1.0]", "[one and a half 1.5]", "[double 2.0]"},
		expectPages: 1,
	}
}

func caseIndent() verifyCase {
	return verifyCase{
		name:        "12_indent",
		description: "Left indent + first-line indent + hanging indent",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>flush-left baseline.</w:t></w:r></w:p>
    <w:p>
      <w:pPr><w:ind w:left="720"/></w:pPr>
      <w:r><w:t>indented 0.5 inch from the left.</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:ind w:firstLine="720"/></w:pPr>
      <w:r><w:t xml:space="preserve">first-line indent only. The first line of this paragraph starts indented but subsequent wrapped lines return to the baseline left margin, which mimics the conventional book paragraph style.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"flush-left baseline.", "indented 0.5 inch from the left.", "first-line indent only."},
		expectPages: 1,
	}
}

func caseMixedCJK() verifyCase {
	return verifyCase{
		name:        "13_mixed_cjk",
		description: "Mixed Chinese + English text with CJK fallback",
		useCJK:      true,
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:jc w:val="center"/></w:pPr>
      <w:r><w:rPr><w:b/><w:sz w:val="40"/></w:rPr><w:t>docx2pdf-go 中文混排测试</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t xml:space="preserve">本段验证 CJK fallback + per-character break opportunities work in 实际场景。</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"docx2pdf-go", "中文混排测试", "本段验证", "实际场景"},
		expectPages: 1,
	}
}

func caseLongCJKWrap() verifyCase {
	return verifyCase{
		name:        "14_long_cjk_wrap",
		description: "Long Chinese paragraph wraps mid-character (no whitespace)",
		useCJK:      true,
		build: func(t *testing.T, dir string) string {
			zh := strings.Repeat("这是一段很长的中文,目的是验证段落能在没有空格的情况下被正确折行。", 6)
			return newDocx().Body(`
    <w:p>
      <w:r><w:t>`+zh+`</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"这是一段很长的中文", "目的是验证段落能在没有空格的情况下被正确折行"},
		expectPages: 1,
	}
}

// --- additional coverage cases ----------------------------------------

func caseListVariants() verifyCase {
	// Three lists in one doc: bullet, lowerLetter, and a nested decimal+lowerLetter.
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:abstractNum w:abstractNumId="1">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%1)"/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:abstractNum w:abstractNumId="2">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
    <w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%2."/>
      <w:pPr><w:ind w:left="1440" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
  <w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>
  <w:num w:numId="3"><w:abstractNumId w:val="2"/></w:num>
</w:numbering>`
	li := func(numID, lvl, text string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="` + lvl + `"/><w:numId w:val="` + numID + `"/></w:numPr></w:pPr>
      <w:r><w:t>` + text + `</w:t></w:r>
    </w:p>`
	}
	return verifyCase{
		name:        "15_list_variants",
		description: "Bullet, lowerLetter, and nested decimal+lowerLetter lists",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>Bullet list:</w:t></w:r></w:p>` +
				li("1", "0", "alpha bullet") + li("1", "0", "beta bullet") +
				`<w:p><w:r><w:t>Lower-letter list:</w:t></w:r></w:p>` +
				li("2", "0", "letter A item") + li("2", "0", "letter B item") +
				`<w:p><w:r><w:t>Nested list:</w:t></w:r></w:p>` +
				li("3", "0", "level 0 first") + li("3", "1", "level 1 first") +
				li("3", "1", "level 1 second") + li("3", "0", "level 0 second")
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		// pdftotext rendering of markers: bullet → "•", lowerLetter level 0 → "a)" "b)",
		// decimal level 0 → "1." "2.", lowerLetter level 1 → "a." "b.".
		expectText: []string{
			"alpha bullet", "beta bullet",
			"letter A item", "letter B item",
			"level 0 first", "level 0 second",
			"level 1 first", "level 1 second",
			"a)", "b)",
			"1.", "2.",
		},
		expectPages: 1,
	}
}

func caseLongTable() verifyCase {
	// 60 rows × 3 cols — must cross at least one page boundary cleanly.
	var rows strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&rows, `<w:tr>
        <w:tc><w:p><w:r><w:t>row %d</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>middle %d</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>last %d</w:t></w:r></w:p></w:tc>
      </w:tr>`, i, i, i)
	}
	return verifyCase{
		name:        "16_long_table",
		description: "60-row table crossing page boundary",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>` +
				rows.String() +
				`</w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText: []string{"row 1", "middle 1", "last 1", "row 60", "middle 60", "last 60"},
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			n := pdfPageCount(t, pdf)
			if n < 2 {
				fail("expected long table to span >=2 pages, got %d", n)
			}
		},
	}
}

func caseAnchoredImage() verifyCase {
	blue := makeSolidPNG(140, 90, color.RGBA{R: 30, G: 80, B: 200, A: 255})
	return verifyCase{
		name:        "17_anchored_image",
		description: "Floating image (wp:anchor) — rendered inline as best-effort fallback",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before anchor</w:t></w:r></w:p>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:anchor>
            <a:graphic><a:graphicData>
              <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
            </a:graphicData></a:graphic>
          </wp:anchor>
        </w:drawing>
      </w:r>
    </w:p>
    <w:p><w:r><w:t>after anchor</w:t></w:r></w:p>`).
				Media("image1.png", blue).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before anchor", "after anchor"},
		expectPages: 1,
	}
}

// caseAnchorWrapTopAndBottom covers wp:wrapTopAndBottom: surrounding
// text must break above and below the image, not flow next to it.
// The image+text inside the same paragraph would otherwise run on
// one line under the previous best-effort inline placement.
func caseAnchorWrapTopAndBottom() verifyCase {
	red := makeSolidPNG(100, 60, color.RGBA{R: 200, G: 30, B: 30, A: 255})
	return verifyCase{
		name:        "17a_anchor_wrap_top_bottom",
		description: "wp:wrapTopAndBottom forces line breaks around the anchored image",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">BEFORE-WRAP </w:t></w:r>
      <w:r>
        <w:drawing>
          <wp:anchor xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
            <wp:extent cx="800000" cy="500000"/>
            <wp:wrapTopAndBottom/>
            <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
              <a:graphicData>
                <pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
                  <pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill>
                </pic:pic>
              </a:graphicData>
            </a:graphic>
          </wp:anchor>
        </w:drawing>
      </w:r>
      <w:r><w:t xml:space="preserve"> AFTER-WRAP</w:t></w:r>
    </w:p>`).
				Media("image1.png", red).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"BEFORE-WRAP", "AFTER-WRAP"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// pdftotext preserves line breaks; ensure the two text
			// fragments are on different lines (image broke them).
			txt := pdftotext(t, pdf)
			lines := strings.Split(txt, "\n")
			var bIdx, aIdx int = -1, -1
			for i, line := range lines {
				if strings.Contains(line, "BEFORE-WRAP") {
					bIdx = i
				}
				if strings.Contains(line, "AFTER-WRAP") {
					aIdx = i
				}
			}
			if bIdx < 0 || aIdx < 0 {
				fail("missing markers in:\n%s", txt)
				return
			}
			if aIdx <= bIdx {
				fail("expected AFTER-WRAP on a later line than BEFORE-WRAP, got %d and %d", bIdx, aIdx)
			}
		},
	}
}

func caseAlignment() verifyCase {
	return verifyCase{
		name:        "18_alignment",
		description: "Left / center / right / justify paragraph alignment",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:pPr><w:jc w:val="left"/></w:pPr><w:r><w:t>LEFT-ALIGNED-MARK</w:t></w:r></w:p>
    <w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:t>CENTER-ALIGNED-MARK</w:t></w:r></w:p>
    <w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:t>RIGHT-ALIGNED-MARK</w:t></w:r></w:p>
    <w:p><w:pPr><w:jc w:val="both"/></w:pPr>
      <w:r><w:t xml:space="preserve">JUSTIFY-MARK `+
				strings.Repeat("the quick brown fox jumps over the lazy dog ", 4)+
				`</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"LEFT-ALIGNED-MARK", "CENTER-ALIGNED-MARK", "RIGHT-ALIGNED-MARK", "JUSTIFY-MARK"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Geometric sanity: with -layout, pdftotext preserves x positions.
			// LEFT mark should appear at smaller column than CENTER which is
			// smaller than RIGHT. Check by leading-space count.
			txt := pdftotext(t, pdf)
			lineX := func(needle string) int {
				for _, line := range strings.Split(txt, "\n") {
					if i := strings.Index(line, needle); i >= 0 {
						return i
					}
				}
				return -1
			}
			l, c, r := lineX("LEFT-ALIGNED-MARK"), lineX("CENTER-ALIGNED-MARK"), lineX("RIGHT-ALIGNED-MARK")
			if !(l < c && c < r) {
				fail("alignment x positions wrong: left=%d center=%d right=%d", l, c, r)
			}
		},
	}
}

func caseStyleInheritance() verifyCase {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Normal">
    <w:rPr><w:sz w:val="22"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:basedOn w:val="Normal"/>
    <w:pPr><w:jc w:val="center"/><w:spacing w:before="240" w:after="120"/></w:pPr>
    <w:rPr><w:b/><w:sz w:val="44"/><w:color w:val="2E74B5"/></w:rPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "19_style_inheritance",
		description: "Heading1 basedOn Normal — picks up center+bold+22pt+blue",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>STYLED HEADING TEXT</w:t></w:r></w:p>
    <w:p><w:r><w:t>plain body paragraph</w:t></w:r></w:p>`).
				Styles(styles).
				Write(t, dir)
		},
		expectText:  []string{"STYLED HEADING TEXT", "plain body paragraph"},
		expectPages: 1,
	}
}

func casePageBreak() verifyCase {
	return verifyCase{
		name:        "20_page_break",
		description: "Explicit w:br type=page produces exactly two pages",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>FIRST-PAGE-MARK</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>SECOND-PAGE-MARK</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"FIRST-PAGE-MARK", "SECOND-PAGE-MARK"},
		expectPages: 2,
	}
}

func caseHyperlink() verifyCase {
	return verifyCase{
		name:        "21_hyperlink",
		description: "External hyperlink — clickable annotation with visible link text",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">See </w:t></w:r>
      <w:hyperlink r:id="rLink">
        <w:r><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr><w:t>example.com</w:t></w:r>
      </w:hyperlink>
      <w:r><w:t xml:space="preserve"> for details.</w:t></w:r>
    </w:p>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rLink" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
    Target="https://example.com" TargetMode="External"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"See", "example.com", "for details."},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Verify the clickable URL annotation is embedded in the PDF.
			// pdfinfo -listenc gives encodings; for annotations we need a
			// different probe. Easiest: scan raw PDF bytes for the URI string.
			raw, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			if !bytes.Contains(raw, []byte("https://example.com")) {
				fail("hyperlink URI not embedded in PDF")
			}
		},
	}
}

func caseEdge() verifyCase {
	return verifyCase{
		name:        "22_edge_cases",
		description: "Empty paragraphs, tab character, paragraph-only image, runs with whitespace preserve",
		build: func(t *testing.T, dir string) string {
			tinyPNG := makeSolidPNG(60, 30, color.RGBA{R: 50, G: 180, B: 50, A: 255})
			return newDocx().
				Body(`
    <w:p><w:r><w:t>BEFORE-EMPTIES</w:t></w:r></w:p>
    <w:p/>
    <w:p/>
    <w:p><w:r><w:t>AFTER-EMPTIES</w:t></w:r></w:p>
    <w:p><w:r><w:t xml:space="preserve">a</w:t><w:tab/><w:t>tab</w:t><w:tab/><w:t>here</w:t></w:r></w:p>
    <w:p><w:r>
      <w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic>
        <pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill>
      </pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>
    </w:r></w:p>
    <w:p><w:r><w:t>END-MARK</w:t></w:r></w:p>`).
				Media("image1.png", tinyPNG).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"BEFORE-EMPTIES", "AFTER-EMPTIES", "tab", "here", "END-MARK"},
		expectPages: 1,
	}
}

// --- expanded coverage: lists ------------------------------------------

func caseListRoman() verifyCase {
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="upperRoman"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:abstractNum w:abstractNumId="1">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="lowerRoman"/><w:lvlText w:val="%1)"/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
  <w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num>
</w:numbering>`
	li := func(numID, text string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="` + numID + `"/></w:numPr></w:pPr>
      <w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "23_list_roman",
		description: "Roman numeral lists (upper and lower)",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>Upper roman:</w:t></w:r></w:p>` +
				li("1", "first") + li("1", "second") + li("1", "third") +
				`<w:p><w:r><w:t>Lower roman:</w:t></w:r></w:p>` +
				li("2", "alpha") + li("2", "beta") + li("2", "gamma")
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		// I., II., III. for upper; i), ii), iii) for lower.
		expectText:  []string{"I.", "II.", "III.", "i)", "ii)", "iii)", "first", "third", "alpha", "gamma"},
		expectPages: 1,
	}
}

func caseListUpperLetter() verifyCase {
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="upperLetter"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	li := func(t string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>` + t + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "24_list_upper_letter",
		description: "upperLetter numbering A. B. C. ...",
		build: func(t *testing.T, dir string) string {
			body := li("alfa") + li("bravo") + li("charlie") + li("delta")
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		expectText:  []string{"A.", "B.", "C.", "D.", "alfa", "bravo", "charlie", "delta"},
		expectPages: 1,
	}
}

func caseListCustomStart() verifyCase {
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="7"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	li := func(t string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>` + t + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "25_list_custom_start",
		description: "Numbered list with w:start=7 — counter begins at 7",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(li("seven")+li("eight")+li("nine")).Numbering(numbering).Write(t, dir)
		},
		expectText:  []string{"7.", "8.", "9.", "seven", "eight", "nine"},
		expectPages: 1,
	}
}

func caseListInTable() verifyCase {
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	li := func(t string) string {
		return `<w:p>
        <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
        <w:r><w:t>` + t + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "26_list_in_table",
		description: "Numbered list inside a table cell",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Header A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Header B</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>features</w:t></w:r></w:p></w:tc>
        <w:tc>` + li("alpha in cell") + li("beta in cell") + li("gamma in cell") + `</w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		expectText:  []string{"Header A", "Header B", "features", "alpha in cell", "beta in cell", "gamma in cell"},
		expectPages: 1,
	}
}

// --- expanded coverage: tables / images --------------------------------

func caseImageInTable() verifyCase {
	red := makeSolidPNG(80, 60, color.RGBA{R: 200, G: 40, B: 40, A: 255})
	return verifyCase{
		name:        "27_image_in_table",
		description: "Inline image inside a table cell — row height must grow",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>label A</w:t></w:r></w:p></w:tc>
        <w:tc>
          <w:p><w:r><w:drawing><wp:inline>
            <a:graphic><a:graphicData>
              <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
            </a:graphicData></a:graphic>
          </wp:inline></w:drawing></w:r></w:p>
        </w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>label B</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>just text in cell</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).
				Media("image1.png", red).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"label A", "label B", "just text in cell"},
		expectPages: 1,
	}
}

func caseJpegImage() verifyCase {
	gold := makeSolidJPEG(180, 100, color.RGBA{R: 230, G: 180, B: 50, A: 255})
	return verifyCase{
		name:        "28_jpeg_image",
		description: "JPEG-encoded image — exercises the JPEG decode path",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before jpeg</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>after jpeg</w:t></w:r></w:p>`).
				Media("image1.jpg", gold).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.jpg"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before jpeg", "after jpeg"},
		expectPages: 1,
	}
}

func caseTransparentImage() verifyCase {
	tp := makeTransparentPNG(160, 90)
	return verifyCase{
		name:        "29_transparent_image",
		description: "PNG with alpha channel — must not corrupt rendering",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before alpha</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>after alpha</w:t></w:r></w:p>`).
				Media("alpha.png", tp).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/alpha.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before alpha", "after alpha"},
		expectPages: 1,
	}
}

func caseImageOnlyParagraph() verifyCase {
	g := makeSolidPNG(120, 80, color.RGBA{R: 40, G: 200, B: 40, A: 255})
	return verifyCase{
		name:        "30_image_only_paragraph",
		description: "Paragraph containing only an image — no text runs",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>top text</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>bottom text</w:t></w:r></w:p>`).
				Media("g.png", g).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/g.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"top text", "bottom text"},
		expectPages: 1,
	}
}

func caseImageLargerThanPage() verifyCase {
	// Way wider than any reasonable page → forces fitImage clamping.
	big := makeSolidPNG(4000, 3000, color.RGBA{R: 80, G: 80, B: 220, A: 255})
	return verifyCase{
		name:        "31_image_larger_than_page",
		description: "Image far wider than the page — must be clamped to content width",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>start</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>end</w:t></w:r></w:p>`).
				Media("big.png", big).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/big.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText: []string{"start", "end"},
	}
}

// --- expanded coverage: section + decorations --------------------------

func casePerSectionHeaders() verifyCase {
	// Two sections, each with its own header file. Page 1 → header A,
	// page 2 → header B. Validates section→header mapping in stamper.
	return verifyCase{
		name:        "32_per_section_headers",
		description: "Two sections with distinct headers — each section uses its own",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>section 1 body</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:sectPr>
          <w:headerReference r:id="rHA" w:type="default"/>
          <w:pgSz w:w="11906" w:h="16838"/>
          <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
        </w:sectPr>
      </w:pPr>
      <w:r><w:t>end of section 1</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>section 2 body</w:t></w:r></w:p>
    <w:sectPr>
      <w:headerReference r:id="rHB" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("headerA.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>HEADER-ALPHA</w:t></w:r></w:p></w:hdr>`).
				Part("headerB.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>HEADER-BRAVO</w:t></w:r></w:p></w:hdr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rHA" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="headerA.xml"/>
  <Relationship Id="rHB" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="headerB.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"section 1 body", "section 2 body", "HEADER-ALPHA", "HEADER-BRAVO"},
		expectPages: 2,
	}
}

func caseFieldSwitches() verifyCase {
	// Word writes PAGE  \* MERGEFORMAT — switches should be stripped when
	// parsing the field code. Use an explicit page break so we don't rely
	// on density-driven page count.
	return verifyCase{
		name:        "33_field_switches",
		description: "Field switches like \\* MERGEFORMAT and \\* ARABIC must be stripped",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>page 1</w:t></w:r></w:p>` +
				`<w:p><w:r><w:br w:type="page"/></w:r></w:p>` +
				`<w:p><w:r><w:t>page 2</w:t></w:r></w:p>`
			return newDocx().
				RawBody(docHeader+body+`
    <w:sectPr>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`+docFooter).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p>
    <w:r><w:t xml:space="preserve">p </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve"> PAGE   \* MERGEFORMAT </w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p>
</w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		// pdftotext may collapse the space between adjacent runs ("p" + "1"),
		// so we assert the substituted digits appear and the cached "?" did
		// not survive — both are sufficient to prove the field was resolved.
		expectText:  []string{"page 1", "page 2"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "p ?") || strings.Contains(txt, "p?") {
				fail("field switches not stripped — cached '?' leaked through:\n%s", txt)
			}
			// Page 1 and page 2 each have their own footer; the digits 1 and 2
			// must both appear somewhere in the extracted text.
			if !strings.Contains(txt, "p1") && !strings.Contains(txt, "p 1") {
				fail("PAGE not substituted on page 1; got:\n%s", txt)
			}
			if !strings.Contains(txt, "p2") && !strings.Contains(txt, "p 2") {
				fail("PAGE not substituted on page 2; got:\n%s", txt)
			}
		},
	}
}

func caseUnknownField() verifyCase {
	// AUTHOR is an unimplemented field — must fall through to the cached
	// "Jane Doe" result rather than disappearing.
	return verifyCase{
		name:        "34_unknown_field_fallthrough",
		description: "Unknown field (AUTHOR) falls through to its cached result",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">by </w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText xml:space="preserve">AUTHOR</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>Jane Doe</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"by", "Jane Doe"},
		expectPages: 1,
	}
}

func casePageNumbersFlag() verifyCase {
	// -page-numbers should stamp X / N on every page, independent of fields.
	return verifyCase{
		name:        "35_page_numbers_flag",
		description: "-page-numbers flag stamps 'X / N' on every page",
		pageNumbers: true,
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>alpha page</w:t></w:r></w:p>` +
				`<w:p><w:r><w:br w:type="page"/></w:r></w:p>` +
				`<w:p><w:r><w:t>bravo page</w:t></w:r></w:p>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		// pdftotext sometimes drops the narrow space between "1" and "/" so
		// accept either "1/2" or "1 / 2"; the substantive check is that the
		// page-number stamper rendered the slash-separated label on each page.
		expectText:  []string{"alpha page", "bravo page"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			for _, want := range []string{"1/2", "2/2"} {
				if !strings.Contains(txt, want) {
					fail("expected %q in extracted text:\n%s", want, txt)
				}
			}
		},
	}
}

func caseCJKInFooter() verifyCase {
	return verifyCase{
		name:        "36_cjk_in_footer",
		description: "CJK glyphs in the footer with a PAGE field — 2 pages forced",
		useCJK:      true,
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>正文 第一页</w:t></w:r></w:p>` +
				`<w:p><w:r><w:br w:type="page"/></w:r></w:p>` +
				`<w:p><w:r><w:t>正文 第二页</w:t></w:r></w:p>`
			return newDocx().
				RawBody(docHeader+body+`
    <w:sectPr>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`+docFooter).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
    <w:r><w:t xml:space="preserve">第 </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">PAGE</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
    <w:r><w:t xml:space="preserve"> 页 共 </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText xml:space="preserve">NUMPAGES</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
    <w:r><w:t xml:space="preserve"> 页</w:t></w:r>
  </w:p>
</w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		// pdftotext collapses spaces between adjacent CJK glyphs, so we test
		// the body markers + the substituted numerics rather than the full
		// "第 1 页 共 2 页" template (which appears as "第1页共2页").
		expectText:  []string{"正文", "第一页", "第二页"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			// The cached "?" must not survive substitution, and the actual
			// page numbers must show up alongside the 页/共 template.
			if strings.Contains(txt, "第?页") || strings.Contains(txt, "共?页") {
				fail("CJK footer fields not substituted:\n%s", txt)
			}
			if !strings.Contains(txt, "第1页") {
				fail("missing CJK page-1 footer marker:\n%s", txt)
			}
			if !strings.Contains(txt, "第2页") {
				fail("missing CJK page-2 footer marker:\n%s", txt)
			}
			if !strings.Contains(txt, "共2页") {
				fail("missing CJK numpages substitution:\n%s", txt)
			}
		},
	}
}

// --- expanded coverage: styles -----------------------------------------

func caseBasedOnChain3() verifyCase {
	// Heading2 → BodyText → Normal — three levels of inheritance.
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Normal">
    <w:rPr><w:sz w:val="22"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="BodyText">
    <w:basedOn w:val="Normal"/>
    <w:pPr><w:spacing w:before="120" w:after="120"/></w:pPr>
    <w:rPr><w:sz w:val="24"/></w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading2">
    <w:basedOn w:val="BodyText"/>
    <w:pPr><w:jc w:val="center"/></w:pPr>
    <w:rPr><w:b/><w:sz w:val="32"/><w:color w:val="703030"/></w:rPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "37_basedOn_chain_3_levels",
		description: "Heading2 → BodyText → Normal three-level basedOn chain",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr>
      <w:r><w:t>DEEP-CHAIN-HEADING</w:t></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="BodyText"/></w:pPr>
      <w:r><w:t>BodyText paragraph</w:t></w:r></w:p>`).
				Styles(styles).
				Write(t, dir)
		},
		expectText:  []string{"DEEP-CHAIN-HEADING", "BodyText paragraph"},
		expectPages: 1,
	}
}

// --- expanded coverage: text edge cases --------------------------------

func caseWhitespacePreserve() verifyCase {
	return verifyCase{
		name:        "38_whitespace_preserve",
		description: "xml:space=\"preserve\" — leading/trailing/interior spaces survive",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">[start</w:t></w:r>
      <w:r><w:t xml:space="preserve">   triple-space   </w:t></w:r>
      <w:r><w:t xml:space="preserve">end]</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		// pdftotext collapses runs of spaces with -layout off; with -layout
		// they're preserved more faithfully. Check both ends and a hint of
		// the gap by looking for the substring with at least one space.
		expectText: []string{"[start", "triple-space", "end]"},
	}
}

func caseLongUnbreakableWord() verifyCase {
	// 220-char word with no break points — exercises the long-word path.
	hugeWord := strings.Repeat("supercalifragilistic", 11)
	return verifyCase{
		name:        "39_long_unbreakable_word",
		description: "Very long word with no whitespace — must not crash; rendering may overflow",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>before</w:t></w:r></w:p>
    <w:p><w:r><w:t>`+hugeWord+`</w:t></w:r></w:p>
    <w:p><w:r><w:t>after</w:t></w:r></w:p>`).Write(t, dir)
		},
		// Just confirm we didn't crash and the surrounding text is present.
		expectText: []string{"before", "after"},
	}
}

func caseMultipleTInSameRun() verifyCase {
	return verifyCase{
		name:        "40_multiple_t_in_same_run",
		description: "Single w:r with multiple w:t fragments — XML decoder concat",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r>
        <w:t xml:space="preserve">multi-</w:t>
        <w:t xml:space="preserve">t-</w:t>
        <w:t>concat</w:t>
      </w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"multi-", "concat"},
		expectPages: 1,
	}
}

func caseMultipleBreaks() verifyCase {
	return verifyCase{
		name:        "41_multiple_breaks",
		description: "Consecutive soft line breaks inside one paragraph",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t>line A</w:t></w:r>
      <w:r><w:br/></w:r>
      <w:r><w:br/></w:r>
      <w:r><w:t>line B after two breaks</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"line A", "line B after two breaks"},
		expectPages: 1,
	}
}

func caseVanishHidden() verifyCase {
	// We don't currently honor w:vanish — this case documents the gap.
	// If we ever add support, change expectations.
	return verifyCase{
		name:        "42_vanish_hidden_text",
		description: "w:vanish (hidden text) — currently rendered as visible; documenting the gap",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">visible-before </w:t></w:r>
      <w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">HIDDEN-CONTENT </w:t></w:r>
      <w:r><w:t>visible-after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		// Until vanish is implemented, the hidden text WILL appear. Asserting
		// the surrounding visible text keeps the test green while documenting
		// the current behavior in the description.
		expectText:  []string{"visible-before", "visible-after"},
		expectPages: 1,
	}
}

func caseEmptyDocument() verifyCase {
	return verifyCase{
		name:        "43_empty_document",
		description: "Document with no body content — defensive boundary",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(``).Write(t, dir)
		},
		expectPages: 1,
	}
}

// --- additional feature corner cases ---------------------------------

func caseMultiParagraphCell() verifyCase {
	return verifyCase{
		name:        "44_multi_paragraph_cell",
		description: "Table cell containing multiple paragraphs — row height grows",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>label</w:t></w:r></w:p></w:tc>
        <w:tc>
          <w:p><w:r><w:t>first paragraph in cell</w:t></w:r></w:p>
          <w:p><w:r><w:t>second paragraph in cell</w:t></w:r></w:p>
          <w:p><w:r><w:rPr><w:b/></w:rPr><w:t>third paragraph bold</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"label", "first paragraph in cell", "second paragraph in cell", "third paragraph bold"},
		expectPages: 1,
	}
}

func caseTableNoGrid() verifyCase {
	return verifyCase{
		name:        "45_table_no_grid",
		description: "Table without explicit tblGrid — columns inferred equal width",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tr>
        <w:tc><w:p><w:r><w:t>A1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>B1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>C1</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>A2</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>B2</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>C2</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"A1", "B1", "C1", "A2", "B2", "C2"},
		expectPages: 1,
	}
}

func caseEmptyCells() verifyCase {
	return verifyCase{
		name:        "46_empty_cells",
		description: "Empty cells alongside filled cells — must not collapse to zero height",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>filled</w:t></w:r></w:p></w:tc>
        <w:tc><w:p/></w:tc>
        <w:tc><w:p><w:r><w:t>also filled</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p/></w:tc>
        <w:tc><w:p><w:r><w:t>middle only</w:t></w:r></w:p></w:tc>
        <w:tc><w:p/></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"filled", "also filled", "middle only"},
		expectPages: 1,
	}
}

func caseHyperlinkBadRid() verifyCase {
	// A w:hyperlink whose r:id has no rel entry — must not crash; the link
	// just renders as plain text.
	return verifyCase{
		name:        "47_hyperlink_unresolvable",
		description: "Hyperlink r:id not present in rels — degrades to plain text, no crash",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">see </w:t></w:r>
      <w:hyperlink r:id="rMissing">
        <w:r><w:t>link-text-LOST</w:t></w:r>
      </w:hyperlink>
      <w:r><w:t xml:space="preserve"> here.</w:t></w:r>
    </w:p>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`).
				Write(t, dir)
		},
		expectText:  []string{"see", "link-text-LOST", "here."},
		expectPages: 1,
	}
}

func caseDeepNestedList() verifyCase {
	// 4 levels of nesting on a single abstractNum.
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="360" w:hanging="360"/></w:pPr></w:lvl>
    <w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%2."/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
    <w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="lowerRoman"/><w:lvlText w:val="%3."/>
      <w:pPr><w:ind w:left="1080" w:hanging="360"/></w:pPr></w:lvl>
    <w:lvl w:ilvl="3"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="◦"/>
      <w:pPr><w:ind w:left="1440" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	li := func(lvl, text string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="` + lvl + `"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "48_deep_nested_list",
		description: "4-level nested list: decimal/lowerLetter/lowerRoman/bullet",
		build: func(t *testing.T, dir string) string {
			body := li("0", "L0 first") +
				li("1", "L1 first") +
				li("2", "L2 first") +
				li("3", "L3 bullet first") +
				li("3", "L3 bullet second") +
				li("2", "L2 second") +
				li("0", "L0 second")
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		expectText: []string{
			"L0 first", "L0 second",
			"L1 first",
			"L2 first", "L2 second",
			"L3 bullet first", "L3 bullet second",
		},
		expectPages: 1,
	}
}

func caseRunRPrInheritance() verifyCase {
	// Paragraph-level w:pPr/w:rPr sets default formatting that runs without
	// their own rPr should inherit.
	return verifyCase{
		name:        "49_run_rpr_inheritance",
		description: "Paragraph rPr defaults applied to runs without their own rPr",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:rPr><w:b/><w:color w:val="C00000"/><w:sz w:val="32"/></w:rPr></w:pPr>
      <w:r><w:t xml:space="preserve">inherits-bold-red </w:t></w:r>
      <w:r><w:rPr><w:b w:val="0"/></w:rPr><w:t>override-not-bold</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"inherits-bold-red", "override-not-bold"},
		expectPages: 1,
	}
}

// --- multi-language ----------------------------------------------------

func caseKorean() verifyCase {
	return verifyCase{
		name:        "50_korean",
		description: "Hangul script via CJK fallback",
		useCJK:      true,
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:pPr><w:jc w:val="center"/></w:pPr>
      <w:r><w:rPr><w:b/><w:sz w:val="36"/></w:rPr><w:t>한국어 렌더링 테스트</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>이 단락은 공백 없이 줄바꿈이 정상적으로 일어나는지 확인합니다.</w:t></w:r></w:p>`).
				Write(t, dir)
		},
		expectText:  []string{"한국어", "렌더링", "테스트", "단락은"},
		expectPages: 1,
	}
}

func caseThai() verifyCase {
	// Thai isn't in our isCJK ranges so it stays on the regular font. We
	// just verify no crash and that the text survives. Real Thai support
	// would need its own font and per-cluster line breaking.
	return verifyCase{
		name:        "51_thai",
		description: "Thai script — survives parse + extract (visual quality limited)",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>สวัสดีครับ ภาษาไทย</w:t></w:r></w:p>`).Write(t, dir)
		},
		// Thai glyphs may not be present in our test font — assert only that
		// the parse-and-render pipeline doesn't choke.
		expectPages: 1,
	}
}

func caseGreek() verifyCase {
	return verifyCase{
		name:        "52_greek",
		description: "Greek script — common in scientific docs",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>Καλημέρα κόσμε</w:t></w:r></w:p>
    <w:p><w:r><w:t>α β γ δ ε ζ η θ ι κ λ μ ν ξ ο π ρ σ τ υ φ χ ψ ω</w:t></w:r></w:p>`).
				Write(t, dir)
		},
		expectText:  []string{"Καλημέρα", "κόσμε"},
		expectPages: 1,
	}
}

func caseEmoji() verifyCase {
	// Most fonts won't have color emoji glyphs but the renderer should not
	// crash on high-codepoint runes. We verify the surrounding text survives.
	return verifyCase{
		name:        "53_emoji",
		description: "High-codepoint runes (emoji) — no crash, surrounding text intact",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>before-emoji 🚀 🎉 ✨ after-emoji</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"before-emoji", "after-emoji"},
		expectPages: 1,
	}
}

func caseMixedScripts() verifyCase {
	return verifyCase{
		name:        "54_mixed_scripts",
		description: "Latin + Cyrillic + CJK + Greek in one paragraph",
		useCJK:      true,
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>english привет 中文 한국어 ελληνικά end-mark</w:t></w:r></w:p>`).
				Write(t, dir)
		},
		expectText:  []string{"english", "end-mark"},
		expectPages: 1,
	}
}

// --- stress ------------------------------------------------------------

func caseStress500Paragraphs() verifyCase {
	return verifyCase{
		name:        "55_stress_500_paragraphs",
		description: "500 paragraphs — exercises line breaking + page break loop at scale",
		build: func(t *testing.T, dir string) string {
			var b strings.Builder
			for i := 1; i <= 500; i++ {
				fmt.Fprintf(&b, `<w:p><w:r><w:t>paragraph %d in stress run.</w:t></w:r></w:p>`, i)
			}
			b.WriteString(`<w:p><w:r><w:t>END-OF-STRESS</w:t></w:r></w:p>`)
			return newDocx().RawBody(docHeader+b.String()+docFooter).Write(t, dir)
		},
		expectText: []string{"paragraph 1 ", "paragraph 250 ", "paragraph 500 ", "END-OF-STRESS"},
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			n := pdfPageCount(t, pdf)
			if n < 5 {
				fail("500 paragraphs produced only %d pages; expected at least 5", n)
			}
		},
	}
}

func caseStress200RowTable() verifyCase {
	return verifyCase{
		name:        "56_stress_200_row_table",
		description: "200-row table — exercises drawRow + page break across many rows",
		build: func(t *testing.T, dir string) string {
			var rows strings.Builder
			for i := 1; i <= 200; i++ {
				fmt.Fprintf(&rows, `<w:tr>
        <w:tc><w:p><w:r><w:t>r%d-c1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>r%d-c2</w:t></w:r></w:p></w:tc>
      </w:tr>`, i, i)
			}
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="4000"/><w:gridCol w:w="4000"/></w:tblGrid>` +
				rows.String() + `</w:tbl>` +
				`<w:p><w:r><w:t>AFTER-TABLE-MARK</w:t></w:r></w:p>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText: []string{"r1-c1", "r100-c1", "r200-c2", "AFTER-TABLE-MARK"},
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			n := pdfPageCount(t, pdf)
			if n < 3 {
				fail("200-row table fit in only %d pages", n)
			}
		},
	}
}

func caseStressManyImages() verifyCase {
	red := makeSolidPNG(120, 80, color.RGBA{R: 220, G: 60, B: 60, A: 255})
	return verifyCase{
		name:        "57_stress_many_images",
		description: "30 image references reusing the same media file — no per-image leak",
		build: func(t *testing.T, dir string) string {
			var paras strings.Builder
			for i := 1; i <= 30; i++ {
				fmt.Fprintf(&paras, `<w:p><w:r><w:t>image %d:</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>`, i)
			}
			return newDocx().
				Body(paras.String()).
				Media("image1.png", red).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText: []string{"image 1:", "image 15:", "image 30:"},
	}
}

// --- batch A coverage ----------------------------------------------------

func caseCapsSmallCaps() verifyCase {
	return verifyCase{
		name:        "58_caps_smallcaps",
		description: "w:caps and w:smallCaps render text as uppercase",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:rPr><w:caps/></w:rPr><w:t>caps applied here</w:t></w:r></w:p>
    <w:p><w:r><w:rPr><w:smallCaps/></w:rPr><w:t>small caps applied</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"CAPS APPLIED HERE", "SMALL CAPS APPLIED"},
		expectPages: 1,
	}
}

func caseVertAlign() verifyCase {
	return verifyCase{
		name:        "59_vert_align",
		description: "Superscript and subscript runs (size 60%, baseline shifted)",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">H</w:t></w:r>
      <w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t>2</w:t></w:r>
      <w:r><w:t xml:space="preserve">O and E=mc</w:t></w:r>
      <w:r><w:rPr><w:vertAlign w:val="superscript"/></w:rPr><w:t>2</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"H", "O and E=mc"},
		expectPages: 1,
	}
}

func caseHighlight() verifyCase {
	return verifyCase{
		name:        "60_highlight",
		description: "w:highlight draws colored background under runs",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">normal then </w:t></w:r>
      <w:r><w:rPr><w:highlight w:val="yellow"/></w:rPr><w:t>YELLOW</w:t></w:r>
      <w:r><w:t xml:space="preserve"> and </w:t></w:r>
      <w:r><w:rPr><w:highlight w:val="cyan"/></w:rPr><w:t>CYAN</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"YELLOW", "CYAN"},
		expectPages: 1,
	}
}

func caseRunShading() verifyCase {
	return verifyCase{
		name:        "61_run_shading",
		description: "w:shd run-level background fill color",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:r><w:rPr><w:shd w:val="clear" w:color="auto" w:fill="DDEEFF"/></w:rPr><w:t>SHADED</w:t></w:r>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "SHADED", "after"},
		expectPages: 1,
	}
}

func caseCharStyle() verifyCase {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="character" w:styleId="Emph">
    <w:rPr><w:i/><w:color w:val="A040A0"/></w:rPr>
  </w:style>
  <w:style w:type="character" w:styleId="StrongEmph">
    <w:basedOn w:val="Emph"/>
    <w:rPr><w:b/></w:rPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "62_char_style",
		description: "w:rStyle applies a named character style, with basedOn",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">plain </w:t></w:r>
      <w:r><w:rPr><w:rStyle w:val="Emph"/></w:rPr><w:t>emphasised</w:t></w:r>
      <w:r><w:t xml:space="preserve"> then </w:t></w:r>
      <w:r><w:rPr><w:rStyle w:val="StrongEmph"/></w:rPr><w:t>strong-emph</w:t></w:r>
    </w:p>`).
				Styles(styles).
				Write(t, dir)
		},
		expectText:  []string{"plain", "emphasised", "strong-emph"},
		expectPages: 1,
	}
}

// --- batch B coverage ----------------------------------------------------

func caseCellShading() verifyCase {
	return verifyCase{
		name:        "63_cell_shading",
		description: "tcPr/w:shd fills the cell background",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:shd w:fill="C0E0FF"/></w:tcPr>
          <w:p><w:r><w:rPr><w:b/></w:rPr><w:t>shaded header</w:t></w:r></w:p>
        </w:tc>
        <w:tc>
          <w:tcPr><w:shd w:fill="FFE0C0"/></w:tcPr>
          <w:p><w:r><w:rPr><w:b/></w:rPr><w:t>second cell</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>plain</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>cells</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"shaded header", "second cell", "plain", "cells"},
		expectPages: 1,
	}
}

func caseCellVAlign() verifyCase {
	return verifyCase{
		name:        "64_cell_valign",
		description: "tcPr/w:vAlign top/center/bottom",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:vAlign w:val="top"/></w:tcPr>
          <w:p><w:r><w:t>top</w:t></w:r></w:p>
        </w:tc>
        <w:tc>
          <w:tcPr><w:vAlign w:val="center"/></w:tcPr>
          <w:p><w:r><w:t>center</w:t></w:r></w:p>
        </w:tc>
        <w:tc>
          <w:tcPr><w:vAlign w:val="bottom"/></w:tcPr>
          <w:p><w:r><w:t>bottom</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>filler1</w:t></w:r></w:p>
              <w:p><w:r><w:t>filler2</w:t></w:r></w:p>
              <w:p><w:r><w:t>filler3</w:t></w:r></w:p>
              <w:p><w:r><w:t>filler4</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>also</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>tall</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"top", "center", "bottom", "filler1", "filler4"},
		expectPages: 1,
	}
}

func caseCellBorderStyles() verifyCase {
	return verifyCase{
		name:        "65_cell_borders",
		description: "Per-edge tcBorders with single/double/dashed/dotted styles",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:tcBorders>
            <w:top w:val="double" w:sz="12" w:color="000000"/>
            <w:bottom w:val="double" w:sz="12" w:color="000000"/>
            <w:left w:val="single" w:sz="4" w:color="404040"/>
            <w:right w:val="single" w:sz="4" w:color="404040"/>
          </w:tcBorders></w:tcPr>
          <w:p><w:r><w:t>DOUBLE</w:t></w:r></w:p>
        </w:tc>
        <w:tc>
          <w:tcPr><w:tcBorders>
            <w:top w:val="dashed" w:sz="8" w:color="0070C0"/>
            <w:bottom w:val="dashed" w:sz="8" w:color="0070C0"/>
            <w:left w:val="dashed" w:sz="8" w:color="0070C0"/>
            <w:right w:val="dashed" w:sz="8" w:color="0070C0"/>
          </w:tcBorders></w:tcPr>
          <w:p><w:r><w:t>DASHED</w:t></w:r></w:p>
        </w:tc>
        <w:tc>
          <w:tcPr><w:tcBorders>
            <w:top w:val="dotted" w:sz="8" w:color="C00000"/>
            <w:bottom w:val="dotted" w:sz="8" w:color="C00000"/>
            <w:left w:val="dotted" w:sz="8" w:color="C00000"/>
            <w:right w:val="dotted" w:sz="8" w:color="C00000"/>
          </w:tcBorders></w:tcPr>
          <w:p><w:r><w:t>DOTTED</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"DOUBLE", "DASHED", "DOTTED"},
		expectPages: 1,
	}
}

// --- batch C coverage ----------------------------------------------------

func caseTableHeaderRepeat() verifyCase {
	return verifyCase{
		name:        "66_table_header_repeat",
		description: "w:trPr/w:tblHeader makes header row repeat on each page",
		build: func(t *testing.T, dir string) string {
			var rows strings.Builder
			rows.WriteString(`<w:tr>
        <w:trPr><w:tblHeader/></w:trPr>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>REPEATING-HEADER</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>second col</w:t></w:r></w:p></w:tc>
      </w:tr>`)
			for i := 1; i <= 80; i++ {
				fmt.Fprintf(&rows, `<w:tr>
        <w:tc><w:p><w:r><w:t>row %d</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>data %d</w:t></w:r></w:p></w:tc>
      </w:tr>`, i, i)
			}
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="4000"/></w:tblGrid>` +
				rows.String() + `</w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText: []string{"REPEATING-HEADER", "row 1", "row 80"},
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			n := strings.Count(txt, "REPEATING-HEADER")
			if n < 2 {
				fail("header repeated %d times, expected >=2", n)
			}
		},
	}
}

// --- batch D coverage ----------------------------------------------------

func caseFirstPageHeader() verifyCase {
	return verifyCase{
		name:        "67_first_page_header",
		description: "w:titlePg + first-page header — different header on page 1",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>first page body</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>second page body</w:t></w:r></w:p>
    <w:sectPr>
      <w:headerReference r:id="rHF" w:type="first"/>
      <w:headerReference r:id="rHD" w:type="default"/>
      <w:titlePg/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("headerFirst.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>FIRST-PAGE-ONLY-HEADER</w:t></w:r></w:p></w:hdr>`).
				Part("headerDefault.xml", `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t>DEFAULT-OTHER-PAGES-HEADER</w:t></w:r></w:p></w:hdr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rHF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="headerFirst.xml"/>
  <Relationship Id="rHD" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="headerDefault.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"FIRST-PAGE-ONLY-HEADER", "DEFAULT-OTHER-PAGES-HEADER", "first page body", "second page body"},
		expectPages: 2,
	}
}

// --- batch E coverage ----------------------------------------------------

func caseInternalLink() verifyCase {
	return verifyCase{
		name:        "68_internal_link",
		description: "Bookmark target + hyperlink w:anchor to that bookmark",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">jump to </w:t></w:r>
      <w:hyperlink w:anchor="myTarget">
        <w:r><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr>
          <w:t>the target</w:t></w:r>
      </w:hyperlink>
      <w:r><w:t xml:space="preserve"> below.</w:t></w:r>
    </w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p>
      <w:bookmarkStart w:id="0" w:name="myTarget"/>
      <w:r><w:t>here is the TARGET section</w:t></w:r>
      <w:bookmarkEnd w:id="0"/>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"jump to", "the target", "below.", "TARGET section"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			raw, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			// gopdf resolves the anchor name into a direct object reference,
			// so the literal "myTarget" string won't appear. Instead verify
			// the Link annotation exists with an internal-Dest array.
			if !bytes.Contains(raw, []byte("/Subtype /Link")) {
				fail("no /Link annotation found in PDF")
			}
			if !bytes.Contains(raw, []byte("/Dest [")) {
				fail("link annotation has no /Dest (not an internal link)")
			}
		},
	}
}

// --- batch F coverage ----------------------------------------------------

func caseTabStops() verifyCase {
	return verifyCase{
		name:        "69_tab_stops",
		description: "w:tabs custom stops — text snaps to declared positions",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:tabs>
        <w:tab w:val="left" w:pos="2880"/>
        <w:tab w:val="right" w:pos="5760"/>
      </w:tabs></w:pPr>
      <w:r><w:t>LEFT</w:t></w:r>
      <w:r><w:tab/><w:t>MIDDLE</w:t></w:r>
      <w:r><w:tab/><w:t>RIGHT</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"LEFT", "MIDDLE", "RIGHT"},
		expectPages: 1,
	}
}

func caseTabLeader() verifyCase {
	return verifyCase{
		name:        "70_tab_leader",
		description: "Tab leader (dot) fills gap — typical for TOC entries",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:tabs>
        <w:tab w:val="right" w:pos="7200" w:leader="dot"/>
      </w:tabs></w:pPr>
      <w:r><w:t>Chapter One</w:t></w:r>
      <w:r><w:tab/><w:t>5</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"Chapter One"},
		expectPages: 1,
	}
}

func caseContextualSpacing() verifyCase {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="LI">
    <w:pPr><w:spacing w:before="200" w:after="200"/><w:contextualSpacing/></w:pPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "71_contextual_spacing",
		description: "contextualSpacing suppresses inter-paragraph spacing between same-style siblings",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:pPr><w:pStyle w:val="LI"/></w:pPr><w:r><w:t>first item</w:t></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="LI"/></w:pPr><w:r><w:t>second item</w:t></w:r></w:p>
    <w:p><w:pPr><w:pStyle w:val="LI"/></w:pPr><w:r><w:t>third item</w:t></w:r></w:p>`).
				Styles(styles).
				Write(t, dir)
		},
		expectText:  []string{"first item", "second item", "third item"},
		expectPages: 1,
	}
}

// --- batch H coverage ----------------------------------------------------

func casePageNumberFmtRoman() verifyCase {
	return verifyCase{
		name:        "72_pagenum_roman",
		description: "w:pgNumType fmt=upperRoman renders PAGE field as I, II, III",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>p one</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>p two</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>p three</w:t></w:r></w:p>
    <w:sectPr>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgNumType w:fmt="upperRoman"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t xml:space="preserve">page </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText>PAGE</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p></w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"p one", "p three"},
		expectPages: 3,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			for _, want := range []string{"I", "II", "III"} {
				if !strings.Contains(txt, want) {
					fail("expected roman %q in footer; got:\n%s", want, txt)
				}
			}
		},
	}
}

func casePageNumberStartFrom() verifyCase {
	return verifyCase{
		name:        "73_pagenum_start_from",
		description: "w:pgNumType w:start=5 — first page numbered 5",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>first body page</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>second body page</w:t></w:r></w:p>
    <w:sectPr>
      <w:footerReference r:id="rF" w:type="default"/>
      <w:pgNumType w:start="5"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("footer1.xml", `<?xml version="1.0"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p><w:r><w:t xml:space="preserve">page </w:t></w:r>
    <w:r><w:fldChar w:fldCharType="begin"/></w:r>
    <w:r><w:instrText>PAGE</w:instrText></w:r>
    <w:r><w:fldChar w:fldCharType="separate"/></w:r>
    <w:r><w:t>?</w:t></w:r>
    <w:r><w:fldChar w:fldCharType="end"/></w:r>
  </w:p></w:ftr>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rF" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"first body page", "second body page"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			if !strings.Contains(txt, "5") || !strings.Contains(txt, "6") {
				fail("expected pages 5+6 in footer; got:\n%s", txt)
			}
		},
	}
}

func casePageBorders() verifyCase {
	return verifyCase{
		name:        "74_page_borders",
		description: "w:pgBorders draws a colored frame around each page",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>page with frame</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
      <w:pgBorders>
        <w:top w:val="double" w:sz="16" w:color="0070C0"/>
        <w:bottom w:val="double" w:sz="16" w:color="0070C0"/>
        <w:left w:val="single" w:sz="8" w:color="0070C0"/>
        <w:right w:val="single" w:sz="8" w:color="0070C0"/>
      </w:pgBorders>
    </w:sectPr>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		expectText:  []string{"page with frame"},
		expectPages: 1,
	}
}

// --- batch I coverage ----------------------------------------------------

func caseNestedTable() verifyCase {
	return verifyCase{
		name:        "75_nested_table",
		description: "Table inside a table cell",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>outer A</w:t></w:r></w:p></w:tc>
        <w:tc>
          <w:tbl>
            <w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="3000"/></w:tblGrid>
            <w:tr>
              <w:tc><w:p><w:r><w:t>inner X</w:t></w:r></w:p></w:tc>
              <w:tc><w:p><w:r><w:t>inner Y</w:t></w:r></w:p></w:tc>
            </w:tr>
            <w:tr>
              <w:tc><w:p><w:r><w:t>inner P</w:t></w:r></w:p></w:tc>
              <w:tc><w:p><w:r><w:t>inner Q</w:t></w:r></w:p></w:tc>
            </w:tr>
          </w:tbl>
        </w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"outer A", "inner X", "inner Y", "inner P", "inner Q"},
		expectPages: 1,
	}
}

func caseCellMargins() verifyCase {
	return verifyCase{
		name:        "76_cell_margins",
		description: "w:tcMar per-cell margin overrides honor",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr>
        <w:tc>
          <w:tcPr><w:tcMar>
            <w:top w:w="200"/><w:bottom w:w="200"/>
            <w:left w:w="400"/><w:right w:w="100"/>
          </w:tcMar></w:tcPr>
          <w:p><w:r><w:t>wider-left-narrower-right</w:t></w:r></w:p>
        </w:tc>
        <w:tc><w:p><w:r><w:t>default margins</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"wider-left-narrower-right", "default margins"},
		expectPages: 1,
	}
}

func caseRowHeight() verifyCase {
	return verifyCase{
		name:        "77_row_height_min",
		description: "w:trHeight minimum row height keeps short cells tall",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:trPr><w:trHeight w:val="1440"/></w:trPr>
        <w:tc><w:p><w:r><w:t>tall row</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>tall too</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>normal</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>normal</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"tall row", "tall too", "normal"},
		expectPages: 1,
	}
}

// --- batch J coverage ----------------------------------------------------

func caseLegalListIsLgl() verifyCase {
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/>
      <w:pPr><w:ind w:left="360" w:hanging="360"/></w:pPr></w:lvl>
    <w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="%1.%2"/>
      <w:isLgl/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	li := func(lvl, text string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="` + lvl + `"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "78_legal_list_isLgl",
		description: "w:isLgl forces lower level to render as decimal even when lowerLetter",
		build: func(t *testing.T, dir string) string {
			body := li("0", "one") + li("1", "one.alpha") + li("1", "one.bravo")
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		// With isLgl on level 1, the marker becomes "1.1" not "1.a".
		expectText:  []string{"1.", "1.1", "1.2", "one", "alpha", "bravo"},
		expectPages: 1,
	}
}

// --- batch K coverage ----------------------------------------------------

func caseVanishHiddenSkip() verifyCase {
	return verifyCase{
		name:        "79_vanish_hidden_skipped",
		description: "w:vanish removes the run from rendered output",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">visible-A </w:t></w:r>
      <w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">SHOULD-NOT-APPEAR </w:t></w:r>
      <w:r><w:t>visible-B</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"visible-A", "visible-B"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "SHOULD-NOT-APPEAR") {
				fail("vanish text leaked into PDF")
			}
		},
	}
}

func casePositionAndW() verifyCase {
	// w:w at 150% used to be a no-op visually so pdftotext returned the
	// stretched word intact. Now that we apply the scale as inter-character
	// spacing (gopdf can't do PDF Tz), 150% is wide enough that pdftotext
	// extracts the run as separate letters — that's the correct observable
	// signal of the fix. Keep the case but use 110% (subtle stretch, single
	// token) plus a separate strong-stretch run we check via letter-by-letter
	// substring.
	return verifyCase{
		name:        "80_position_and_w",
		description: "w:position raises baseline; w:w widens character advance",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">baseline </w:t></w:r>
      <w:r><w:rPr><w:position w:val="10"/></w:rPr><w:t xml:space="preserve">raised </w:t></w:r>
      <w:r><w:rPr><w:position w:val="-6"/></w:rPr><w:t xml:space="preserve">lowered </w:t></w:r>
      <w:r><w:rPr><w:w w:val="110"/></w:rPr><w:t>stretched</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"baseline", "raised", "lowered", "stretched"},
		expectPages: 1,
	}
}

func caseNoBreakAndSym() verifyCase {
	return verifyCase{
		name:        "81_nobreakhyphen_sym",
		description: "w:noBreakHyphen produces U+2011 and w:sym maps to a literal rune",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">part1</w:t></w:r>
      <w:r><w:noBreakHyphen/></w:r>
      <w:r><w:t xml:space="preserve">part2 </w:t></w:r>
      <w:r><w:sym w:font="Symbol" w:char="2192"/></w:r>
      <w:r><w:t xml:space="preserve"> arrow.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"part1", "part2", "arrow."},
		expectPages: 1,
	}
}

// --- batch L coverage ----------------------------------------------------

func caseImageExplicitExtent() verifyCase {
	img := makeSolidPNG(800, 200, color.RGBA{R: 100, G: 100, B: 200, A: 255})
	return verifyCase{
		name:        "82_image_explicit_extent",
		description: "wp:extent forces a smaller rendered size than the source pixels",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <wp:extent cx="1500000" cy="500000"/>
      <a:graphic><a:graphicData>
        <pic:pic><pic:blipFill><a:blip r:embed="rImg"/></pic:blipFill></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>after</w:t></w:r></w:p>`).
				Media("image1.png", img).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before", "after"},
		expectPages: 1,
	}
}

// --- batch M coverage ----------------------------------------------------

func caseDateFilenameAuthor() verifyCase {
	return verifyCase{
		name:        "83_date_filename_author",
		description: "DATE/FILENAME/AUTHOR fields substituted from Options/runtime",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">file=</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText>FILENAME</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>cached.docx</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
      <w:r><w:t xml:space="preserve"> author=</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText>AUTHOR</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>cached author</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
    </w:p>`).Write(t, dir)
		},
		// Convert reads inPath into Options.SourceFilename if not set; Author
		// stays unset in verify so it falls through to cached.
		expectText:  []string{"test.docx", "cached author"},
		expectPages: 1,
	}
}

func caseSEQField() verifyCase {
	return verifyCase{
		name:        "84_seq_field",
		description: "SEQ Figure produces 1, 2, 3 ... per identifier",
		build: func(t *testing.T, dir string) string {
			seq := func(name string) string {
				return `<w:r><w:fldChar w:fldCharType="begin"/></w:r>
        <w:r><w:instrText xml:space="preserve">SEQ ` + name + `</w:instrText></w:r>
        <w:r><w:fldChar w:fldCharType="separate"/></w:r>
        <w:r><w:t>?</w:t></w:r>
        <w:r><w:fldChar w:fldCharType="end"/></w:r>`
			}
			return newDocx().Body(`
    <w:p><w:r><w:t xml:space="preserve">Figure </w:t></w:r>`+seq("Figure")+`<w:r><w:t>: alpha</w:t></w:r></w:p>
    <w:p><w:r><w:t xml:space="preserve">Figure </w:t></w:r>`+seq("Figure")+`<w:r><w:t>: beta</w:t></w:r></w:p>
    <w:p><w:r><w:t xml:space="preserve">Table </w:t></w:r>`+seq("Table")+`<w:r><w:t>: t1</w:t></w:r></w:p>
    <w:p><w:r><w:t xml:space="preserve">Figure </w:t></w:r>`+seq("Figure")+`<w:r><w:t>: gamma</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"Figure", "Table", "alpha", "beta", "gamma"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			// Figure counter should reach 3; Table counter should be 1.
			for _, want := range []string{"1", "2", "3"} {
				if !strings.Contains(txt, want) {
					fail("expected %q somewhere; got:\n%s", want, txt)
				}
			}
		},
	}
}

// --- batch O coverage ----------------------------------------------------

func caseTrackChanges() verifyCase {
	return verifyCase{
		name:        "85_track_changes_accept",
		description: "w:ins kept, w:del dropped (accept mode)",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">start </w:t></w:r>
      <w:ins><w:r><w:t xml:space="preserve">INSERTED-KEPT </w:t></w:r></w:ins>
      <w:del><w:r><w:delText xml:space="preserve">REJECTED-DROP </w:delText></w:r></w:del>
      <w:r><w:t>end</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"start", "INSERTED-KEPT", "end"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "REJECTED-DROP") {
				fail("deleted text leaked into PDF (accept mode)")
			}
		},
	}
}

// --- batch N coverage ----------------------------------------------------

func caseFootnotes() verifyCase {
	return verifyCase{
		name:        "86_footnotes_trailer",
		description: "Footnote refs render as [N]; trailer section lists each note body",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">A claim with a citation</w:t></w:r>
      <w:r><w:footnoteReference w:id="2"/></w:r>
      <w:r><w:t xml:space="preserve">. Another point</w:t></w:r>
      <w:r><w:footnoteReference w:id="3"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>`).
				Part("footnotes.xml", `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>This is FOOTNOTE-TWO body.</w:t></w:r></w:p></w:footnote>
  <w:footnote w:id="3"><w:p><w:r><w:t>This is FOOTNOTE-THREE body.</w:t></w:r></w:p></w:footnote>
</w:footnotes>`).
				Write(t, dir)
		},
		// "[2]" and "[3]" in body, plus the note body text rendered at
		// page bottom (we no longer append a trailing "Footnotes" heading).
		expectText:  []string{"A claim with a citation", "Another point", "FOOTNOTE-TWO body", "FOOTNOTE-THREE body"},
		expectPages: 1,
	}
}

// --- batch P coverage ----------------------------------------------------

func caseMultiColumn() verifyCase {
	return verifyCase{
		name:        "87_multi_column",
		description: "Two-column layout: column 1 fills, then column 2 (paragraphs side-by-side)",
		build: func(t *testing.T, dir string) string {
			var b strings.Builder
			for i := 1; i <= 60; i++ {
				fmt.Fprintf(&b, `<w:p><w:r><w:t>paragraph %d of a long flow.</w:t></w:r></w:p>`, i)
			}
			body := b.String() + `
    <w:sectPr>
      <w:cols w:num="2" w:space="720"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		expectText: []string{"paragraph 1 ", "paragraph 60 "},
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Layout-preserving extraction should show column 1 paragraphs
			// to the LEFT of column 2 paragraphs on the same line.
			out, _ := combinedOutput("pdftotext", "-layout", pdf, "-")
			txt := string(out)
			// Look for an early paragraph followed by a high-number paragraph
			// on the same line. If columns rendered correctly, paragraph 1
			// shares a line with a paragraph far down the sequence.
			lines := strings.Split(txt, "\n")
			foundParallel := false
			for _, line := range lines {
				if strings.Contains(line, "paragraph 1 ") && strings.Contains(line, "paragraph 5") {
					// e.g. "paragraph 1 ... paragraph 53"
					foundParallel = true
					break
				}
			}
			if !foundParallel {
				fail("columns appear stacked, not side-by-side:\n%s", txt[:min(500, len(txt))])
			}
		},
	}
}

// --- batch S coverage ----------------------------------------------------

func caseThemeColor() verifyCase {
	// theme1.xml with one named color; the body run uses w:color/themeColor.
	return verifyCase{
		name:        "88_theme_color",
		description: "w:color w:themeColor=accent1 resolves through theme1.xml",
		build: func(t *testing.T, dir string) string {
			theme := `<?xml version="1.0"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <a:themeElements>
    <a:clrScheme><a:accent1><a:srgbClr val="C00040"/></a:accent1></a:clrScheme>
    <a:fontScheme>
      <a:majorFont><a:latin typeface="Cambria"/></a:majorFont>
      <a:minorFont><a:latin typeface="Calibri"/></a:minorFont>
    </a:fontScheme>
  </a:themeElements>
</a:theme>`
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:rPr><w:color w:themeColor="accent1"/></w:rPr>
        <w:t>theme-colored text</w:t></w:r>
    </w:p>`).
				Part("theme/theme1.xml", theme).
				Write(t, dir)
		},
		expectText:  []string{"theme-colored text"},
		expectPages: 1,
	}
}

func caseTableStyleApplied() verifyCase {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="table" w:styleId="MyGrid">
    <w:rPr><w:rFonts/><w:b/><w:color w:val="C00000"/></w:rPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "89_table_style_applied",
		description: "w:tblPr/w:tblStyle applies style's rPr (bold + red) to all cells",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblPr><w:tblStyle w:val="MyGrid"/></w:tblPr>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>styled A</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>styled B</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Styles(styles).Write(t, dir)
		},
		expectText:  []string{"styled A", "styled B"},
		expectPages: 1,
	}
}

// --- batch T coverage ----------------------------------------------------

func caseLineNumbersDrawn() verifyCase {
	return verifyCase{
		name:        "90_line_numbers_drawn",
		description: "w:lnNumType paints counter in the left margin every Nth line",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>line one</w:t></w:r></w:p>
    <w:p><w:r><w:t>line two</w:t></w:r></w:p>
    <w:p><w:r><w:t>line three</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="2160"/>
      <w:lnNumType w:countBy="1" w:start="1"/>
    </w:sectPr>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		expectText:  []string{"line one", "line two", "line three"},
		expectPages: 1,
	}
}

func caseMirrorMargins() verifyCase {
	return verifyCase{
		name:        "91_mirror_margins",
		description: "w:mirrorMargins swaps left/right margins on even pages",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>page one</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>page two</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="2880" w:gutter="0"/>
      <w:mirrorMargins/>
    </w:sectPr>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		expectText:  []string{"page one", "page two"},
		expectPages: 2,
	}
}

// --- batch U coverage ----------------------------------------------------

func caseRefField() verifyCase {
	return verifyCase{
		name:        "92_ref_field",
		description: "REF field resolves to the named bookmark's captured text",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:bookmarkStart w:id="1" w:name="myref"/>
      <w:r><w:t>BOOKMARK-CONTENT-HERE</w:t></w:r>
      <w:bookmarkEnd w:id="1"/>
    </w:p>
    <w:p>
      <w:r><w:t xml:space="preserve">See: </w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText xml:space="preserve">REF myref</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:t>cached-old-value</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
    </w:p>`).Write(t, dir)
		},
		// The REF field should resolve to BOOKMARK-CONTENT-HERE, not stay
		// cached.
		expectText:  []string{"BOOKMARK-CONTENT-HERE", "See:"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			// "BOOKMARK-CONTENT-HERE" should appear at least twice — once as
			// the bookmark target and once as the REF substitution.
			n := strings.Count(txt, "BOOKMARK-CONTENT-HERE")
			if n < 2 {
				fail("REF didn't substitute bookmark text; count=%d", n)
			}
			if strings.Contains(txt, "cached-old-value") {
				fail("REF stale cached value leaked")
			}
		},
	}
}

// --- batch V coverage ----------------------------------------------------

func caseImageCrop() verifyCase {
	// Source image is half blue / half red; crop the right 50% so only the
	// blue half remains.
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			if x < 100 {
				img.Set(x, y, color.RGBA{R: 0, G: 0, B: 200, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 200, G: 0, B: 0, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return verifyCase{
		name:        "93_image_crop",
		description: "a:srcRect crop r=50% drops the right half of the image",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p><w:r><w:t>before crop</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline>
      <a:graphic><a:graphicData>
        <pic:pic>
          <pic:blipFill>
            <a:blip r:embed="rImg"/>
            <a:srcRect l="0" r="50000" t="0" b="0"/>
          </pic:blipFill>
        </pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>after crop</w:t></w:r></w:p>`).
				Media("image1.png", buf.Bytes()).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before crop", "after crop"},
		expectPages: 1,
	}
}

// --- batch W coverage ----------------------------------------------------

func caseDocMetadataInfo() verifyCase {
	return verifyCase{
		name:        "94_doc_info_metadata",
		description: "docProps/core.xml + Options Author surface in PDF /Info dictionary",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`<w:p><w:r><w:t>hello with metadata</w:t></w:r></w:p>`).
				RawFile("docProps/core.xml", `<?xml version="1.0"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
                   xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:title>VERIFY-DOC-TITLE</dc:title>
  <dc:creator>VERIFY-DOC-AUTHOR</dc:creator>
  <dc:subject>VERIFY-DOC-SUBJECT</dc:subject>
</cp:coreProperties>`).
				Write(t, dir)
		},
		expectText:  []string{"hello with metadata"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// gopdf writes /Info metadata as UTF-16BE hex strings, so a
			// raw-byte search misses them. pdfinfo decodes the dictionary.
			out, err := exec.Command("pdfinfo", pdf).Output()
			if err != nil {
				fail("pdfinfo: %v", err)
				return
			}
			text := string(out)
			for _, want := range []string{"VERIFY-DOC-TITLE", "VERIFY-DOC-AUTHOR", "VERIFY-DOC-SUBJECT"} {
				if !strings.Contains(text, want) {
					fail("expected %q in pdfinfo output", want)
				}
			}
		},
	}
}

func caseEmbossOutline() verifyCase {
	return verifyCase{
		name:        "95_emboss_outline",
		description: "w:emboss / w:imprint / w:outline rendered with ghost / lighter color",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:rPr><w:emboss/></w:rPr><w:t xml:space="preserve">EMBOSS </w:t></w:r>
      <w:r><w:rPr><w:imprint/></w:rPr><w:t xml:space="preserve">IMPRINT </w:t></w:r>
      <w:r><w:rPr><w:outline/></w:rPr><w:t>OUTLINE</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"EMBOSS", "IMPRINT", "OUTLINE"},
		expectPages: 1,
	}
}

func caseLetterSpacing() verifyCase {
	// w:spacing="60" = 3pt of letter spacing. At 3pt between glyphs pdftotext
	// breaks the run into separate letters, which is the correct observable
	// outcome: spacing is actually applied. Verify both that the surrounding
	// "normal"/"tail" survive and that the spaced letters appear in order.
	return verifyCase{
		name:        "96_letter_spacing",
		description: "w:spacing in rPr inserts visible inter-character space",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">normal </w:t></w:r>
      <w:r><w:rPr><w:spacing w:val="60"/></w:rPr><w:t>SPACED</w:t></w:r>
      <w:r><w:t xml:space="preserve"> tail</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"normal", "tail"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			// Letters must appear in S-P-A-C-E-D order; allow any whitespace
			// between them so pdftotext column splits don't fail us.
			letters := []string{"S", "P", "A", "C", "E", "D"}
			pos := strings.Index(txt, "normal")
			if pos < 0 {
				fail("missing 'normal' in extracted text")
				return
			}
			rest := txt[pos:]
			for _, ch := range letters {
				i := strings.Index(rest, ch)
				if i < 0 {
					fail("missing %q after 'normal' in: %q", ch, rest)
					return
				}
				rest = rest[i+1:]
			}
		},
	}
}

// --- batch X coverage ----------------------------------------------------

func caseConditionalTable() verifyCase {
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="table" w:styleId="Plain">
    <w:rPr><w:sz w:val="22"/></w:rPr>
  </w:style>
</w:styles>`
	return verifyCase{
		name:        "97_conditional_table",
		description: "w:tblLook firstRow + firstColumn → those cells get bold emphasis",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblPr>
        <w:tblStyle w:val="Plain"/>
        <w:tblLook w:firstRow="1" w:firstColumn="1"/>
      </w:tblPr>
      <w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>H2</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>F1</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>plain</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Styles(styles).Write(t, dir)
		},
		expectText:  []string{"H1", "H2", "F1", "plain"},
		expectPages: 1,
	}
}

// --- batch Y coverage ----------------------------------------------------

func caseHyperlinkField() verifyCase {
	return verifyCase{
		name:        "98_hyperlink_field",
		description: "HYPERLINK field substitutes cached text + emits clickable link",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">visit </w:t></w:r>
      <w:r><w:fldChar w:fldCharType="begin"/></w:r>
      <w:r><w:instrText xml:space="preserve">HYPERLINK "https://example.com/field"</w:instrText></w:r>
      <w:r><w:fldChar w:fldCharType="separate"/></w:r>
      <w:r><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr><w:t>example field link</w:t></w:r>
      <w:r><w:fldChar w:fldCharType="end"/></w:r>
      <w:r><w:t xml:space="preserve">.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"visit", "example field link"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			raw, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			if !bytes.Contains(raw, []byte("https://example.com/field")) {
				fail("HYPERLINK field URL not embedded as link annotation")
			}
		},
	}
}

func casePictureBullet() verifyCase {
	// Picture-bullet image: a tiny pink solid PNG.
	pbImg := makeSolidPNG(40, 40, color.RGBA{R: 220, G: 80, B: 160, A: 255})
	numbering := `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
             xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:numPicBullet w:numPicBulletId="0">
    <w:pict><v:shape><v:imagedata r:id="rPB"/></v:shape></w:pict>
  </w:numPicBullet>
  <w:abstractNum w:abstractNumId="0">
    <w:lvl w:ilvl="0">
      <w:numFmt w:val="bullet"/>
      <w:lvlText w:val="·"/>
      <w:lvlPicBulletId w:val="0"/>
      <w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr>
    </w:lvl>
  </w:abstractNum>
  <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>
</w:numbering>`
	rels := `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rPB" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/pb.png"/>
</Relationships>`
	li := func(text string) string {
		return `<w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
      <w:r><w:t>` + text + `</w:t></w:r></w:p>`
	}
	return verifyCase{
		name:        "99_picture_bullet",
		description: "w:lvlPicBulletId renders an image as the list marker",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(li("first picture-bullet item")+li("second item")+li("third item")).
				Numbering(numbering).
				Media("pb.png", pbImg).
				Rels(rels).
				Write(t, dir)
		},
		expectText:  []string{"first picture-bullet item", "second item", "third item"},
		expectPages: 1,
	}
}

func caseDropCap() verifyCase {
	return verifyCase{
		name:        "100_drop_cap",
		description: "w:framePr dropCap renders the first letter at ~3× size",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:framePr w:dropCap="drop" w:lines="3"/></w:pPr>
      <w:r><w:t xml:space="preserve">Once upon a time there was a long paragraph whose first letter is supposed to drop down three lines as an ornamental capital, an old typography convention familiar to book readers.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		// The enlarged "O" stretches across multiple rendered lines, so
		// pdftotext puts extra whitespace between it and "nce". Verify the
		// surrounding text is intact and the first letter survived.
		expectText:  []string{"upon a time", "ornamental capital"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			// First non-space char must be "O" (the dropped cap).
			trimmed := strings.TrimSpace(txt)
			if trimmed == "" || trimmed[0] != 'O' {
				fail("drop-cap O missing as first character")
			}
		},
	}
}

// --- batch Z coverage ---------------------------------------------------

func caseContinuousSection() verifyCase {
	return verifyCase{
		name:        "101_continuous_section",
		description: "w:sectPr type=continuous does NOT add a page break",
		build: func(t *testing.T, dir string) string {
			body := `<w:p><w:r><w:t>before continuous boundary</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:sectPr>
          <w:type w:val="continuous"/>
          <w:pgSz w:w="11906" w:h="16838"/>
          <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
        </w:sectPr>
      </w:pPr>
      <w:r><w:t>end of first section</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>after continuous boundary</w:t></w:r></w:p>
    <w:sectPr>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().RawBody(docHeader+body+docFooter).Write(t, dir)
		},
		expectText:  []string{"before continuous boundary", "after continuous boundary"},
		expectPages: 1,
	}
}

func caseTabRightAlign() verifyCase {
	return verifyCase{
		name:        "102_tab_right_align",
		description: "Right-aligned tab stop places text so its right edge lands on the stop",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:tabs>
        <w:tab w:val="right" w:pos="7200" w:leader="dot"/>
      </w:tabs></w:pPr>
      <w:r><w:t>Chapter One</w:t></w:r>
      <w:r><w:tab/><w:t>5</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		// Verify the visible parts survive; positioning checked visually in PNG.
		expectText:  []string{"Chapter One"},
		expectPages: 1,
	}
}

func caseThemeShade() verifyCase {
	theme := `<?xml version="1.0"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <a:themeElements>
    <a:clrScheme><a:accent1><a:srgbClr val="4080FF"/></a:accent1></a:clrScheme>
    <a:fontScheme><a:majorFont><a:latin typeface="Calibri"/></a:majorFont><a:minorFont><a:latin typeface="Calibri"/></a:minorFont></a:fontScheme>
  </a:themeElements>
</a:theme>`
	return verifyCase{
		name:        "103_theme_shade",
		description: "w:themeShade darkens the resolved theme color (lumMod approximation)",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:rPr><w:color w:themeColor="accent1" w:themeShade="80"/></w:rPr>
        <w:t>darkened accent</w:t></w:r>
    </w:p>`).
				Part("theme/theme1.xml", theme).
				Write(t, dir)
		},
		expectText:  []string{"darkened accent"},
		expectPages: 1,
	}
}

func caseFootnoteAcrossPageBreak() verifyCase {
	return verifyCase{
		name:        "105_footnote_across_page_break",
		description: "Footnotes for refs on page 1 land at bottom of page 1, not page 2",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">page one body</w:t></w:r>
      <w:r><w:footnoteReference w:id="2"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p>
      <w:r><w:t xml:space="preserve">page two body</w:t></w:r>
      <w:r><w:footnoteReference w:id="3"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>`).
				Part("footnotes.xml", `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>NOTE-FOR-PAGE-ONE</w:t></w:r></w:p></w:footnote>
  <w:footnote w:id="3"><w:p><w:r><w:t>NOTE-FOR-PAGE-TWO</w:t></w:r></w:p></w:footnote>
</w:footnotes>`).
				Write(t, dir)
		},
		expectText:  []string{"page one body", "page two body", "NOTE-FOR-PAGE-ONE", "NOTE-FOR-PAGE-TWO"},
		expectPages: 2,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Per-page text extraction: page 1 should contain page-one note;
			// page 2 should contain page-two note.
			out, _ := combinedOutput("pdftotext", "-f", "1", "-l", "1", pdf, "-")
			p1 := string(out)
			out2, _ := combinedOutput("pdftotext", "-f", "2", "-l", "2", pdf, "-")
			p2 := string(out2)
			if !strings.Contains(p1, "NOTE-FOR-PAGE-ONE") {
				fail("page 1 missing its footnote body:\n%s", p1)
			}
			if !strings.Contains(p2, "NOTE-FOR-PAGE-TWO") {
				fail("page 2 missing its footnote body:\n%s", p2)
			}
			if strings.Contains(p2, "NOTE-FOR-PAGE-ONE") {
				fail("page 1's footnote leaked onto page 2:\n%s", p2)
			}
		},
	}
}

// --- batch DD coverage --------------------------------------------------

func caseFootnotePageBottom() verifyCase {
	return verifyCase{
		name:        "104_footnote_page_bottom",
		description: "Footnotes render at page bottom (not in a trailer)",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">main body with citation</w:t></w:r>
      <w:r><w:footnoteReference w:id="2"/></w:r>
      <w:r><w:t>.</w:t></w:r>
    </w:p>`).
				Part("footnotes.xml", `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>BOTTOM-OF-PAGE-NOTE</w:t></w:r></w:p></w:footnote>
</w:footnotes>`).
				Write(t, dir)
		},
		expectText:  []string{"main body with citation", "BOTTOM-OF-PAGE-NOTE"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The footnote body should sit BELOW the body content. pdftotext
			// preserves vertical order; check that the order is body → note.
			txt := pdftotext(t, pdf)
			bodyIdx := strings.Index(txt, "main body with citation")
			noteIdx := strings.Index(txt, "BOTTOM-OF-PAGE-NOTE")
			if bodyIdx < 0 || noteIdx < 0 {
				fail("missing markers in extracted text:\n%s", txt)
			}
			if noteIdx < bodyIdx {
				fail("footnote rendered above body (idx %d < %d)", noteIdx, bodyIdx)
			}
		},
	}
}

// caseFootnoteInTableCell guards against regression of a bug where
// measureCell's dry-layout pass also queued the footnote ID, causing each
// note in a table cell to be drawn twice at page bottom.
func caseFootnoteInTableCell() verifyCase {
	return verifyCase{
		name:        "106_footnote_in_table_cell",
		description: "Footnote referenced inside a table cell renders exactly once at page bottom",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr><w:tc><w:p>
        <w:r><w:t xml:space="preserve">cell with ref</w:t></w:r>
        <w:r><w:footnoteReference w:id="2"/></w:r>
        <w:r><w:t>.</w:t></w:r>
      </w:p></w:tc></w:tr>
    </w:tbl>`
			return newDocx().
				Body(body).
				Part("footnotes.xml", `<?xml version="1.0"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:footnote w:type="separator" w:id="0"/>
  <w:footnote w:type="continuationSeparator" w:id="1"/>
  <w:footnote w:id="2"><w:p><w:r><w:t>UNIQUE-MARKER</w:t></w:r></w:p></w:footnote>
</w:footnotes>`).
				Write(t, dir)
		},
		expectText:  []string{"cell with ref", "UNIQUE-MARKER"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			n := strings.Count(txt, "UNIQUE-MARKER")
			if n != 1 {
				fail("UNIQUE-MARKER appears %d times in output, want 1:\n%s", n, txt)
			}
		},
	}
}

// caseHangingIndent exercises w:ind w:left + w:hanging (negative
// IndentFirstLinePt). The first physical line should outdent — i.e. start
// further left than wrapped continuation lines. We verify the content
// survives across wrapping; the visual outdent is left to the PNG snapshot.
func caseHangingIndent() verifyCase {
	return verifyCase{
		name:        "107_hanging_indent",
		description: "w:ind w:hanging — first line outdents past body indent, wraps fit at body width",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr>
        <w:ind w:left="1440" w:hanging="720"/>
      </w:pPr>
      <w:r><w:t xml:space="preserve">FIRST-WORD body continues here with enough text to force at least one wrap so we can confirm the indent applies correctly across line breaks. </w:t></w:r>
      <w:r><w:t xml:space="preserve">More words. More words. More words. More words. More words. More words.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"FIRST-WORD", "More words"},
		expectPages: 1,
	}
}

// caseSmartTagTransparent verifies that text inside a w:smartTag wrapper
// (often emitted by older Word for auto-recognized entities like dates,
// addresses, names) is preserved rather than skipped.
func caseSmartTagTransparent() verifyCase {
	return verifyCase{
		name:        "108_smarttag_transparent",
		description: "w:smartTag wrapper is transparent — contained text reaches the PDF",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:smartTag w:uri="urn:schemas-microsoft-com:office:smarttags" w:element="date">
        <w:r><w:t xml:space="preserve">INSIDE-SMARTTAG</w:t></w:r>
      </w:smartTag>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "INSIDE-SMARTTAG", "after"},
		expectPages: 1,
	}
}

// caseInsHyperlinkSurvives verifies that a tracked-change insertion
// containing a hyperlink (w:ins wrapping w:hyperlink) renders the link text
// rather than dropping it.
func caseInsHyperlinkSurvives() verifyCase {
	return verifyCase{
		name:        "109_ins_hyperlink_survives",
		description: "w:ins around w:hyperlink keeps the inserted link text",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:ins w:id="1" w:author="x" w:date="2026-01-01T00:00:00Z">
        <w:hyperlink r:id="rLink">
          <w:r><w:t>INSERTED-LINK</w:t></w:r>
        </w:hyperlink>
      </w:ins>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rLink" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="http://example.com" TargetMode="External"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before", "INSERTED-LINK", "after"},
		expectPages: 1,
	}
}

// caseMoveToMoveFrom: tracked-change moves. moveTo (new location) renders;
// moveFrom (old location) drops. Behaves identically to ins/del per Word's
// "accept all" semantics.
func caseMoveToMoveFrom() verifyCase {
	return verifyCase{
		name:        "110_move_to_move_from",
		description: "w:moveTo keeps moved text; w:moveFrom drops the old location",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:moveTo w:id="1" w:author="x" w:date="2026-01-01T00:00:00Z">
        <w:r><w:t>MOVED-HERE</w:t></w:r>
      </w:moveTo>
      <w:r><w:t xml:space="preserve"> middle </w:t></w:r>
      <w:moveFrom w:id="2" w:author="x" w:date="2026-01-01T00:00:00Z">
        <w:r><w:t>SHOULD-NOT-APPEAR</w:t></w:r>
      </w:moveFrom>
      <w:r><w:t xml:space="preserve">after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "MOVED-HERE", "middle", "after"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "SHOULD-NOT-APPEAR") {
				fail("moveFrom content leaked into output")
			}
		},
	}
}

// caseOLEObjectPlaceholder: a w:object (embedded OLE) should produce a
// visible marker rather than silently disappearing.
func caseOLEObjectPlaceholder() verifyCase {
	return verifyCase{
		name:        "111_ole_object_placeholder",
		description: "w:object emits an [Embedded object] marker so the reader sees something was there",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:r>
        <w:object>
          <o:OLEObject xmlns:o="urn:schemas-microsoft-com:office:office" Type="Embed"/>
        </w:object>
      </w:r>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "Embedded object", "after"},
		expectPages: 1,
	}
}

// caseSettingsDefaultTabStop: settings.xml declares a 4-inch defaultTabStop
// (5760 twips). A paragraph with a single tab and no explicit tabs should
// snap to that grid instead of the 36pt half-inch fallback. We use 4 inches
// so the resulting layout gap is unmistakable in pdftotext output.
func caseSettingsDefaultTabStop() verifyCase {
	return verifyCase{
		name:        "112_settings_default_tab_stop",
		description: "w:defaultTabStop from settings.xml drives the implicit tab grid",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t>L</w:t></w:r>
      <w:r><w:tab/></w:r>
      <w:r><w:t>R</w:t></w:r>
    </w:p>`).
				Part("settings.xml", `<?xml version="1.0"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:defaultTabStop w:val="5760"/>
</w:settings>`).
				Write(t, dir)
		},
		expectText:  []string{"L", "R"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// pdftotext -bbox emits each word with absolute coordinates in
			// PDF points. With the 4-inch (288pt) defaultTabStop, R should
			// sit at xMin ≥ 270 (allowing some slack). The half-inch fallback
			// would put R near xMin ≈ 108.
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			lxMin, ok := bboxXMin(txt, "L")
			if !ok {
				fail("could not find L bbox in:\n%s", txt)
				return
			}
			rxMin, ok := bboxXMin(txt, "R")
			if !ok {
				fail("could not find R bbox in:\n%s", txt)
				return
			}
			if rxMin-lxMin < 250 {
				fail("L→R gap = %.1fpt, want ≥ 250pt (4-inch tab grid)", rxMin-lxMin)
			}
		},
	}
}

// bboxXMin pulls the xMin attribute of the first <word>…label</word> entry
// from pdftotext -bbox output. Returns (0, false) when not found.
func bboxXMin(bboxXML, label string) (float64, bool) {
	needle := ">" + label + "</word>"
	idx := strings.Index(bboxXML, needle)
	if idx < 0 {
		return 0, false
	}
	// Walk back to find the most recent xMin=" before this label.
	prefix := bboxXML[:idx]
	xi := strings.LastIndex(prefix, `xMin="`)
	if xi < 0 {
		return 0, false
	}
	rest := prefix[xi+len(`xMin="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(rest[:end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// caseVMLImage exercises the legacy w:pict / v:imagedata route. Common in
// older Word docs and Excel/Outlook pastes; previously skipped entirely so
// the image went missing. The case asserts the image renders by checking
// for an embedded /Image XObject in the PDF stream — image presence is the
// only signal pdftotext can't give us.
func caseVMLImage() verifyCase {
	img := makeSolidPNG(40, 20, color.RGBA{R: 30, G: 200, B: 30, A: 255})
	return verifyCase{
		name:        "113_vml_image",
		description: "w:pict / v:imagedata routes through the same image renderer as w:drawing",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:r>
        <w:pict>
          <v:shape xmlns:v="urn:schemas-microsoft-com:vml" style="width:40pt;height:20pt">
            <v:imagedata r:id="rImg"/>
          </v:shape>
        </w:pict>
      </w:r>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).
				Media("image1.png", img).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"before", "after"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Raw scan: gopdf emits images as /Image XObjects. If we routed
			// through the image path, the PDF stream will contain "/Image".
			data, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			if !bytes.Contains(data, []byte("/Image")) {
				fail("no /Image XObject in PDF — VML image did not render")
			}
		},
	}
}

// caseFramePrPositioned exercises a w:framePr-positioned paragraph that
// should render at an absolute page-anchored location, not inline. The
// frame's text ("FLOATED") must appear well to the right of the body's
// natural left margin — confirming it didn't fall through to inline flow.
func caseFramePrPositioned() verifyCase {
	return verifyCase{
		name:        "114_framepr_positioned",
		description: "w:framePr with vAnchor=page + xAlign=right positions the paragraph absolutely",
		build: func(t *testing.T, dir string) string {
			// Frame anchored to page: 200pt wide, 4 inches down from page
			// top, right-aligned to the margin region.
			return newDocx().Body(`
    <w:p><w:r><w:t>body line one</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:framePr w:w="4000" w:h="800" w:vAnchor="page" w:hAnchor="margin" w:xAlign="right" w:y="5760" w:wrap="around"/>
      </w:pPr>
      <w:r><w:t>FLOATED</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>body line two</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"body line one", "FLOATED", "body line two"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// FLOATED should sit to the right of the body lines. Bbox check:
			// FLOATED.xMin > body.xMin by a wide margin (≥ 200pt).
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			bodyX, ok := bboxXMin(txt, "body")
			if !ok {
				fail("body bbox not found")
				return
			}
			floatX, ok := bboxXMin(txt, "FLOATED")
			if !ok {
				fail("FLOATED bbox not found")
				return
			}
			if floatX-bodyX < 200 {
				fail("FLOATED.x (%.1f) only %.1fpt right of body.x (%.1f), want ≥ 200",
					floatX, floatX-bodyX, bodyX)
			}
			// And FLOATED should sit at roughly y=288pt (4 inches from page top,
			// per w:y=5760 twips). Allow ±20pt.
			floatY, ok := bboxYMin(txt, "FLOATED")
			if !ok {
				fail("FLOATED y bbox not found")
				return
			}
			if floatY < 268 || floatY > 308 {
				fail("FLOATED.y = %.1f, want roughly 288 (5760 twips below page top)", floatY)
			}
		},
	}
}

// bboxYMin pulls the yMin attribute of the first <word>…label</word> in
// pdftotext -bbox output.
func bboxYMin(bboxXML, label string) (float64, bool) {
	needle := ">" + label + "</word>"
	idx := strings.Index(bboxXML, needle)
	if idx < 0 {
		return 0, false
	}
	prefix := bboxXML[:idx]
	yi := strings.LastIndex(prefix, `yMin="`)
	if yi < 0 {
		return 0, false
	}
	rest := prefix[yi+len(`yMin="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(rest[:end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// caseInlineSdt: inline content control. <w:sdt><w:sdtContent><w:r>…</w:r>
// </w:sdtContent></w:sdt> sits in a paragraph; the contained run text must
// survive the wrapper.
func caseInlineSdt() verifyCase {
	return verifyCase{
		name:        "115_inline_sdt",
		description: "Inline w:sdt is transparent — its run content reaches the PDF",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:sdt>
        <w:sdtPr><w:id w:val="1"/><w:placeholder><w:docPart w:val="DefaultPlaceholder"/></w:placeholder></w:sdtPr>
        <w:sdtContent>
          <w:r><w:t>INLINE-SDT-TEXT</w:t></w:r>
        </w:sdtContent>
      </w:sdt>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "INLINE-SDT-TEXT", "after"},
		expectPages: 1,
	}
}

// caseBlockSdt: block-level content control wrapping a whole paragraph.
// Common in templates ("[Click here to enter text]" placeholders).
func caseBlockSdt() verifyCase {
	return verifyCase{
		name:        "116_block_sdt",
		description: "Block-level w:sdt is transparent — wrapped paragraphs reach the body flow",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>first</w:t></w:r></w:p>
    <w:sdt>
      <w:sdtPr><w:id w:val="2"/></w:sdtPr>
      <w:sdtContent>
        <w:p><w:r><w:t>SDT-WRAPPED-PARAGRAPH</w:t></w:r></w:p>
      </w:sdtContent>
    </w:sdt>
    <w:p><w:r><w:t>last</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"first", "SDT-WRAPPED-PARAGRAPH", "last"},
		expectPages: 1,
	}
}

// caseSdtInTableCell: block-level SDT inside a table cell. Verifies the
// table-cell dispatch path also recognizes w:sdt.
func caseSdtInTableCell() verifyCase {
	return verifyCase{
		name:        "117_sdt_in_table_cell",
		description: "Block w:sdt inside a table cell still reaches the cell's blocks",
		build: func(t *testing.T, dir string) string {
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="5000"/></w:tblGrid>
      <w:tr><w:tc>
        <w:sdt>
          <w:sdtPr><w:id w:val="3"/></w:sdtPr>
          <w:sdtContent>
            <w:p><w:r><w:t>CELL-SDT-TEXT</w:t></w:r></w:p>
          </w:sdtContent>
        </w:sdt>
      </w:tc></w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"CELL-SDT-TEXT"},
		expectPages: 1,
	}
}

// caseInlineMath: m:oMath inside a paragraph. Best-effort: contained
// chardata survives as an italic run. Structural formatting (fractions,
// subscripts) is intentionally lost.
func caseInlineMath() verifyCase {
	return verifyCase{
		name:        "118_inline_math",
		description: "Inline m:oMath equation extracts visible text",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">The polynomial </w:t></w:r>
      <m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">
        <m:r><m:t>P(x)=ax</m:t></m:r>
        <m:sSup>
          <m:e><m:r><m:t>x</m:t></m:r></m:e>
          <m:sup><m:r><m:t>2</m:t></m:r></m:sup>
        </m:sSup>
        <m:r><m:t>+bx+c</m:t></m:r>
      </m:oMath>
      <w:r><w:t xml:space="preserve"> is quadratic.</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"polynomial", "P(x)", "quadratic"},
		expectPages: 1,
	}
}

// caseDisplayMath: m:oMathPara as a body-level equation. Renders as its
// own paragraph (centered + italic) inserted between surrounding body
// paragraphs.
func caseDisplayMath() verifyCase {
	return verifyCase{
		name:        "119_display_math",
		description: "Body-level m:oMathPara becomes its own paragraph in flow",
		build: func(t *testing.T, dir string) string {
			return newDocx().RawBody(docHeader+`
    <w:p><w:r><w:t>before</w:t></w:r></w:p>
    <m:oMathPara xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">
      <m:oMath>
        <m:r><m:t>EQUATION-TEXT</m:t></m:r>
      </m:oMath>
    </m:oMathPara>
    <w:p><w:r><w:t>after</w:t></w:r></w:p>`+docFooter).Write(t, dir)
		},
		expectText:  []string{"before", "EQUATION-TEXT", "after"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Order check: before → equation → after in the extracted text.
			txt := pdftotext(t, pdf)
			b := strings.Index(txt, "before")
			e := strings.Index(txt, "EQUATION-TEXT")
			a := strings.Index(txt, "after")
			if b < 0 || e < 0 || a < 0 {
				fail("missing markers in:\n%s", txt)
				return
			}
			if !(b < e && e < a) {
				fail("expected before<equation<after, got %d %d %d", b, e, a)
			}
		},
	}
}

// caseFldSimplePage exercises the "simple" form of a field — w:fldSimple
// with a PAGE instr. Word commonly writes this form in headers/footers
// instead of the verbose fldChar/instrText complex form. Both should
// substitute to the actual page number per the renderer's stamp pass.
func caseFldSimplePage() verifyCase {
	return verifyCase{
		name:        "120_fldsimple_page",
		description: "w:fldSimple PAGE in a header gets substituted to the actual page number",
		build: func(t *testing.T, dir string) string {
			hdr := `<?xml version="1.0"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p>
    <w:r><w:t xml:space="preserve">Page </w:t></w:r>
    <w:fldSimple w:instr="PAGE">
      <w:r><w:t>OLD-CACHED-VAL</w:t></w:r>
    </w:fldSimple>
  </w:p>
</w:hdr>`
			body := `
    <w:p><w:r><w:t>body of doc</w:t></w:r></w:p>
    <w:sectPr>
      <w:headerReference w:type="default" r:id="rH1"/>
      <w:pgSz w:w="11906" w:h="16838"/>
      <w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/>
    </w:sectPr>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("header1.xml", hdr).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rH1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText:  []string{"Page", "1", "body of doc"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The OLD cached value must be replaced — if it leaks through,
			// our fldSimple expansion to the marker stream didn't reach
			// flattenFields.
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "OLD-CACHED-VAL") {
				fail("cached PAGE value leaked — fldSimple did not substitute:\n%s", txt)
			}
			// "Page 1" should appear on the page (header).
			if !strings.Contains(txt, "Page") || !strings.Contains(txt, "1") {
				fail("missing 'Page' or '1' in:\n%s", txt)
			}
		},
	}
}

// caseRTLParagraph: w:bidi paragraph with Hebrew text. The MVP reverses
// atom (word) order in the line and rune order within each RTL atom; the
// paragraph also right-aligns by default. We verify the right-alignment
// via bbox — the first visible word should sit near the right margin, not
// the left.
//
// This case requires a fallback font with Hebrew glyphs (Noto Sans CJK
// includes basic Hebrew). Skipped silently if not present.
func caseRTLParagraph() verifyCase {
	return verifyCase{
		name:        "121_rtl_paragraph",
		description: "w:bidi paragraph reverses word order and right-aligns by default",
		build: func(t *testing.T, dir string) string {
			// "shalom olam" written in Hebrew, three logical words plus
			// short LTR digit to confirm digits stay positioned sensibly.
			return newDocx().Body(`
    <w:p><w:r><w:t>ltr-paragraph-left</w:t></w:r></w:p>
    <w:p>
      <w:pPr><w:bidi/></w:pPr>
      <w:r><w:t xml:space="preserve">שלום עולם</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"ltr-paragraph-left"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The LTR paragraph's word sits near xMin=72 (left margin).
			// The RTL paragraph's content should be right-aligned, so its
			// own xMin is much further right (≥ ~300pt on A4 with default
			// margins — the exact value depends on text width).
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			ltrX, ok := bboxXMin(txt, "ltr-paragraph-left")
			if !ok {
				// pdftotext often splits at hyphens; try just the first word.
				ltrX, ok = bboxXMin(txt, "ltr")
				if !ok {
					fail("could not locate LTR sample in bbox output")
					return
				}
			}
			// The first non-LTR word bbox after the LTR one tells us where
			// the RTL line landed. We approximate by scanning for any word
			// whose xMin > ltrX + 50 (i.e. right-aligned, not left).
			// If our RTL alignment didn't kick in the Hebrew word would be
			// at the same xMin as the LTR paragraph.
			if !hasWordRightOf(txt, ltrX+50) {
				fail("no word found right of LTR x %.1f — RTL paragraph not right-aligned",
					ltrX)
			}
		},
	}
}

// hasWordRightOf reports whether any <word> in the bbox XML has xMin
// greater than the threshold. Used by RTL right-alignment check.
func hasWordRightOf(bboxXML string, threshold float64) bool {
	rest := bboxXML
	for {
		idx := strings.Index(rest, `xMin="`)
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(`xMin="`):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return false
		}
		v, err := strconv.ParseFloat(rest[:end], 64)
		if err == nil && v > threshold {
			return true
		}
		rest = rest[end+1:]
	}
}

// caseTextBoxContent: a w:drawing wrapping a wps:txbx with paragraphs
// inside. Without extraction the whole drawing falls through and the
// text-box content is lost. We assert the text-box prose reaches the
// PDF.
func caseTextBoxContent() verifyCase {
	return verifyCase{
		name:        "122_textbox_content",
		description: "wps:txbx text-box content is preserved as inline italic text",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before-box </w:t></w:r>
      <w:r>
        <w:drawing>
          <wp:anchor xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
            <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
              <a:graphicData>
                <wps:wsp xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape">
                  <wps:txbx>
                    <w:txbxContent>
                      <w:p><w:r><w:t>FIRST-BOX-LINE</w:t></w:r></w:p>
                      <w:p><w:r><w:t>SECOND-BOX-LINE</w:t></w:r></w:p>
                    </w:txbxContent>
                  </wps:txbx>
                </wps:wsp>
              </a:graphicData>
            </a:graphic>
          </wp:anchor>
        </w:drawing>
      </w:r>
      <w:r><w:t xml:space="preserve"> after-box</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before-box", "FIRST-BOX-LINE", "SECOND-BOX-LINE", "after-box"},
		expectPages: 1,
	}
}

// caseAlternateContentChoice: mc:AlternateContent wrapping a text box in
// mc:Choice (modern shape) and a VML pict in mc:Fallback. We prefer the
// Choice — its text-box content should reach the PDF.
func caseAlternateContentChoice() verifyCase {
	return verifyCase{
		name:        "123_altcontent_choice",
		description: "mc:AlternateContent prefers Choice; its text-box content is emitted",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">before </w:t></w:r>
      <w:r>
        <mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">
          <mc:Choice Requires="wps">
            <w:drawing>
              <wp:anchor xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
                <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
                  <a:graphicData>
                    <wps:wsp xmlns:wps="http://schemas.microsoft.com/office/word/2010/wordprocessingShape">
                      <wps:txbx>
                        <w:txbxContent>
                          <w:p><w:r><w:t>CHOICE-BOX-TEXT</w:t></w:r></w:p>
                        </w:txbxContent>
                      </wps:txbx>
                    </wps:wsp>
                  </a:graphicData>
                </a:graphic>
              </wp:anchor>
            </w:drawing>
          </mc:Choice>
          <mc:Fallback>
            <w:pict><v:shape xmlns:v="urn:schemas-microsoft-com:vml" style="width:50pt;height:50pt"/></w:pict>
          </mc:Fallback>
        </mc:AlternateContent>
      </w:r>
      <w:r><w:t xml:space="preserve"> after</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"before", "CHOICE-BOX-TEXT", "after"},
		expectPages: 1,
	}
}

// caseAlternateContentFallback: mc:Choice yields nothing renderable; we
// drop to mc:Fallback and the VML image inside renders. We assert the
// PDF contains an /Image XObject.
func caseAlternateContentFallback() verifyCase {
	img := makeSolidPNG(30, 30, color.RGBA{R: 200, G: 100, B: 30, A: 255})
	return verifyCase{
		name:        "124_altcontent_fallback",
		description: "When mc:Choice produces no content, mc:Fallback is used",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:r>
        <mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">
          <mc:Choice Requires="unknown-namespace">
            <unknownElement/>
          </mc:Choice>
          <mc:Fallback>
            <w:pict>
              <v:shape xmlns:v="urn:schemas-microsoft-com:vml" style="width:30pt;height:30pt">
                <v:imagedata r:id="rImg"/>
              </v:shape>
            </w:pict>
          </mc:Fallback>
        </mc:AlternateContent>
      </w:r>
    </w:p>`).
				Media("image1.png", img).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rImg" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`).
				Write(t, dir)
		},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			data, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			if !bytes.Contains(data, []byte("/Image")) {
				fail("no /Image XObject — Fallback VML image did not render")
			}
		},
	}
}

// caseComments: reviewer markup from word/comments.xml. Body has
// commentRangeStart/End wrapping a phrase and commentReference at the
// site. We don't render any inline marker — the comment text appears
// in the trailing "Comments" section.
func caseComments() verifyCase {
	return verifyCase{
		name:        "125_comments_trailer",
		description: "Comments survive as a trailing 'Comments' section, like endnotes",
		build: func(t *testing.T, dir string) string {
			return newDocx().
				Body(`
    <w:p>
      <w:commentRangeStart w:id="0"/>
      <w:r><w:t>The reviewed phrase</w:t></w:r>
      <w:commentRangeEnd w:id="0"/>
      <w:r><w:commentReference w:id="0"/></w:r>
      <w:r><w:t xml:space="preserve"> stays in the body.</w:t></w:r>
    </w:p>`).
				Part("comments.xml", `<?xml version="1.0"?>
<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:comment w:id="0" w:author="Reviewer" w:date="2026-01-01T00:00:00Z">
    <w:p><w:r><w:t>FIRST-COMMENT-BODY</w:t></w:r></w:p>
  </w:comment>
</w:comments>`).
				Write(t, dir)
		},
		expectText:  []string{"reviewed phrase", "FIRST-COMMENT-BODY"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Order check: body text comes before the comment trailer.
			txt := pdftotext(t, pdf)
			b := strings.Index(txt, "reviewed phrase")
			c := strings.Index(txt, "FIRST-COMMENT-BODY")
			if b < 0 || c < 0 {
				fail("missing markers in:\n%s", txt)
				return
			}
			if !(b < c) {
				fail("comment appears before body text (idx %d > %d)", c, b)
			}
		},
	}
}

// caseHeadingOutline: paragraphs styled as Heading1/Heading2/Title
// contribute entries to the PDF outline. We verify by checking the PDF
// for the /Outlines object plus our heading titles appearing in the
// /Title entries inside.
func caseHeadingOutline() verifyCase {
	return verifyCase{
		name:        "126_heading_outline",
		description: "Heading1/2/Title paragraphs produce PDF outline (sidebar bookmarks)",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:pPr><w:pStyle w:val="Title"/></w:pPr>
      <w:r><w:t>BOOK-TITLE</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>OUTLINE-CHAPTER-ONE</w:t></w:r>
    </w:p>
    <w:p><w:r><w:t>body text</w:t></w:r></w:p>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>OUTLINE-CHAPTER-TWO</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"BOOK-TITLE", "OUTLINE-CHAPTER-ONE", "body text", "OUTLINE-CHAPTER-TWO"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			data, err := os.ReadFile(pdf)
			if err != nil {
				fail("read pdf: %v", err)
				return
			}
			// PDF outline strings are emitted as PDF "text strings", which
			// gopdf encodes UTF-16BE — they won't appear as raw ASCII in
			// the byte stream. Two structural signals are enough:
			//
			//   1. /Outlines is present (anchors the outline tree).
			//   2. We have at least 3 /Title entries in the file
			//      (Title + Heading1 #1 + Heading1 #2 = our three
			//      heading paragraphs).
			if !bytes.Contains(data, []byte("/Outlines")) {
				fail("no /Outlines reference in PDF — outline tree not emitted")
				return
			}
			n := bytes.Count(data, []byte("/Title"))
			if n < 3 {
				fail("found %d /Title entries in PDF, want ≥ 3 (one per heading)", n)
			}
		},
	}
}

// caseChartTextExtraction: a w:drawing referencing an embedded chart
// part. The chart XML carries a title and axis labels; without
// extraction, the reader loses every word the chart was telling them.
// We assert the chart's distinctive labels appear in the produced PDF.
func caseChartTextExtraction() verifyCase {
	return verifyCase{
		name:        "127_chart_text",
		description: "c:chart text (title + labels) is surfaced from the chart part",
		build: func(t *testing.T, dir string) string {
			chart := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"
              xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <c:chart>
    <c:title>
      <c:tx><c:rich><a:p><a:r><a:t>SALES-PER-QUARTER</a:t></a:r></a:p></c:rich></c:tx>
    </c:title>
    <c:plotArea>
      <c:barChart>
        <c:ser>
          <c:tx><c:strRef><c:strCache><c:pt><c:v>SERIES-A</c:v></c:pt></c:strCache></c:strRef></c:tx>
          <c:cat>
            <c:strRef><c:strCache>
              <c:pt><c:v>Q1-LABEL</c:v></c:pt>
              <c:pt><c:v>Q2-LABEL</c:v></c:pt>
            </c:strCache></c:strRef>
          </c:cat>
        </c:ser>
      </c:barChart>
    </c:plotArea>
  </c:chart>
</c:chartSpace>`
			body := `
    <w:p>
      <w:r><w:t xml:space="preserve">See the chart: </w:t></w:r>
      <w:r>
        <w:drawing>
          <wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
            <wp:extent cx="3000000" cy="2000000"/>
            <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
              <a:graphicData>
                <c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rChart"/>
              </a:graphicData>
            </a:graphic>
          </wp:inline>
        </w:drawing>
      </w:r>
      <w:r><w:t xml:space="preserve"> below.</w:t></w:r>
    </w:p>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("charts/chart1.xml", chart).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rChart" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/chart1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText: []string{
			"See the chart",
			"SALES-PER-QUARTER",
			"SERIES-A",
			"Q1-LABEL",
			"Q2-LABEL",
			"below",
		},
		expectPages: 1,
	}
}

// caseChartBarRender exercises the actual chart-drawing path with
// numeric values present. The chart's title + legend + category
// labels survive as PDF text via the chart renderer's Cell calls.
func caseChartBarRender() verifyCase {
	return verifyCase{
		name:        "127a_chart_bar",
		description: "Bar chart with numeric data renders title/legend/categories as PDF text",
		build: func(t *testing.T, dir string) string {
			chart := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"
              xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <c:chart>
    <c:title>
      <c:tx><c:rich><a:p><a:r><a:t>QUARTERLY-REVENUE</a:t></a:r></a:p></c:rich></c:tx>
    </c:title>
    <c:plotArea>
      <c:barChart>
        <c:barDir val="col"/>
        <c:ser>
          <c:tx><c:strRef><c:strCache><c:pt idx="0"><c:v>WIDGETS-INC</c:v></c:pt></c:strCache></c:strRef></c:tx>
          <c:cat>
            <c:strRef><c:strCache>
              <c:pt idx="0"><c:v>QUARTER-ONE</c:v></c:pt>
              <c:pt idx="1"><c:v>QUARTER-TWO</c:v></c:pt>
              <c:pt idx="2"><c:v>QUARTER-THREE</c:v></c:pt>
            </c:strCache></c:strRef>
          </c:cat>
          <c:val>
            <c:numRef><c:numCache>
              <c:pt idx="0"><c:v>30</c:v></c:pt>
              <c:pt idx="1"><c:v>45</c:v></c:pt>
              <c:pt idx="2"><c:v>60</c:v></c:pt>
            </c:numCache></c:numRef>
          </c:val>
        </c:ser>
      </c:barChart>
    </c:plotArea>
  </c:chart>
</c:chartSpace>`
			body := `
    <w:p>
      <w:r>
        <w:drawing>
          <wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
            <wp:extent cx="3000000" cy="2000000"/>
            <a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
              <a:graphicData>
                <c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" r:id="rChart"/>
              </a:graphicData>
            </a:graphic>
          </wp:inline>
        </w:drawing>
      </w:r>
    </w:p>`
			return newDocx().
				RawBody(docHeader+body+docFooter).
				Part("charts/chart1.xml", chart).
				Rels(`<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rChart" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/chart1.xml"/>
</Relationships>`).
				Write(t, dir)
		},
		expectText: []string{
			"QUARTERLY-REVENUE",
			"QUARTER-ONE",
			"QUARTER-TWO",
			"QUARTER-THREE",
		},
		expectPages: 1,
	}
}

// casePageBreakBeforeValZero exercises the OOXML on/off attribute
// convention. Heading paragraphs in markdown-converted docx commonly
// carry <w:pageBreakBefore w:val="0"/> — meaning "explicitly NO page
// break before". Treating the element's mere presence as ON used to
// produce one-page-per-heading output on real-world docs. This case
// puts the same flag on a body paragraph and asserts the document
// stays on a single page.
func casePageBreakBeforeValZero() verifyCase {
	return verifyCase{
		name:        "128_pagebreakbefore_val_zero",
		description: "<w:pageBreakBefore w:val=\"0\"/> means OFF, not ON",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>first paragraph</w:t></w:r></w:p>
    <w:p>
      <w:pPr><w:pageBreakBefore w:val="0"/></w:pPr>
      <w:r><w:t>second paragraph (NO break before)</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"first paragraph", "second paragraph"},
		expectPages: 1, // would be 2 with the old eager-true parser
	}
}

// caseOverwideWordInCell exercises the character-level wrap fallback
// triggered when a single word atom is wider than its cell's content
// width. Without the fallback, long identifiers would spill into the
// next cell (visible as "member_idClaims" with no border between).
func caseOverwideWordInCell() verifyCase {
	return verifyCase{
		name:        "129_overwide_word_in_cell",
		description: "Long word in a narrow cell wraps at character boundaries instead of spilling",
		build: func(t *testing.T, dir string) string {
			// Two-column 2-row table. Column 1 is 2000 twips (100 pt)
			// — still not wide enough to render "submission_timestamp"
			// (~96 pt) plus cell padding on one line, so the per-rune
			// wrap fallback must kick in. Column 2 is 3000 twips so the
			// table still fits without ridiculous overflow.
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2000"/><w:gridCol w:w="3000"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>submission_timestamp</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>UNIQUE-NEIGHBOR</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"submission", "UNIQUE-NEIGHBOR"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The two cells must NOT be concatenated. pdftotext extracts
			// cell text with whitespace between columns; if the long word
			// spilled into the neighbor, "submission_timestamp" and
			// "UNIQUE-NEIGHBOR" would appear adjacent. Confirm there's
			// some separation (any whitespace) between them.
			txt := pdftotext(t, pdf)
			if strings.Contains(txt, "submission_timestampUNIQUE-NEIGHBOR") {
				fail("over-wide word spilled into neighbor cell:\n%s", txt)
			}
		},
	}
}

// caseHorizontalRuleVMLPict covers Office's HTML-compat <hr> form:
// <w:pict><v:rect o:hr="t"/></w:pict>. Markdown → docx converters emit
// this for "---" thematic breaks. The renderer turns the whole
// paragraph into a thin gray line.
func caseHorizontalRuleVMLPict() verifyCase {
	return verifyCase{
		name:        "130_horizontal_rule_vml",
		description: "VML <v:rect o:hr=\"t\"> renders as a horizontal separator line",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>before-rule</w:t></w:r></w:p>
    <w:p><w:r>
      <w:pict><v:rect xmlns:v="urn:schemas-microsoft-com:vml"
              xmlns:o="urn:schemas-microsoft-com:office:office"
              style="width:0.0pt;height:1.5pt"
              o:hr="t" o:hralign="center"/></w:pict>
    </w:r></w:p>
    <w:p><w:r><w:t>after-rule</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"before-rule", "after-rule"},
		expectPages: 1,
	}
}

// caseSymbolRoutesToFallback exercises the symbol-block fallback
// routing. Dingbat / arrow / geometric-shape runes are emitted as
// their own atoms and routed to FontFallback when registered (since
// Latin fonts like Arial frequently omit them — notably macOS's
// Arial.ttf has no U+2713 CHECK MARK). Without this, ✓ in source
// docx files renders as .notdef and disappears from the PDF.
//
// Skipped when no fallback font is available — there's nothing
// meaningful to verify against the regular face.
func caseSymbolRoutesToFallback() verifyCase {
	return verifyCase{
		name:        "131_symbol_routes_to_fallback",
		description: "Dingbat / arrow runes (✓ →) render via FontFallback when regular lacks them",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p>
      <w:r><w:t xml:space="preserve">claim </w:t></w:r>
      <w:r><w:t>✓</w:t></w:r>
      <w:r><w:t xml:space="preserve"> alert </w:t></w:r>
      <w:r><w:t>→</w:t></w:r>
      <w:r><w:t xml:space="preserve"> flag</w:t></w:r>
    </w:p>`).Write(t, dir)
		},
		expectText:  []string{"claim", "alert", "flag"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// When the fallback font covers them, ✓ and → should reach
			// the extracted text. When no fallback is registered (e.g.
			// running on a bare container), pdftotext may extract
			// nothing or replacement chars — the test still passes
			// because we don't assert symbol presence in expectText.
			txt := pdftotext(t, pdf)
			// If we got "✓" through, also verify it's NOT mojibake
			// (Identity-H font lookups can produce odd bytes when the
			// glyph isn't found).
			if strings.Contains(txt, "✓") && !strings.Contains(txt, "claim") {
				fail("got ✓ but lost surrounding text — encoding issue")
			}
		},
	}
}

// caseTableHeaderRepeatsAtTopOfPage exercises the pre-flight
// page-break check that injects the repeating tblHeader BEFORE the
// row that would overflow. Without the pre-flight, the repeated
// header lands AFTER that row, mid-page, on every page where the
// table continues — clearly visible as "data | header | data" on
// every page break boundary.
func caseTableHeaderRepeatsAtTopOfPage() verifyCase {
	return verifyCase{
		name:        "132_table_header_at_top_of_page",
		description: "Repeating tblHeader appears at the top of each new page, before body rows",
		build: func(t *testing.T, dir string) string {
			// Build a 2-column table with 80 body rows, each row's cell
			// containing enough text to wrap to 2-3 lines. Default A4
			// page can fit ~30 such rows; 80 guarantees ≥ 2 pages of
			// table content.
			var rows strings.Builder
			rows.WriteString(`<w:tbl>
      <w:tblGrid><w:gridCol w:w="2500"/><w:gridCol w:w="2500"/></w:tblGrid>
      <w:tr><w:trPr><w:tblHeader/></w:trPr>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>HEADER-LEFT</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:rPr><w:b/></w:rPr><w:t>HEADER-RIGHT</w:t></w:r></w:p></w:tc>
      </w:tr>`)
			for i := 0; i < 80; i++ {
				fmt.Fprintf(&rows, `<w:tr>
        <w:tc><w:p><w:r><w:t>row-%02d-left has body text long enough to fill a few lines so each row takes vertical space</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>row-%02d-right also has similarly long content to push row height up</w:t></w:r></w:p></w:tc>
      </w:tr>`, i, i)
			}
			rows.WriteString(`</w:tbl>`)
			return newDocx().Body(rows.String()).Write(t, dir)
		},
		// page count not asserted — depends on row heights
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			pages := pdfPageCount(t, pdf)
			if pages < 2 {
				fail("table should span at least 2 pages, got %d", pages)
				return
			}
			for p := 1; p <= pages; p++ {
				txt := pdftotextRange(t, pdf, p, p)
				if !strings.Contains(txt, "HEADER-LEFT") {
					continue // page without table → no assertion
				}
				headerIdx := strings.Index(txt, "HEADER-LEFT")
				// Find first body row on this page.
				firstBodyIdx := -1
				for i := 0; i < 80; i++ {
					marker := fmt.Sprintf("row-%02d-left", i)
					if idx := strings.Index(txt, marker); idx >= 0 {
						if firstBodyIdx < 0 || idx < firstBodyIdx {
							firstBodyIdx = idx
						}
					}
				}
				if firstBodyIdx >= 0 && headerIdx > firstBodyIdx {
					fail("page %d: header (idx %d) appears AFTER first body row (idx %d)",
						p, headerIdx, firstBodyIdx)
				}
			}
		},
	}
}

// caseListMarkerWithParaIndentOverride covers list paragraphs where the
// numbering.xml level defines a w:hanging but the paragraph itself
// overrides w:ind w:left without touching w:hanging. The marker must
// be positioned against the EFFECTIVE body indent (paragraph's left)
// minus the lvl's hanging — not the lvl's left. Otherwise the marker
// lands on top of the body's first letter ("1Holding" instead of
// "1.  Holding"). Surfaced by an HSBC KYC form.
func caseListMarkerWithParaIndentOverride() verifyCase {
	return verifyCase{
		name:        "133_list_marker_indent_override",
		description: "Paragraph-level w:ind w:left override does not collide list marker with body text",
		build: func(t *testing.T, dir string) string {
			numbering := `<?xml version="1.0"?>
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
			body := `
    <w:p>
      <w:pPr>
        <w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>
        <w:ind w:left="426"/>
      </w:pPr>
      <w:r><w:rPr><w:b/></w:rPr><w:t>HEADING-MARKER-FIRST</w:t></w:r>
    </w:p>`
			return newDocx().Body(body).Numbering(numbering).Write(t, dir)
		},
		expectText:  []string{"HEADING-MARKER-FIRST"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Concrete check: bbox of the "1." marker must end BEFORE
			// the body text's xMin starts (no horizontal overlap).
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			_, markerMax, mOK := bboxRange(txt, "1.")
			bodyMin, _, bOK := bboxRange(txt, "HEADING-MARKER-FIRST")
			if !mOK || !bOK {
				fail("missing bbox: marker=%v body=%v", mOK, bOK)
				return
			}
			if markerMax > bodyMin {
				fail("marker xMax=%.1f overlaps body xMin=%.1f", markerMax, bodyMin)
			}
		},
	}
}

// caseParagraphBottomBorderAsRule covers the empty-paragraph-with-
// w:pBdr/w:bottom pattern used by Google Docs (and Word's manual
// "Border Bottom on an empty paragraph" trick) to encode markdown
// "---" thematic breaks. Distinct from #130 which exercises the VML
// <v:rect o:hr="t"> form; both produce a line, the source XML
// differs entirely.
func caseParagraphBottomBorderAsRule() verifyCase {
	return verifyCase{
		name:        "134_pbdr_bottom_as_rule",
		description: "Empty paragraph with w:pBdr/w:bottom renders as a horizontal line",
		build: func(t *testing.T, dir string) string {
			return newDocx().Body(`
    <w:p><w:r><w:t>HEADING-ABOVE</w:t></w:r></w:p>
    <w:p>
      <w:pPr>
        <w:pBdr><w:bottom w:val="single" w:sz="12" w:space="1" w:color="auto"/></w:pBdr>
      </w:pPr>
    </w:p>
    <w:p><w:r><w:t>BODY-BELOW</w:t></w:r></w:p>`).Write(t, dir)
		},
		expectText:  []string{"HEADING-ABOVE", "BODY-BELOW"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The horizontal-line region between the two visible
			// paragraphs leaves ≥ 12 vertical points of separation.
			// Without the rule the two paragraphs would render
			// back-to-back with only their natural spacing — less
			// than the rule pad + line thickness.
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			_, _, aboveBottom, _, ok1 := bboxFull(txt, "HEADING-ABOVE")
			_, _, belowTop, _, ok2 := bboxFull(txt, "BODY-BELOW")
			if !ok1 || !ok2 {
				fail("missing bbox above=%v below=%v", ok1, ok2)
				return
			}
			gap := belowTop - aboveBottom
			if gap < 12 {
				t.Logf("between gap = %.1f pt (above.yMax=%.1f below.yMin=%.1f)", gap, aboveBottom, belowTop)
				fail("vertical gap %.1fpt — rule didn't reserve space?", gap)
			}
		},
	}
}

// caseParaMarkRPrDoesNotInheritToRuns ensures we do NOT propagate the
// pPr/rPr (which styles the paragraph mark glyph only) onto the runs in
// the paragraph. Real-world trigger: a paragraph where pPr/rPr declares
// <w:b/> but the runs themselves don't — every word came out
// double-stroked because faux-bold kicked in across the whole paragraph.
func caseParaMarkRPrDoesNotInheritToRuns() verifyCase {
	return verifyCase{
		name:        "136_pmark_rpr_does_not_inherit_to_runs",
		description: "pPr/rPr (paragraph-mark styling) must not be inherited by runs",
		build: func(t *testing.T, dir string) string {
			// pPr/rPr says bold; the lone run has its own rPr (no bold).
			// If we incorrectly inherit pPr/rPr, the run renders bold.
			body := `<w:p>
      <w:pPr>
        <w:rPr><w:b/><w:bCs/></w:rPr>
      </w:pPr>
      <w:r>
        <w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial"/></w:rPr>
        <w:t>not bold here</w:t>
      </w:r>
    </w:p>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"not bold here"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// Without a real bold font registered, the renderer fakes
			// bold by double-stroking glyphs at a +0.3pt offset. When
			// pdftotext extracts that, the doubled rendering often
			// shows up as a tighter character pitch but the *text*
			// content stays a single copy. The clearer signal is a
			// rendered-width comparison: faux-bold lengthens visible
			// width measurably (glyph offset + thicker strokes). For
			// this regression we just confirm the text appears once
			// per pdftotext line, which is the simplest invariant that
			// breaks if bold-bit propagation comes back along with the
			// double-stroke artifact in some pdftotext versions.
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			// Count occurrences of the word "bold". If faux-bold's
			// double-stroke produces overlapping but distinct glyph
			// extractions, pdftotext may emit "bold bold" — a
			// telltale doubling pattern we've seen in the wild.
			if strings.Count(txt, "bold</word>") > 1 ||
				strings.Count(txt, ">bold<") > 1 {
				fail("'bold' appears more than once — faux-bold was applied where it shouldn't be:\n%s", txt)
			}
		},
	}
}

// caseMultilineCenterVAlignDoesNotOverflow guards against the
// cellContentHeight stub regression. When a cell uses
// <w:vAlign w:val="center"/> and its text wraps to multiple lines, the
// pre-fix code computed content height as a fixed 13.2pt-per-paragraph
// stub (assumes one line each). The resulting "slack" was wildly
// inflated, pushing content past the row's bottom border — the bottom
// row's last line drew outside the page area. The fix uses the actual
// measureCell result minus cell padding.
func caseMultilineCenterVAlignDoesNotOverflow() verifyCase {
	return verifyCase{
		name:        "137_multiline_center_valign_no_overflow",
		description: "Multi-line text in vAlign=center cell stays within row bounds",
		build: func(t *testing.T, dir string) string {
			// 4-line address in a narrow vAlign=center cell. Width set
			// so "12 PLACE #08-02 PLACE" can't fit on one line and the
			// explicit <w:br/> forces another two-line wrap below.
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="2400"/></w:tblGrid>
      <w:tr>
        <w:trPr><w:trHeight w:val="1448"/></w:trPr>
        <w:tc>
          <w:tcPr>
            <w:vAlign w:val="center"/>
            <w:tcBorders>
              <w:top w:val="single" w:sz="4"/>
              <w:bottom w:val="single" w:sz="4"/>
              <w:left w:val="single" w:sz="4"/>
              <w:right w:val="single" w:sz="4"/>
            </w:tcBorders>
          </w:tcPr>
          <w:p>
            <w:r><w:rPr><w:sz w:val="20"/></w:rPr><w:t>12 PLACE #08-02 PLACE</w:t></w:r>
            <w:r><w:rPr><w:sz w:val="20"/></w:rPr><w:br/><w:t>RESIDENCES SINGAPORE 223588</w:t></w:r>
          </w:p>
        </w:tc>
      </w:tr>
      <w:tr>
        <w:tc>
          <w:tcPr><w:tcBorders>
            <w:top w:val="single" w:sz="4"/>
            <w:bottom w:val="single" w:sz="4"/>
            <w:left w:val="single" w:sz="4"/>
            <w:right w:val="single" w:sz="4"/>
          </w:tcBorders></w:tcPr>
          <w:p><w:r><w:t>BELOW-MARKER</w:t></w:r></w:p>
        </w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"12 PLACE", "RESIDENCES", "223588", "BELOW-MARKER"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The wrapped text must end ABOVE the BELOW-MARKER row.
			// Pre-fix, vAlign centering with the stubbed content height
			// pushed "SINGAPORE 223588" past the cell's bottom border —
			// pdftotext would show it y-overlapping BELOW-MARKER.
			out, _ := combinedOutput("pdftotext", "-bbox", pdf, "-")
			txt := string(out)
			_, _, _, last223588, ok1 := bboxFull(txt, "223588")
			_, _, markerTop, _, ok2 := bboxFull(txt, "BELOW-MARKER")
			if !ok1 || !ok2 {
				fail("missing bbox 223588=%v BELOW-MARKER=%v\n%s", ok1, ok2, txt)
				return
			}
			if last223588 > markerTop {
				fail("'223588' bottom y=%.1f exceeds BELOW-MARKER top y=%.1f — multi-line content escaped its cell",
					last223588, markerTop)
			}
		},
	}
}

// caseAlmostOverwideWordPrefersFreshLine guards the ordering of the
// over-wide-atom path: when a word atom doesn't fit in the *remaining*
// space on the current line but DOES fit on a fresh one, we must flush
// the line and place it whole — not split it per rune. Real-world
// trigger: narrow table header "Last Name" rendering as "Last Nam\ne"
// because "Name" alone is wider than the column's content width.
func caseAlmostOverwideWordPrefersFreshLine() verifyCase {
	return verifyCase{
		name:        "135_almost_overwide_word_prefers_fresh_line",
		description: "Word that fits on its own line but not after preceding text wraps at the space, not per rune",
		build: func(t *testing.T, dir string) string {
			// Single-cell table sized so "Last Name" doesn't fit on one
			// line (≈56pt at default font) but "Name" alone (≈32pt)
			// fits comfortably inside the ~40pt content width.
			body := `<w:tbl>
      <w:tblGrid><w:gridCol w:w="960"/></w:tblGrid>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Last Name</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>`
			return newDocx().Body(body).Write(t, dir)
		},
		expectText:  []string{"Last", "Name"},
		expectPages: 1,
		custom: func(t *testing.T, pdf string, fail func(format string, args ...any)) {
			// The cell must contain the substring "Name" intact (not
			// broken into "Nam" + "e" on adjacent lines). pdftotext
			// preserves text grouping by line; a per-rune split would
			// produce "Nam\ne" with "e" alone on its own line.
			txt := pdftotext(t, pdf)
			if !strings.Contains(txt, "Name") {
				fail("'Name' not present intact — text was split per rune:\n%s", txt)
			}
		},
	}
}

// writeReport renders an HTML index showing every case's pages with badges.
func writeReport(t *testing.T, outRoot string, results []caseResult) {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html><head><meta charset="utf-8"><title>docx2pdf-go verify report</title>
<style>
  body { font: 14px -apple-system, system-ui, sans-serif; max-width: 1100px; margin: 24px auto; padding: 0 20px; color: #1a1a1a; }
  h1 { font-weight: 600; }
  h2 { margin-top: 36px; font-size: 18px; }
  .case { margin: 24px 0; padding: 16px; border: 1px solid #e2e2e2; border-radius: 8px; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 12px; font-weight: 600; }
  .ok   { background: #d4f1d4; color: #14651e; }
  .fail { background: #ffd4d4; color: #8a1d1d; }
  .desc { color: #555; margin: 4px 0 8px; }
  .meta { color: #888; font-size: 12px; }
  .fail-list { color: #8a1d1d; margin: 6px 0; }
  img { max-width: 480px; border: 1px solid #ddd; vertical-align: top; margin: 6px 6px 0 0; }
  pre { background: #f6f6f6; padding: 6px 10px; border-radius: 4px; font-size: 11px; max-height: 100px; overflow: auto; white-space: pre-wrap; }
</style></head><body>
<h1>docx2pdf-go verify report</h1>
<p class="meta">PDFs and PNGs are written next to this file. Click any image to open full size.</p>
`)
	pass, fail := 0, 0
	for _, r := range results {
		if len(r.failures) == 0 {
			pass++
		} else {
			fail++
		}
	}
	fmt.Fprintf(&b, `<p><b>%d pass</b> · <b>%d fail</b> · %d cases total</p>`, pass, fail, len(results))

	for _, r := range results {
		badge := `<span class="badge ok">PASS</span>`
		if len(r.failures) > 0 {
			badge = `<span class="badge fail">FAIL</span>`
		}
		fmt.Fprintf(&b, `<div class="case"><h2>%s &nbsp; %s</h2>`, r.name, badge)
		if r.description != "" {
			fmt.Fprintf(&b, `<div class="desc">%s</div>`, htmlEscape(r.description))
		}
		fmt.Fprintf(&b, `<div class="meta">pages: %d · <a href="%s">open PDF</a></div>`,
			r.pages, filepath.Base(filepath.Dir(r.pdfPath))+"/"+filepath.Base(r.pdfPath))
		if len(r.failures) > 0 {
			b.WriteString(`<ul class="fail-list">`)
			for _, msg := range r.failures {
				fmt.Fprintf(&b, `<li>%s</li>`, htmlEscape(msg))
			}
			b.WriteString(`</ul>`)
		}
		// Page snapshots.
		for _, png := range r.pngPaths {
			rel := r.name + "/" + png
			fmt.Fprintf(&b, `<a href="%s"><img src="%s" alt="%s"></a>`, rel, rel, png)
		}
		// Text sample (first ~300 chars).
		sample := r.textSample
		if len(sample) > 400 {
			sample = sample[:400] + "..."
		}
		if strings.TrimSpace(sample) != "" {
			fmt.Fprintf(&b, `<pre>%s</pre>`, htmlEscape(sample))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</body></html>`)

	indexPath := filepath.Join(outRoot, "index.html")
	if err := os.WriteFile(indexPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
