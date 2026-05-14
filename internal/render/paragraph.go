package render

import (
	"image"
	"strconv"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func (r *renderer) drawParagraph(p docx.Paragraph) error {
	// Positioned frame (w:framePr with placement attrs): render the
	// paragraph in its anchored absolute position without touching the
	// document flow. Surrounding body text is NOT reflowed around the
	// frame, so a w:wrap="around" frame may visually overlap — matching
	// docx4j's "absolute-positioned-block" behavior. drawFrame returns
	// nil for nothing-to-do (zero-width frames or missing geometry).
	if p.Frame != nil {
		return r.drawFrame(p)
	}
	// Horizontal rule paragraph (markdown's "---" / Word's o:hr="t"
	// VML rect). Draw a thin gray line spanning the content width
	// instead of running through the normal text-layout pipeline.
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
		r.pdf.AddOutline(title)
	}
	// RTL paragraph state: drives rune-reversal inside RTL word atoms and
	// line-internal atom reversal at flush time. Set before runsToAtoms so
	// atom construction sees it.
	r.paragraphRTL = p.Bidi
	defer func() { r.paragraphRTL = false }()
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
	listIndent := 0.0
	if p.List != nil {
		markerText, markerImg, indentPt, hangPt := r.resolveListMarker(*p.List)
		listIndent = indentPt
		if markerText != "" || markerImg != nil {
			markerX := r.marL + indentPt - hangPt
			if markerX < r.marL {
				markerX = r.marL
			}
			r.pendingMarker = &pendingMarker{text: markerText, image: markerImg, x: markerX}
		}
	}

	if len(p.Runs) == 0 && r.pendingMarker == nil {
		size := r.opts.DefaultFontSize
		r.ensureRoom(size * 1.2)
		r.cursorY += size * 1.2
		return nil
	}

	// Word semantics: paragraph-level w:ind overrides numbering.xml indent
	// for the same list item rather than stacking.
	leftIndent := listIndent
	if p.IndentLeftPt > 0 {
		leftIndent = p.IndentLeftPt
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

	if p.SpacingAfter > 0 {
		r.cursorY += p.SpacingAfter
	}
	r.prevStyleID = p.StyleID
	return nil
}

// restoreParagraphState rolls back marL/contentW/lineHeight to the values
// captured at paragraph start, EXCEPT when a column-advance happened mid-
// paragraph. In that case the column position persists and the next
// paragraph picks up where we left off.
func (r *renderer) restoreParagraphState(savedColIdx int, savedMarL, savedContentW float64, savedLine docx.LineHeight) {
	r.lineHeight = savedLine
	if r.colIdx != savedColIdx {
		r.marL = r.colBaseX + float64(r.colIdx)*(r.colW+r.colGap)
		r.contentW = r.colW
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
func (r *renderer) resolveListMarker(li docx.ListInfo) (marker string, img image.Image, indentPt, hangPt float64) {
	absID, ok := r.doc.Numbering.NumToAbs[li.NumID]
	if !ok {
		return "", nil, 0, 0
	}
	an, ok := r.doc.Numbering.Abstract[absID]
	if !ok {
		return "", nil, 0, 0
	}
	lv, ok := an.Levels[li.Level]
	if !ok {
		return "", nil, 0, 0
	}

	if r.counters[li.NumID] == nil {
		r.counters[li.NumID] = map[int]int{}
	}
	if _, seen := r.counters[li.NumID][li.Level]; !seen {
		r.counters[li.NumID][li.Level] = lv.Start
	} else {
		r.counters[li.NumID][li.Level]++
	}
	for k := range r.counters[li.NumID] {
		if k > li.Level {
			delete(r.counters[li.NumID], k)
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

	if lv.PicBulletID > 0 {
		if rid, ok := r.doc.Numbering.PicBullets[lv.PicBulletID]; ok {
			if image, ok := r.doc.Images[rid]; ok {
				return "", image, indentPt, hangPt
			}
		}
	}

	marker = formatLevelText(lv, r.counters[li.NumID])
	return marker, nil, indentPt, hangPt
}

// formatLevelText expands lvlText placeholders like "%1.%2" using the
// current counter map. For bullets, lvlText is taken literally.
func formatLevelText(lv docx.NumLevel, counters map[int]int) string {
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
		val := counters[n-1]
		fm := lv.Format
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
	case "decimal", "decimalZero":
		return strconv.Itoa(n)
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
	default:
		return strconv.Itoa(n)
	}
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

// isHorizontalRuleParagraph reports whether p is a "thematic break"
// paragraph (markdown's "---" or any other source that Word encoded
// as a <v:rect o:hr="t">). True iff any run in the paragraph carries
// HorizontalRule = true. We treat the whole paragraph as the
// separator rather than mixing line content with a rule.
func isHorizontalRuleParagraph(p docx.Paragraph) bool {
	for _, run := range p.Runs {
		if run.HorizontalRule {
			return true
		}
	}
	return false
}

// drawHorizontalRule paints a thin gray line spanning the current
// content width and advances cursorY by a small fixed amount so
// surrounding paragraphs get appropriate breathing room.
func (r *renderer) drawHorizontalRule(p docx.Paragraph) {
	// SpacingBefore from the paragraph's pPr still applies — gives the
	// rule some air above it.
	if p.SpacingBefore > 0 {
		r.cursorY += p.SpacingBefore
	}
	const rulePad = 6.0 // half-em above + below the line
	r.ensureRoom(rulePad*2 + 1)
	y := r.cursorY + rulePad
	r.pdf.SetLineWidth(0.6)
	r.pdf.SetStrokeColor(160, 160, 160)
	r.pdf.Line(r.marL, y, r.marL+r.contentW, y)
	r.cursorY = y + rulePad
	if p.SpacingAfter > 0 {
		r.cursorY += p.SpacingAfter
	}
}
