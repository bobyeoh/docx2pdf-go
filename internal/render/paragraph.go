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
	if p.PageBreak {
		r.pdf.AddPage()
		r.cursorY = r.marT
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
	} else if p.IndentFirstLinePt < 0 {
		// Hanging indent: first physical line starts further left and is
		// proportionally wider. Carried as renderer state so layoutLine can
		// apply it on the very first flush only.
		r.firstLineHangPt = -p.IndentFirstLinePt
	}
	runs := p.Runs
	if p.DropCap != "" {
		runs = applyDropCap(runs, p.DropCapLines)
	}
	atoms = append(atoms, r.runsToAtoms(runs)...)

	if err := r.layoutLine(atoms, p.Alignment); err != nil {
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
	if hangPt < 2 {
		hangPt = 6
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
