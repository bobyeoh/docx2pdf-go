package render

import (
	"fmt"
	"strconv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
	"github.com/signintech/gopdf"
)

// stampPageDecorations renders header/footer onto every page after the body
// is finalized. Each PDF page is mapped to its owning section via
// sectionPageStart so multi-section docs use the right header/footer and
// margin geometry on each page.
func (r *renderer) stampPageDecorations(sections []docx.Section, sectionPageStart []int) error {
	anyDecoration := false
	for _, s := range sections {
		if len(s.HeaderBlocks) > 0 || len(s.FooterBlocks) > 0 ||
			len(s.HeaderFirstBlocks) > 0 || len(s.FooterFirstBlocks) > 0 ||
			len(s.HeaderEvenBlocks) > 0 || len(s.FooterEvenBlocks) > 0 {
			anyDecoration = true
			break
		}
	}
	if !anyDecoration {
		return nil
	}
	n := r.pdf.GetNumberOfPages()
	if n == 0 {
		return nil
	}

	sectionOf := func(pageNo int) int {
		for i := len(sectionPageStart) - 1; i >= 0; i-- {
			if sectionPageStart[i] <= pageNo {
				return i
			}
		}
		return 0
	}

	for i := 1; i <= n; i++ {
		if err := r.pdf.SetPage(i); err != nil {
			return err
		}
		sec := sections[sectionOf(i)]
		r.pageW = twipsToPt(sec.PageSize.WidthTwips)
		r.pageH = twipsToPt(sec.PageSize.HeightTwips)
		marL := twipsToPt(sec.Margins.Left)
		marR := twipsToPt(sec.Margins.Right)
		marL += twipsToPt(sec.GutterTwips)
		if sec.MirrorMargins && i%2 == 0 {
			marL, marR = marR, marL
		}
		r.marL = marL
		r.marR = marR
		r.marT = twipsToPt(sec.Margins.Top)
		r.marB = twipsToPt(sec.Margins.Bottom)
		r.contentW = r.pageW - r.marL - r.marR

		if sec.BackgroundColor != "" {
			r1, g1, b1 := parseHexColor(sec.BackgroundColor)
			r.pdf.SetFillColor(r1, g1, b1)
			r.pdf.Rectangle(0, 0, r.pageW, r.pageH, "F", 0, 0)
		}
		drawPageBorders(r, sec.Borders)

		if sec.LineNumbering.CountBy > 0 {
			drawLineNumbers(r, sec)
		}

		savedFields := r.fields
		pageInSection := i - sectionPageStart[sectionOf(i)] + 1
		displayPage := pageInSection
		if sec.PageNumber.Start > 0 {
			displayPage = sec.PageNumber.Start + pageInSection - 1
		}
		r.fields = fieldVars{
			page:        displayPage,
			numPages:    n,
			pageFmt:     sec.PageNumber.Fmt,
			now:         savedFields.now,
			filename:    savedFields.filename,
			author:      savedFields.author,
			title:       savedFields.title,
			subject:     savedFields.subject,
			seqCounters: savedFields.seqCounters,
			bookmarks:   savedFields.bookmarks,
		}

		hdr := sec.HeaderBlocks
		ftr := sec.FooterBlocks
		pageWithinSection := i - sectionPageStart[sectionOf(i)] + 1
		if sec.TitlePg && pageWithinSection == 1 {
			if len(sec.HeaderFirstBlocks) > 0 {
				hdr = sec.HeaderFirstBlocks
			}
			if len(sec.FooterFirstBlocks) > 0 {
				ftr = sec.FooterFirstBlocks
			}
		} else if sec.EvenAndOddHeaders && i%2 == 0 {
			if len(sec.HeaderEvenBlocks) > 0 {
				hdr = sec.HeaderEvenBlocks
			}
			if len(sec.FooterEvenBlocks) > 0 {
				ftr = sec.FooterEvenBlocks
			}
		}

		if len(hdr) > 0 {
			headerY := r.marT * 0.35
			if err := r.drawAt(hdr, r.marL, headerY, r.contentW); err != nil {
				r.fields = savedFields
				return err
			}
		}
		if len(ftr) > 0 {
			footerY := r.pageH - r.marB + 6
			if err := r.drawAt(ftr, r.marL, footerY, r.contentW); err != nil {
				r.fields = savedFields
				return err
			}
		}
		r.fields = savedFields
	}
	return nil
}

