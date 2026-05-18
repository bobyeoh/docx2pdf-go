package render

import (
	"image"
	"strconv"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func (r *renderer) drawParagraph(p docx.Paragraph) error {
	// Drop any wrap-around band that has been outgrown by the cursor —
	// otherwise paragraphs after the floating image start with stale
	// narrowing.
	r.clearExpiredFloatBand()
	// Positioned frame (w:framePr with placement attrs): render the
	// paragraph in its anchored absolute position without touching the
	// document flow. Surrounding body text is NOT reflowed around the
	// frame, so a w:wrap="around" frame may visually overlap — matching
	// docx4j's "absolute-positioned-block" behavior. drawFrame returns
	// nil for nothing-to-do (zero-width frames or missing geometry).
	if p.Frame != nil {
		return r.drawFrame(p)
	}
	// Horizontal rule paragraph. Word encodes "---" thematic breaks two
	// different ways depending on the converter:
	//   - <w:pict><v:rect o:hr="t"/></w:pict>   ← markdown via Word
	//   - empty paragraph with <w:pBdr><w:bottom .../>  ← Google Docs,
	//     and Word's own "Border Bottom" formatting on an empty para
	// Both produce a thin horizontal line; isHorizontalRuleParagraph
	// detects either form. Real content with a bottom border (e.g.
	// section dividers under headings) falls through to the normal
	// layout, with the border drawn at the bottom of the paragraph.
	if isHorizontalRuleParagraph(p) {
		r.drawHorizontalRule(p)
		return nil
	}
	if p.PageBreak {
		r.pdf.AddPage()
		r.cursorY = r.marT
	}
	// PDF outline: paragraphs styled as Heading1..9 or Title contribute
	// a bookmark in the reader's sidebar so the resulting PDF has a
	// clickable navigable structure. The text of the first paragraph at
	// each heading level becomes the bookmark title. We add the outline
	// here (before content draws) so it points at the correct page —
	// note that any preceding spacing-before or KeepNext ensureRoom may
	// still shift the page; close enough for "click to jump near
	// chapter".
	if title := headingTitle(p); title != "" {
		// Heading level → indent in the PDF outline. gopdf doesn't model
		// a real nested-outline tree (only a flat list), so we approximate
		// hierarchy by prefixing N-1 figure-spaces per level. The
		// resulting sidebar reads as Heading 1 > Heading 2 > ... without
		// requiring upstream support for sub-outlines.
		level := outlineLevelFromStyle(p.StyleID, p.OutlineLvl)
		if level > 1 {
			title = strings.Repeat("  ", level-1) + title
		}
		r.pdf.AddOutline(title)
		// TOC discovery hook: when the renderer is running its
		// first pass via renderWithTOC, this callback records the
		// page number each heading lands on so we can populate the
		// auto-generated TOC entries with live page numbers on the
		// second pass. Nil in the normal render path.
		if r.opts.onHeadingPage != nil {
			r.opts.onHeadingPage(title, p.StyleID, r.pdf.GetNumberOfPages())
		}
	}
	// RTL paragraph state: drives rune-reversal inside RTL word atoms and
	// line-internal atom reversal at flush time. Set before runsToAtoms so
	// atom construction sees it.
	r.paragraphRTL = p.Bidi
	// East Asian line-break rules default to ON unless the paragraph
	// explicitly turned them off via w:kinsoku w:val="false".
	r.paragraphKinsoku = p.Kinsoku == nil || *p.Kinsoku
	// w:overflowPunct (default ON in Word) controls whether trailing
	// punctuation may overhang the right margin instead of forcing a
	// wrap. Our Kinsoku no-start logic IS the overhang implementation,
	// so this flag effectively turns it off when explicitly false.
	r.paragraphOverflowPunct = p.OverflowPunct == nil || *p.OverflowPunct
	// w:wordWrap (default ON) allows Latin mid-word breaking when a long
	// word doesn't fit. When false, the long word overflows the margin
	// rather than being split — typical for headings.
	r.paragraphWordWrap = p.WordWrap == nil || *p.WordWrap
	defer func() {
		r.paragraphRTL = false
		r.paragraphKinsoku = false
		r.paragraphOverflowPunct = true
		r.paragraphWordWrap = true
	}()
	// RTL paragraphs whose alignment was not explicit default to right.
	// The parser's AlignLeft is the zero value and indistinguishable from
	// "not set", so we err on the side of doing the natural thing for
	// bidi=on paragraphs; explicit center/right/justify pass through.
	align := p.Alignment
	if p.Bidi && align == docx.AlignLeft {
		align = docx.AlignRight
	}
	// Contextual spacing: suppress before-spacing if the previous paragraph
	// shared the same style (typical for list items, body-text).
	sb := p.SpacingBefore
	if p.ContextualSpacing && p.StyleID != "" && p.StyleID == r.prevStyleID {
		sb = 0
	}
	if sb > 0 {
		r.cursorY += sb
	}
	// keepNext/keepLines: reserve a line of next-paragraph height so an
	// orphan line doesn't end the page right after a keepNext heading.
	if p.KeepNext || p.KeepLines {
		r.ensureRoom(r.opts.DefaultFontSize * 2.4)
	}
	savedTabs := r.activeTabs
	r.activeTabs = p.Tabs
	defer func() { r.activeTabs = savedTabs }()

	// Lists get an extra left indent that applies to every line; the marker
	// is hung to the left of that indent via pendingMarker so flush() draws
	// it at the first line's baseline.
	//
	// Word semantics: paragraph-level w:ind overrides numbering.xml indent
	// for the same list item rather than stacking. Marker position MUST be
	// computed against the EFFECTIVE body indent (paragraph override if
	// any, else the lvl's value) — using the lvl's value when the
	// paragraph overrode w:left makes the marker overlap the body text
	// when the paragraph chose a smaller indent.
	var listMarkerText string
	var listMarkerImg image.Image
	var listMarkerFont string
	var listMarkerJc string
	var lvlHangPt float64
	listIndent := 0.0
	if p.List != nil {
		markerText, markerImg, indentPt, hangPt, font, jc := r.resolveListMarker(*p.List)
		listIndent = indentPt
		listMarkerText = markerText
		listMarkerImg = markerImg
		listMarkerFont = font
		listMarkerJc = jc
		lvlHangPt = hangPt
	}

	if len(p.Runs) == 0 && p.List == nil {
		size := r.opts.DefaultFontSize
		r.ensureRoom(size * 1.2)
		r.cursorY += size * 1.2
		return nil
	}

	// w:pBdr top/left/right/bottom — paint a frame around the paragraph
	// content. Top is drawn before content starts; verticals + bottom
	// after content finishes (we capture the paragraph's start-y now
	// and the end-y after layout). The "bottom only on an empty para"
	// HR case was already handled above via isHorizontalRuleParagraph.
	pbdrTopY := r.cursorY

	// Effective body indent: paragraph override beats lvl value.
	leftIndent := listIndent
	if p.IndentLeftPt > 0 {
		leftIndent = p.IndentLeftPt
	}

	// Now place the marker. markerX sits leftIndent − hangPt to the left
	// of the original margin, which is what Word does even when the
	// paragraph overrides w:left without touching w:hanging.
	if p.List != nil && (listMarkerText != "" || listMarkerImg != nil) {
		markerX := r.marL + leftIndent - lvlHangPt
		if markerX < r.marL {
			markerX = r.marL
		}
		r.pendingMarker = &pendingMarker{
			text:       listMarkerText,
			image:      listMarkerImg,
			x:          markerX,
			fontFamily: listMarkerFont,
			jc:         listMarkerJc,
			colWidth:   lvlHangPt,
		}
	}

	savedColIdx := r.colIdx
	savedMarL, savedContentW, savedLine := r.marL, r.contentW, r.lineHeight
	if leftIndent > 0 {
		r.marL += leftIndent
		r.contentW -= leftIndent
	}
	r.lineHeight = p.LineHeight

	var atoms []atom
	if p.IndentFirstLinePt > 0 {
		atoms = append(atoms, atom{kind: atomSpace, width: p.IndentFirstLinePt})
	} else if p.IndentFirstLinePt < 0 && p.List == nil {
		// Hanging indent: first physical line starts further left and is
		// proportionally wider. Carried as renderer state so layoutLine can
		// apply it on the very first flush only.
		//
		// Skip when this paragraph has a list marker. Word duplicates the
		// "hanging" geometry on both the abstractNum lvl AND the
		// paragraph's pPr/w:ind for legacy-reader compatibility; applying
		// both would shift the first line into the marker column and
		// render content overlapping the marker (visible as a black dot
		// next to each list item's first letter).
		r.firstLineHangPt = -p.IndentFirstLinePt
	}
	runs := p.Runs
	if p.DropCap != "" {
		runs = applyDropCap(runs, p.DropCapLines)
	}
	atoms = append(atoms, r.runsToAtoms(runs)...)

	if err := r.layoutLine(atoms, align); err != nil {
		r.restoreParagraphState(savedColIdx, savedMarL, savedContentW, savedLine)
		r.pendingMarker = nil
		r.firstLineHangPt = 0
		return err
	}
	r.restoreParagraphState(savedColIdx, savedMarL, savedContentW, savedLine)
	r.pendingMarker = nil
	r.firstLineHangPt = 0

	// Paint full pBdr (top/left/right/bottom) around the content area
	// just laid out. Bottom is drawn BEFORE SpacingAfter so the rule
	// hugs the text. The HR-only case was handled earlier.
	pbdrBottomY := r.cursorY
	r.drawParagraphFrameBorders(p, pbdrTopY, pbdrBottomY)
	// Revision change bar: in show-revisions mode, paint a 1pt vertical
	// rule in the left margin next to any paragraph that contains a
	// tracked-change run (ins/del/moveFrom/moveTo). This matches Word's
	// default "Change bars" balloon-margin convention, except we draw a
	// paragraph-spanning bar rather than per-line (we don't know the
	// per-line revision mask at this layer).
	if r.opts.ShowRevisions && paragraphHasRevision(p) {
		r.drawRevisionChangeBar(pbdrTopY, pbdrBottomY)
	}
	// w:suppressLineNumbers — record the y-range so the section's
	// line-number gutter skips this paragraph. Effective only when the
	// section enables w:lnNumType; otherwise harmless.
	if p.SuppressLineNumbers && pbdrBottomY > pbdrTopY {
		r.suppressedLineNumRanges = append(r.suppressedLineNumRanges, suppressedRange{
			top:    pbdrTopY,
			bottom: pbdrBottomY,
		})
	}

	if p.SpacingAfter > 0 {
		r.cursorY += p.SpacingAfter
	}
	r.prevStyleID = p.StyleID
	return nil
}

// drawParagraphFrameBorders paints any combination of top / bottom /
// left / right edges declared in w:pBdr. The four edges share the same
// BorderEdge shape as table cells (width Sz in pt, hex Color, style).
func (r *renderer) drawParagraphFrameBorders(p docx.Paragraph, topY, bottomY float64) {
	b := p.Borders
	if !(b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has()) {
		return
	}
	leftX := r.marL
	rightX := r.marL + r.contentW
	if b.Top.Has() {
		drawCellEdge(r, b.Top, leftX, topY, rightX, topY)
	}
	if b.Bottom.Has() {
		drawCellEdge(r, b.Bottom, leftX, bottomY, rightX, bottomY)
	}
	if b.Left.Has() {
		drawCellEdge(r, b.Left, leftX, topY, leftX, bottomY)
	}
	if b.Right.Has() {
		drawCellEdge(r, b.Right, rightX, topY, rightX, bottomY)
	}
}

// restoreParagraphState rolls back marL/contentW/lineHeight to the values
// captured at paragraph start, EXCEPT when a column-advance happened mid-
// paragraph. In that case the column position persists and the next
// paragraph picks up where we left off.
func (r *renderer) restoreParagraphState(savedColIdx int, savedMarL, savedContentW float64, savedLine docx.LineHeight) {
	r.lineHeight = savedLine
	if r.colIdx != savedColIdx {
		r.applyColumn(r.colIdx)
		return
	}
	r.marL = savedMarL
	r.contentW = savedContentW
}

// resolveListMarker returns the marker text/image to draw, the left indent
// in points (applied to the whole paragraph), and the marker-to-content gap.
//
// Returns empty values when the list isn't defined — some Word docs
// reference numIds that aren't in numbering.xml, and we fall back gracefully
// rather than failing the render.
func (r *renderer) resolveListMarker(li docx.ListInfo) (marker string, img image.Image, indentPt, hangPt float64, fontFamily, markerJc string) {
	absID, ok := r.doc.Numbering.NumToAbs[li.NumID]
	if !ok {
		return "", nil, 0, 0, "", ""
	}
	an, ok := r.doc.Numbering.Abstract[absID]
	if !ok {
		return "", nil, 0, 0, "", ""
	}
	lv, ok := an.Levels[li.Level]
	if !ok {
		return "", nil, 0, 0, "", ""
	}

	// w:lvlOverride lives on the w:num, not the abstractNum. A num that
	// inherits a shared abstract can either: replace the whole level
	// definition (w:lvl child of w:lvlOverride), or override just the
	// initial counter (w:startOverride).
	var startOverride int
	if numOv, ok := r.doc.Numbering.Overrides[li.NumID]; ok {
		if ov, ok := numOv[li.Level]; ok {
			if ov.LvlReplace != nil {
				lv = *ov.LvlReplace
			}
			if ov.StartOverride > 0 {
				startOverride = ov.StartOverride
			}
		}
	}

	// Counter state is keyed by numId (not abstractNumId) so that distinct
	// w:num records — even when sharing an abstractNum — keep independent
	// counters. Word's "continuation" lists explicitly share the same numId,
	// so this also preserves the legacy continuation behavior.
	counterKey := li.NumID
	if r.counters[counterKey] == nil {
		r.counters[counterKey] = map[int]int{}
	}
	startVal := lv.Start
	if startOverride > 0 {
		startVal = startOverride
	}
	if _, seen := r.counters[counterKey][li.Level]; !seen {
		r.counters[counterKey][li.Level] = startVal
	} else {
		r.counters[counterKey][li.Level]++
	}
	// Default rule (OOXML §17.9.7): when a level appears, every higher-indent
	// level (ilvl > current) resets next time it appears. A non-default
	// w:lvlRestart on a higher level overrides this:
	//   lvlRestart == 0       — never restart (don't delete)
	//   lvlRestart  > 0       — restart only when ancestor at val advances;
	//                            when WE just advanced level li.Level, only
	//                            child-levels whose lvlRestart points at
	//                            li.Level (or unset/<0) get reset.
	for k, child := range an.Levels {
		if k <= li.Level {
			continue
		}
		// Default: reset whenever any higher level advances.
		if child.LvlRestart < 0 {
			delete(r.counters[counterKey], k)
			continue
		}
		// Explicit "never restart".
		if child.LvlRestart == 0 {
			continue
		}
		// Explicit restart trigger: ancestor index (1-based in OOXML).
		// val=N means restart when level (N-1) advances. We just advanced
		// level li.Level, so restart only if li.Level == child.LvlRestart-1.
		if li.Level == child.LvlRestart-1 {
			delete(r.counters[counterKey], k)
		}
	}

	indentPt = twipsToPt(lv.LeftTwips)
	hangPt = twipsToPt(lv.HangingTwips)
	// Defaults when the level definition omits indent metadata (some
	// minimal numbering.xml files do): emulate Word's default of
	// 720 twips body indent with a 360 twips hanging marker. Without
	// this, marker and text would overlap at the paragraph's left
	// margin since both end up at x = marL.
	if indentPt == 0 {
		indentPt = 36 // 720 twips = 0.5 inch
	}
	if hangPt < 2 {
		hangPt = 18 // 360 twips = 0.25 inch — enough gap between marker and text
	}

	markerJc = lv.MarkerJc
	if lv.PicBulletID > 0 {
		if rid, ok := r.doc.Numbering.PicBullets[lv.PicBulletID]; ok {
			if image, ok := r.doc.Images[rid]; ok {
				return "", image, indentPt, hangPt, "", markerJc
			}
		}
	}

	marker = formatLevelTextAt(lv, an.Levels, r.counters[counterKey], li.Level)
	// Choose a font hint for the marker. Bullet levels often carry a
	// w:rFonts pointing at "Symbol" / "Wingdings"; only honor it for
	// bullet markers — for numeric markers the body font is correct.
	if lv.Format == "bullet" && lv.MarkerFontFamily != "" {
		fontFamily = lv.MarkerFontFamily
	}
	// w:suff controls what separates the marker from the body:
	//   "tab"     — default: tab to next tabstop (we approximate with hangPt)
	//   "space"   — one space-width gap (keep hangPt as-is; minor over-pad
	//                acceptable vs. visible overlap with body)
	//   "nothing" — body text starts immediately after the marker. Collapse
	//                hangPt so the body indent equals the marker position.
	if lv.Suff == "nothing" {
		hangPt = 0
		// Re-anchor indent at the marker's natural left edge: same as
		// lv.LeftTwips, no extra hanging gap.
		if twipsToPt(lv.LeftTwips) == 0 {
			indentPt = 0
		}
	}
	return marker, nil, indentPt, hangPt, fontFamily, markerJc
}

// formatLevelText expands lvlText placeholders like "%1.%2" using the
// current counter map. For bullets, lvlText is taken literally.
//
// Each %N references the counter for ilvl=N-1 and must be rendered in
// THAT level's numFmt, not the current level's. When the current level
// declares w:isLgl, every substitution is forced to decimal (Word's
// "legal numbering" mode used for "1.2.3.4" outlines that mix Roman
// and Arabic per-level formats).
func formatLevelText(lv docx.NumLevel, allLevels map[int]docx.NumLevel, counters map[int]int) string {
	return formatLevelTextAt(lv, allLevels, counters, -1)
}

// formatLevelTextAt is the same as formatLevelText but knows the current
// ilvl so it can honor w:hideParent (which suppresses %N placeholders for
// ancestor levels — ilvl < currentLevel).
func formatLevelTextAt(lv docx.NumLevel, allLevels map[int]docx.NumLevel, counters map[int]int, currentLevel int) string {
	if lv.Format == "bullet" || lv.Format == "" {
		if lv.Text != "" {
			return lv.Text
		}
		return "•"
	}
	out := lv.Text
	if out == "" {
		out = "%1."
	}
	for n := 1; n <= 9; n++ {
		needle := "%" + strconv.Itoa(n)
		if !strings.Contains(out, needle) {
			continue
		}
		// w:hideParent: %N referring to an ancestor (ilvl < currentLevel)
		// is suppressed. Word renders the marker as if the placeholder
		// wasn't there.
		if lv.HideParent && currentLevel >= 0 && (n-1) < currentLevel {
			out = strings.ReplaceAll(out, needle, "")
			continue
		}
		val := counters[n-1]
		fm := ""
		if other, ok := allLevels[n-1]; ok {
			fm = other.Format
		}
		if fm == "" {
			fm = lv.Format
		}
		if lv.IsLgl {
			fm = "decimal"
		}
		out = strings.ReplaceAll(out, needle, formatNumber(val, fm))
	}
	return out
}

func formatNumber(n int, fmtName string) string {
	if n <= 0 {
		n = 1
	}
	switch fmtName {
	case "decimal":
		return strconv.Itoa(n)
	case "decimalZero":
		if n < 10 {
			return "0" + strconv.Itoa(n)
		}
		return strconv.Itoa(n)
	case "decimalHalfWidth":
		return strconv.Itoa(n)
	case "decimalFullWidth", "decimalFullWidth2":
		return fullWidthDigits(n)
	case "lowerLetter":
		return alphaLabel(n, false)
	case "upperLetter":
		return alphaLabel(n, true)
	case "lowerRoman":
		return roman(n, false)
	case "upperRoman":
		return roman(n, true)
	case "none":
		return ""
	case "bullet":
		return "•"
	case "ordinal":
		return ordinalLabel(n)
	case "ordinalText":
		return ordinalText(n)
	case "cardinalText":
		return cardinalText(n)
	case "chineseCounting", "chineseCountingThousand", "ideographDigital":
		return chineseCounting(n)
	case "chineseLegalSimplified":
		return chineseLegalSimplified(n)
	case "ideographTraditional":
		return ideographTraditional(n)
	case "ideographLegalTraditional":
		return ideographLegalTraditional(n)
	case "decimalEnclosedCircle", "decimalEnclosedCircleChinese":
		return decimalEnclosedCircle(n)
	case "decimalEnclosedParen":
		return "(" + strconv.Itoa(n) + ")"
	case "decimalEnclosedFullstop":
		return strconv.Itoa(n) + "."
	case "numberInDash":
		return "- " + strconv.Itoa(n) + " -"
	case "hex":
		return strings.ToUpper(strconv.FormatInt(int64(n), 16))
	case "japaneseCounting", "japaneseDigitalTenThousand":
		return chineseCounting(n)
	case "japaneseLegal":
		return chineseLegalSimplified(n)
	case "koreanCounting", "koreanDigital", "koreanDigital2":
		return koreanCounting(n)
	case "koreanLegal":
		return koreanLegal(n)
	case "aiueo":
		return aiueoLabel(n, false)
	case "aiueoFullWidth":
		return aiueoLabel(n, true)
	case "iroha":
		return irohaLabel(n, false)
	case "irohaFullWidth":
		return irohaLabel(n, true)
	case "thaiNumbers", "thaiCountingThousand", "thaiCounting":
		return thaiDigits(n)
	case "ganada":
		return ganadaLabel(n)
	case "chosung":
		return chosungLabel(n)
	case "arabicAlpha":
		return arabicAlphaLabel(n)
	case "arabicAbjad":
		return arabicAbjadLabel(n)
	case "hindiVowels":
		return hindiVowelsLabel(n)
	case "hindiConsonants":
		return hindiConsonantsLabel(n)
	case "hindiNumbers", "hindiCounting":
		return hindiDigits(n)
	case "hebrew1":
		return hebrew1Label(n)
	case "hebrew2":
		return hebrew2Label(n)
	case "russianLower":
		return russianLetterLabel(n, false)
	case "russianUpper":
		return russianLetterLabel(n, true)
	case "vietnameseCounting":
		return vietnameseCounting(n)
	case "bengaliCounting":
		return bengaliCounting(n)
	case "bengaliNumbers":
		return bengaliDigits(n)
	case "taiwaneseCounting", "taiwaneseCountingThousand", "taiwaneseDigital":
		return ideographTraditional(n)
	case "ideographZodiac":
		return zodiacLabel(n, false)
	case "ideographZodiacTraditional":
		return zodiacLabel(n, true)
	case "thaiLetters":
		return thaiLettersLabel(n)
	case "chicago":
		// Footnote symbol cycle: * † ‡ § ‖ ¶, doubling on each wrap.
		return chicagoLabel(n)
	case "bahtText":
		return bahtText(n)
	case "dollarText":
		return dollarText(n)
	default:
		return strconv.Itoa(n)
	}
}

// chicagoLabel maps n → Chicago Manual of Style footnote symbols.
// Cycle is *, †, ‡, §, ‖, ¶, repeating doubled on overflow (** †† …).
func chicagoLabel(n int) string {
	syms := []string{"*", "†", "‡", "§", "‖", "¶"}
	if n < 1 {
		return ""
	}
	idx := (n - 1) % len(syms)
	rep := (n-1)/len(syms) + 1
	return strings.Repeat(syms[idx], rep)
}

// bahtText spells out a number as Thai currency words (whole baht only,
// no satang). Implements the OOXML numFmt "bahtText" — used in legal
// documents in Thai locales.
func bahtText(n int) string {
	if n == 0 {
		return "ศูนย์บาทถ้วน"
	}
	digits := []string{"", "หนึ่ง", "สอง", "สาม", "สี่", "ห้า", "หก", "เจ็ด", "แปด", "เก้า"}
	positions := []string{"", "สิบ", "ร้อย", "พัน", "หมื่น", "แสน", "ล้าน"}
	if n < 0 {
		return "ลบ" + bahtText(-n)
	}
	if n >= 1_000_000 {
		left := bahtText(n / 1_000_000)
		right := n % 1_000_000
		if right == 0 {
			return left + "ล้านบาทถ้วน"
		}
		// Strip "บาทถ้วน" from the recursive call before concatenating.
		rest := bahtText(right)
		rest = strings.TrimSuffix(rest, "บาทถ้วน")
		return left + "ล้าน" + rest + "บาทถ้วน"
	}
	var b strings.Builder
	s := strconv.Itoa(n)
	L := len(s)
	for i, c := range s {
		d := int(c - '0')
		pos := L - i - 1
		if d == 0 {
			continue
		}
		switch {
		case pos == 0 && L > 1 && d == 1:
			b.WriteString("เอ็ด")
		case pos == 1 && d == 2:
			b.WriteString("ยี่")
			b.WriteString(positions[pos])
		case pos == 1 && d == 1:
			b.WriteString(positions[pos])
		default:
			b.WriteString(digits[d])
			if pos > 0 && pos < len(positions) {
				b.WriteString(positions[pos])
			}
		}
	}
	b.WriteString("บาทถ้วน")
	return b.String()
}


// zodiacLabel returns the Chinese 12-animal zodiac character for n.
// Traditional adds a tick when the cycle wraps past 12.
func zodiacLabel(n int, traditional bool) string {
	cycle := []rune{'鼠', '牛', '虎', '兔', '龍', '蛇', '馬', '羊', '猴', '雞', '狗', '豬'}
	if n < 1 {
		return strconv.Itoa(n)
	}
	idx := (n - 1) % 12
	repeat := (n - 1) / 12
	out := string(cycle[idx])
	if traditional && repeat > 0 {
		out += strings.Repeat("ʹ", repeat)
	}
	return out
}

// thaiLettersLabel cycles through Thai consonants (ก..ฮ).
func thaiLettersLabel(n int) string {
	letters := []rune{
		'ก', 'ข', 'ฃ', 'ค', 'ฅ', 'ฆ', 'ง', 'จ', 'ฉ', 'ช', 'ซ',
		'ฌ', 'ญ', 'ฎ', 'ฏ', 'ฐ', 'ฑ', 'ฒ', 'ณ', 'ด', 'ต', 'ถ',
		'ท', 'ธ', 'น', 'บ', 'ป', 'ผ', 'ฝ', 'พ', 'ฟ', 'ภ', 'ม',
		'ย', 'ร', 'ล', 'ว', 'ศ', 'ษ', 'ส', 'ห', 'ฬ', 'อ', 'ฮ',
	}
	if n < 1 {
		return strconv.Itoa(n)
	}
	idx := (n - 1) % len(letters)
	repeat := (n - 1) / len(letters)
	return strings.Repeat(string(letters[idx]), repeat+1)
}

// hebrew1Label returns the additive hebrew-numeral form for n (1..999).
// Standard ordering א=1, ב=2, ... ת=400. Larger values cascade by
// stacking hundreds → tens → ones.
func hebrew1Label(n int) string {
	if n <= 0 {
		return strconv.Itoa(n)
	}
	ones := []string{"", "א", "ב", "ג", "ד", "ה", "ו", "ז", "ח", "ט"}
	tens := []string{"", "י", "כ", "ל", "מ", "נ", "ס", "ע", "פ", "צ"}
	hundreds := []string{"", "ק", "ר", "ש", "ת"}
	var b strings.Builder
	if n >= 1000 {
		// fall back to numeric for huge values
		return strconv.Itoa(n)
	}
	h := n / 100
	t := (n % 100) / 10
	o := n % 10
	for h > 4 {
		b.WriteString("ת")
		h -= 4
	}
	b.WriteString(hundreds[h])
	switch n % 100 {
	case 15:
		b.WriteString("טו")
	case 16:
		b.WriteString("טז")
	default:
		b.WriteString(tens[t])
		b.WriteString(ones[o])
	}
	return b.String()
}

// hebrew2Label is the "spelled-out" reverse-ordered variant. We use the
// same letters but read RTL — Word's hebrew2 also adds a geresh tick.
func hebrew2Label(n int) string {
	s := hebrew1Label(n)
	if len(s) >= 4 {
		return s + "׳"
	}
	return s
}

// russianLetterLabel cycles through the Cyrillic alphabet, skipping
// reserved letters (Ё, Й, Ъ, Ы, Ь) that Word also skips.
func russianLetterLabel(n int, upper bool) string {
	lower := []rune{'а', 'б', 'в', 'г', 'д', 'е', 'ж', 'з', 'и', 'к', 'л', 'м', 'н',
		'о', 'п', 'р', 'с', 'т', 'у', 'ф', 'х', 'ц', 'ч', 'ш', 'щ', 'э', 'ю', 'я'}
	if n < 1 {
		return strconv.Itoa(n)
	}
	idx := (n - 1) % len(lower)
	repeat := (n - 1) / len(lower)
	c := lower[idx]
	if upper {
		c -= 0x20
	}
	out := strings.Repeat(string(c), repeat+1)
	return out
}

// vietnameseCounting produces traditional Vietnamese number words for
// small n; falls back to digits above 20 (spelling out compounds
// requires a fuller grammar than we model).
func vietnameseCounting(n int) string {
	words := []string{
		"", "Một", "Hai", "Ba", "Bốn", "Năm",
		"Sáu", "Bảy", "Tám", "Chín", "Mười",
		"Mười Một", "Mười Hai", "Mười Ba", "Mười Bốn", "Mười Lăm",
		"Mười Sáu", "Mười Bảy", "Mười Tám", "Mười Chín", "Hai Mươi",
	}
	if n >= 1 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// bengaliDigits maps decimal digits to the Bengali script (০ … ৯).
func bengaliDigits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(0x09E6 + (r - '0'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bengaliCounting is the Bengali-script counting form; we use digits for
// simplicity since spelled-out forms vary regionally.
func bengaliCounting(n int) string {
	return bengaliDigits(n)
}

func fullWidthDigits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(0xFF10 + (r - '0'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func chineseLegalSimplified(n int) string {
	digits := []rune("零壹贰叁肆伍陆柒捌玖")
	if n < 10 {
		return string(digits[n])
	}
	if n < 20 {
		if n == 10 {
			return "拾"
		}
		return "拾" + string(digits[n-10])
	}
	if n < 100 {
		tens, ones := n/10, n%10
		out := string(digits[tens]) + "拾"
		if ones > 0 {
			out += string(digits[ones])
		}
		return out
	}
	return chineseCounting(n)
}

func thaiDigits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(0x0E50 + (r - '0'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hindiDigits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(0x0966 + (r - '0'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func koreanCounting(n int) string {
	digits := []rune("영일이삼사오육칠팔구")
	if n < 10 {
		return string(digits[n])
	}
	if n < 20 {
		if n == 10 {
			return "십"
		}
		return "십" + string(digits[n-10])
	}
	if n < 100 {
		tens, ones := n/10, n%10
		out := string(digits[tens]) + "십"
		if ones > 0 {
			out += string(digits[ones])
		}
		return out
	}
	return strconv.Itoa(n)
}

func koreanLegal(n int) string {
	digits := []rune("영일이삼사오육칠팔구")
	if n < 10 {
		return string(digits[n])
	}
	return koreanCounting(n)
}

func ganadaLabel(n int) string {
	letters := []rune{'가', '나', '다', '라', '마', '바', '사', '아', '자', '차', '카', '타', '파', '하'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

func chosungLabel(n int) string {
	letters := []rune{'ㄱ', 'ㄴ', 'ㄷ', 'ㄹ', 'ㅁ', 'ㅂ', 'ㅅ', 'ㅇ', 'ㅈ', 'ㅊ', 'ㅋ', 'ㅌ', 'ㅍ', 'ㅎ'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

func arabicAlphaLabel(n int) string {
	letters := []rune{'ا', 'ب', 'ت', 'ث', 'ج', 'ح', 'خ', 'د', 'ذ', 'ر', 'ز', 'س', 'ش', 'ص', 'ض',
		'ط', 'ظ', 'ع', 'غ', 'ف', 'ق', 'ك', 'ل', 'م', 'ن', 'ه', 'و', 'ي'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

func arabicAbjadLabel(n int) string {
	letters := []rune{'ا', 'ب', 'ج', 'د', 'ه', 'و', 'ز', 'ح', 'ط', 'ي', 'ك', 'ل', 'م', 'ن', 'س',
		'ع', 'ف', 'ص', 'ق', 'ر', 'ش', 'ت', 'ث', 'خ', 'ذ', 'ض', 'ظ', 'غ'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

func hindiVowelsLabel(n int) string {
	letters := []rune{'अ', 'आ', 'इ', 'ई', 'उ', 'ऊ', 'ऋ', 'ए', 'ऐ', 'ओ', 'औ'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

func hindiConsonantsLabel(n int) string {
	letters := []rune{'क', 'ख', 'ग', 'घ', 'ङ', 'च', 'छ', 'ज', 'झ', 'ञ', 'ट', 'ठ', 'ड', 'ढ', 'ण',
		'त', 'थ', 'द', 'ध', 'न', 'प', 'फ', 'ब', 'भ', 'म', 'य', 'र', 'ल', 'व', 'श', 'ष', 'स', 'ह'}
	if n >= 1 && n <= len(letters) {
		return string(letters[n-1])
	}
	return strconv.Itoa(n)
}

// ordinalLabel returns "1st", "2nd", "3rd", "4th"…
func ordinalLabel(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// ordinalText returns "first", "second"… for small n; falls back to
// "{N}th" beyond 20 since spelling them out would balloon the table.
func ordinalText(n int) string {
	words := []string{
		"", "first", "second", "third", "fourth", "fifth",
		"sixth", "seventh", "eighth", "ninth", "tenth",
		"eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth",
		"sixteenth", "seventeenth", "eighteenth", "nineteenth", "twentieth",
	}
	if n >= 1 && n < len(words) {
		return words[n]
	}
	return ordinalLabel(n)
}

// cardinalText returns "one", "two", "three"… up to twenty; "{N}" beyond.
func cardinalText(n int) string {
	words := []string{
		"", "one", "two", "three", "four", "five",
		"six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty",
	}
	if n >= 1 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// chineseCounting converts 1..9999 into mainland-Chinese counting form
// (一、二、三、十、二十、一百二十三). For values outside that range we
// fall back to the decimal form, since spelling out 10000+ requires
// additional unit characters (万) that the original w:numFmt
// distinguishes between styles.
func chineseCounting(n int) string {
	if n <= 0 {
		return ""
	}
	digits := []rune("零一二三四五六七八九")
	if n < 10 {
		return string(digits[n])
	}
	if n < 20 {
		// 10..19 — "十X" (just 十 for 10; 十一..十九 otherwise).
		if n == 10 {
			return "十"
		}
		return "十" + string(digits[n%10])
	}
	if n < 100 {
		tens := n / 10
		ones := n % 10
		s := string(digits[tens]) + "十"
		if ones != 0 {
			s += string(digits[ones])
		}
		return s
	}
	if n < 1000 {
		h := n / 100
		rest := n % 100
		s := string(digits[h]) + "百"
		if rest == 0 {
			return s
		}
		if rest < 10 {
			return s + "零" + string(digits[rest])
		}
		return s + chineseCounting(rest)
	}
	if n < 10000 {
		k := n / 1000
		rest := n % 1000
		s := string(digits[k]) + "千"
		if rest == 0 {
			return s
		}
		if rest < 100 {
			return s + "零" + chineseCounting(rest)
		}
		return s + chineseCounting(rest)
	}
	return strconv.Itoa(n)
}

// ideographTraditional uses the older Han numerals for 1-10 (壹貳叄...).
func ideographTraditional(n int) string {
	digits := []rune("零壹貳參肆伍陸柒捌玖")
	if n >= 0 && n < len(digits) {
		return string(digits[n])
	}
	return chineseCounting(n)
}

// ideographLegalTraditional uses the traditional banking forms.
func ideographLegalTraditional(n int) string {
	digits := []rune("零壹貳叄肆伍陸柒捌玖")
	if n >= 0 && n < len(digits) {
		return string(digits[n])
	}
	return chineseCounting(n)
}

// decimalEnclosedCircle returns ①..⑳ for 1..20, falling back to plain
// decimals beyond that range since the BMP glyph series stops at 20.
func decimalEnclosedCircle(n int) string {
	if n >= 1 && n <= 20 {
		// U+2460 = ①. Subtract 1 because ① is offset 0.
		return string(rune(0x2460 + n - 1))
	}
	return strconv.Itoa(n)
}

// aiueoLabel returns Japanese hiragana ア,イ,ウ,エ,オ... (or full-width
// variants when fullwidth=true). After the first 5 it repeats the pattern
// — a passable approximation for short lists.
func aiueoLabel(n int, fullwidth bool) string {
	half := []rune{'ｱ', 'ｲ', 'ｳ', 'ｴ', 'ｵ', 'ｶ', 'ｷ', 'ｸ', 'ｹ', 'ｺ'}
	full := []rune{'ア', 'イ', 'ウ', 'エ', 'オ', 'カ', 'キ', 'ク', 'ケ', 'コ'}
	tbl := half
	if fullwidth {
		tbl = full
	}
	if n >= 1 && n <= len(tbl) {
		return string(tbl[n-1])
	}
	return strconv.Itoa(n)
}

// irohaLabel returns iroha-order kana for 1..N.
func irohaLabel(n int, fullwidth bool) string {
	full := []rune{'イ', 'ロ', 'ハ', 'ニ', 'ホ', 'ヘ', 'ト', 'チ', 'リ', 'ヌ'}
	if n >= 1 && n <= len(full) {
		if fullwidth {
			return string(full[n-1])
		}
		return string(full[n-1])
	}
	return strconv.Itoa(n)
}

func alphaLabel(n int, upper bool) string {
	base := byte('a')
	if upper {
		base = 'A'
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{base + byte(n%26)}, out...)
		n /= 26
	}
	return string(out)
}

func roman(n int, upper bool) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"m", "cm", "d", "cd", "c", "xc", "l", "xl", "x", "ix", "v", "iv", "i"}
	var b strings.Builder
	for i, v := range vals {
		for n >= v {
			b.WriteString(syms[i])
			n -= v
		}
	}
	out := b.String()
	if upper {
		out = strings.ToUpper(out)
	}
	return out
}

// headingTitle returns a PDF-outline title when p is styled as a heading.
// Recognized styles: Heading1..Heading9 and Title (Word's defaults). We
// match case-insensitively and tolerate the variant "Heading 1" style ID
// some templates use. Returns "" for non-heading paragraphs.
//
// The title is the concatenated visible text of the paragraph's runs,
// trimmed of surrounding whitespace.
func headingTitle(p docx.Paragraph) string {
	if !isHeadingStyle(p.StyleID) {
		return ""
	}
	var b strings.Builder
	for _, run := range p.Runs {
		if run.Props.Vanish || run.Bookmark != "" || run.FieldBegin || run.FieldSep || run.FieldEnd || run.InstrText != "" {
			continue
		}
		b.WriteString(run.Text)
	}
	return strings.TrimSpace(b.String())
}

// outlineLevelFromStyle maps a paragraph's style ID and explicit
// w:outlineLvl onto a 1-based PDF outline depth. Title and Heading1
// both resolve to depth 1; Heading2 → 2; etc. An explicit outlineLvl
// (0..9, where 0 = top) wins when set, since some templates apply
// outline levels via direct formatting on non-heading styles.
func outlineLevelFromStyle(styleID string, outlineLvl int) int {
	if outlineLvl >= 0 && outlineLvl <= 9 {
		// w:outlineLvl is 0-based (0 = top); convert to a 1-based depth.
		// We use outlineLvl only when the style isn't a Heading variant
		// — Heading styles trump because they're the conventional source.
		if !isHeadingStyle(styleID) {
			return outlineLvl + 1
		}
	}
	low := strings.ToLower(strings.ReplaceAll(styleID, " ", ""))
	if low == "title" {
		return 1
	}
	if strings.HasPrefix(low, "heading") {
		rest := low[len("heading"):]
		if rest == "" {
			return 1
		}
		if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= 9 {
			return n
		}
	}
	return 1
}

func isHeadingStyle(id string) bool {
	if id == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(id, " ", ""))
	if low == "title" {
		return true
	}
	if !strings.HasPrefix(low, "heading") {
		return false
	}
	// "heading", "heading1" … "heading9" all qualify; "headingnoborder"
	// (custom names) shouldn't, so require either bare "heading" or one
	// trailing decimal digit.
	rest := low[len("heading"):]
	if rest == "" {
		return true
	}
	if len(rest) == 1 && rest[0] >= '1' && rest[0] <= '9' {
		return true
	}
	return false
}

// isHorizontalRuleParagraph reports whether p should render as a
// standalone horizontal-rule separator. Two source forms qualify:
//  1. Any run with HorizontalRule = true (VML <v:rect o:hr="t">).
//  2. An empty paragraph carrying ONLY a w:pBdr bottom edge — the
//     "Border Bottom on an empty paragraph" pattern Google Docs and
//     Word both produce for markdown "---" or the user's manual
//     Border-Bottom-empty-paragraph trick.
//
// Non-empty paragraphs with bottom borders (e.g. headings underlined
// by a w:bottom edge) are NOT considered HR; their content renders
// normally and the border is painted at the paragraph's bottom edge
// (see drawParagraphBorders).
func isHorizontalRuleParagraph(p docx.Paragraph) bool {
	for _, run := range p.Runs {
		if run.HorizontalRule {
			return true
		}
	}
	if !p.Borders.Bottom.Has() {
		return false
	}
	for _, run := range p.Runs {
		if run.Text != "" || run.ImageID != "" {
			return false
		}
	}
	return true
}

// drawHorizontalRule paints a thin line spanning the current content
// width and advances cursorY by a small fixed amount so surrounding
// paragraphs get appropriate breathing room. The line's thickness and
// color come from p.Borders.Bottom when set (the w:pBdr / w:bottom
// source form); otherwise we use a sensible default that matches
// markdown viewers.
func (r *renderer) drawHorizontalRule(p docx.Paragraph) {
	if p.SpacingBefore > 0 {
		r.cursorY += p.SpacingBefore
	}
	const rulePad = 6.0 // half-em above + below the line
	r.ensureRoom(rulePad*2 + 1)
	y := r.cursorY + rulePad

	lw := 0.6
	rr, gg, bb := uint8(160), uint8(160), uint8(160)
	if p.Borders.Bottom.Has() {
		if p.Borders.Bottom.Sz > 0 {
			lw = p.Borders.Bottom.Sz
		}
		if c := p.Borders.Bottom.Color; c != "" && c != "auto" {
			rr, gg, bb = parseHexColor(c)
		} else {
			rr, gg, bb = 0, 0, 0 // "auto" = black per spec
		}
	}
	r.pdf.SetLineWidth(lw)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.Line(r.marL, y, r.marL+r.contentW, y)
	r.cursorY = y + rulePad
	if p.SpacingAfter > 0 {
		r.cursorY += p.SpacingAfter
	}
}