// drawAt runs the block-level drawing pipeline against a transient region
// without disturbing the main body cursor. Used for headers / footers.
func (r *renderer) drawAt(blocks []docx.Block, x, y, w float64) error {
	savedY, savedMarL, savedContentW, savedNoBreak := r.cursorY, r.marL, r.contentW, r.noPageBreak
	r.cursorY = y
	r.marL = x
	r.contentW = w
	r.noPageBreak = true
	defer func() {
		r.cursorY = savedY
		r.marL = savedMarL
		r.contentW = savedContentW
		r.noPageBreak = savedNoBreak
	}()
	for _, b := range blocks {
		switch v := b.(type) {
		case docx.Paragraph:
			if err := r.drawParagraph(v); err != nil {
				return err
			}
		case docx.Table:
			if err := r.drawTable(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// stampPageNumbers walks every page after the body has been laid out and
// draws "i / n" centered in the bottom margin.
func (r *renderer) stampPageNumbers() error {
	n := r.pdf.GetNumberOfPages()
	if n == 0 {
		return nil
	}
	if err := r.pdf.SetFont(defaultFamily, "", 9); err != nil {
		return err
	}
	r.pdf.SetTextColor(80, 80, 80)
	for i := 1; i <= n; i++ {
		if err := r.pdf.SetPage(i); err != nil {
			return err
		}
		label := fmt.Sprintf("%d / %d", i, n)
		w, _ := r.pdf.MeasureTextWidth(label)
		x := (r.pageW - w) / 2
		y := r.pageH - r.marB + 14
		if y > r.pageH-6 {
			y = r.pageH - 6
		}
		r.pdf.SetX(x)
		r.pdf.SetY(y)
		if err := r.pdf.Cell(nil, label); err != nil {
			return err
		}
	}
	return nil
}

// applyLineHeight converts a natural (font-derived) line height to the
// effective line height per the paragraph's w:spacing w:line semantics.
func (r *renderer) applyLineHeight(natural float64) float64 {
	switch r.lineHeight.Rule {
	case "exact":
		if r.lineHeight.Pt > 0 {
			return r.lineHeight.Pt
		}
	case "atLeast":
		if r.lineHeight.Pt > natural {
			return r.lineHeight.Pt
		}
	case "auto":
		if r.lineHeight.Mul > 0 {
			return natural * r.lineHeight.Mul
		}
	}
	return natural
}

// appendNotesSection appends a heading + the note bodies (each prefixed with
// "[id]") to the current page. Skipped silently if notes is empty.
func (r *renderer) appendNotesSection(notes map[string][]docx.Block, title string) error {
	if len(notes) == 0 {
		return nil
	}
	var ids []string
	for k := range notes {
		ids = append(ids, k)
	}
	sortStringDecimals(ids)

	r.ensureRoom(36)
	r.cursorY += 18
	title2 := docx.Paragraph{
		Runs:         []docx.Run{{Text: title, Props: docx.RunProps{Bold: true, FontSize: 14}}},
		SpacingAfter: 6,
	}
	if err := r.drawParagraph(title2); err != nil {
		return err
	}
	for _, id := range ids {
		marker := docx.Paragraph{
			Runs: []docx.Run{{Text: "[" + id + "] ", Props: docx.RunProps{Bold: true}}},
		}
		if err := r.drawParagraph(marker); err != nil {
			return err
		}
		for _, b := range notes[id] {
			switch v := b.(type) {
			case docx.Paragraph:
				if err := r.drawParagraph(v); err != nil {
					return err
				}
			case docx.Table:
				if err := r.drawTable(v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sortStringDecimals(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0; j-- {
			ai, _ := strconv.Atoi(ss[j])
			bi, _ := strconv.Atoi(ss[j-1])
			if ai >= bi {
				break
			}
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

// drawLineNumbers paints a numeric counter in the left margin at every Nth
// line position. This is an approximation good enough for legal-doc style
// layout where exact alignment doesn't matter.
func drawLineNumbers(r *renderer, sec docx.Section) {
	countBy := sec.LineNumbering.CountBy
	if countBy < 1 {
		countBy = 1
	}
	lineH := r.opts.DefaultFontSize * 1.2
	x := r.marL - 18
	if x < 2 {
		x = 2
	}
	_ = r.pdf.SetFont(defaultFamily, "", 9)
	r.pdf.SetTextColor(120, 120, 120)
	n := r.lineNumCounter
	for y := r.marT; y+lineH < r.pageH-r.marB; y += lineH {
		if n%countBy == 0 {
			r.pdf.SetX(x)
			r.pdf.SetY(y)
			_ = r.pdf.Cell(nil, strconv.Itoa(n))
		}
		n++
	}
	if sec.LineNumbering.Restart != "newSection" {
		r.lineNumCounter = n
	}
}

// drawPageBorders draws the four w:pgBorders edges inset slightly from the
// page edge.
func drawPageBorders(r *renderer, b docx.PageBorders) {
	if !(b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has()) {
		return
	}
	inset := 18.0
	x1, y1, x2, y2 := inset, inset, r.pageW-inset, r.pageH-inset
	if b.Top.Has() {
		drawCellEdge(r, b.Top, x1, y1, x2, y1)
	}
	if b.Bottom.Has() {
		drawCellEdge(r, b.Bottom, x1, y2, x2, y2)
	}
	if b.Left.Has() {
		drawCellEdge(r, b.Left, x1, y1, x1, y2)
	}
	if b.Right.Has() {
		drawCellEdge(r, b.Right, x2, y1, x2, y2)
	}
}

func (r *renderer) ensureRoom(h float64) {
	if r.noPageBreak {
		return
	}
	if r.cursorY+h > r.pageH-r.marB {
		if r.numColumns > 1 && r.colIdx < int(r.numColumns)-1 {
			r.colIdx++
			r.marL = r.colBaseX + float64(r.colIdx)*(r.colW+r.colGap)
			r.cursorY = r.marT
			return
		}
		r.drawFootnotesAtBottom()
		r.newPage()
		if r.numColumns > 1 {
			r.colIdx = 0
			r.marL = r.colBaseX
		}
	}
}

// drawFootnotesAtBottom emits pendingFootnotes immediately above the bottom
// margin, with a thin separator rule.
func (r *renderer) drawFootnotesAtBottom() {
	if r.drawingFootnotes || len(r.pendingFootnotes) == 0 {
		return
	}
	r.drawingFootnotes = true
	defer func() {
		r.drawingFootnotes = false
		r.pendingFootnotes = nil
	}()

	notesH := 6 + 14*float64(len(r.pendingFootnotes))
	startY := r.pageH - r.marB - notesH
	if startY < r.cursorY+6 {
		startY = r.cursorY + 6
	}

	savedY := r.cursorY
	savedNoPageBreak := r.noPageBreak
	r.noPageBreak = true
	defer func() {
		r.cursorY = savedY
		r.noPageBreak = savedNoPageBreak
	}()

	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(120, 120, 120)
	r.pdf.Line(r.marL, startY, r.marL+r.contentW*0.3, startY)
	r.cursorY = startY + 2

	logFn := r.opts.Logger
	if logFn == nil && r.opts.Verbose {
		logFn = func(s string) { fmt.Println(s) }
	}
	logErr := func(msg string, err error) {
		if err == nil || logFn == nil {
			return
		}
		logFn(fmt.Sprintf("footnote: %s: %v", msg, err))
	}

	for _, pn := range r.pendingFootnotes {
		var blocks []docx.Block
		if pn.endnote {
			blocks = r.doc.Endnotes[pn.id]
		} else {
			blocks = r.doc.Footnotes[pn.id]
		}
		marker := docx.Paragraph{
			Runs: []docx.Run{{
				Text: "[" + pn.id + "] ", Props: docx.RunProps{Bold: true, FontSize: 9},
			}},
		}
		logErr("marker "+pn.id, r.drawParagraph(marker))
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for k := range v.Runs {
					if v.Runs[k].Props.FontSize == 0 {
						v.Runs[k].Props.FontSize = 9
					}
				}
				logErr("body "+pn.id, r.drawParagraph(v))
			}
		}
	}
}

// newPage adds a page using the renderer's current section geometry so a
// body-internal page break in a landscape section produces another landscape
// page.
func (r *renderer) newPage() {
	r.pdf.AddPageWithOption(gopdf.PageOption{
		PageSize: &gopdf.Rect{W: r.pageW, H: r.pageH},
	})
	r.cursorY = r.marT
	primeContentStream(r.pdf)
}

// primeContentStream emits a zero-width invisible stroke so gopdf doesn't
// produce a malformed (empty) /Contents object for a page that ends up with
// no drawing operations. Without this, viewers reject the page as
// "wrong type (cmd)" and render it at 0×0 instead of the declared MediaBox.
func primeContentStream(pdf *gopdf.GoPdf) {
	pdf.SetLineWidth(0)
	pdf.Line(0, 0, 0, 0)
}
