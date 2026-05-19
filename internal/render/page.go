package render

import (
	"fmt"
	"strconv"
	"strings"

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
		applyPageBorderMargins(r, sec.Borders)

		// Word only paints w:background when settings.xml declares
		// w:displayBackgroundShape — without that switch the color is
		// stored but suppressed at render time.
		if sec.BackgroundColor != "" && r.doc.Settings.DisplayBackgroundShape {
			r1, g1, b1 := parseHexColor(sec.BackgroundColor)
			r.pdf.SetFillColor(r1, g1, b1)
			r.pdf.Rectangle(0, 0, r.pageW, r.pageH, "F", 0, 0)
		}
		secIdx := sectionOf(i)
		pageInSection := i - sectionPageStart[secIdx] + 1
		isLastSecPage := i == n || (secIdx+1 < len(sectionPageStart) && sectionPageStart[secIdx+1]-1 == i)
		if shouldDrawPageBorder(sec.Borders.Display, pageInSection, isLastSecPage) {
			drawPageBorders(r, sec.Borders)
		}

		if sec.LineNumbering.CountBy > 0 {
			drawLineNumbers(r, sec)
		}

		savedFields := r.fields
		displayPage := pageInSection
		if sec.PageNumber.Start > 0 {
			displayPage = sec.PageNumber.Start + pageInSection - 1
		}
		// pages in THIS section = next-section-start - this-section-start.
		// Last section runs to the end of the document.
		secPagesEnd := n
		if secIdx+1 < len(sectionPageStart) {
			secPagesEnd = sectionPageStart[secIdx+1] - 1
		}
		numSecPages := secPagesEnd - sectionPageStart[secIdx] + 1
		chapNum := ""
		if sec.PageNumber.ChapStyle > 0 {
			chapNum = resolveChapterNumber(r, i, sec.PageNumber.ChapStyle)
		}
		r.fields = fieldVars{
			page:            displayPage,
			numPages:        n,
			pageFmt:         sec.PageNumber.Fmt,
			numSectionPages: numSecPages,
			section:         secIdx + 1,
			chapStyle:       sec.PageNumber.ChapStyle,
			chapSep:         sec.PageNumber.ChapSep,
			chapNumber:      chapNum,
			decimalSymbol:   savedFields.decimalSymbol,
			listSeparator:   savedFields.listSeparator,
			now:             savedFields.now,
			filename:        savedFields.filename,
			filenameFull:    savedFields.filenameFull,
			author:          savedFields.author,
			title:           savedFields.title,
			subject:         savedFields.subject,
			company:         savedFields.company,
			keywords:        savedFields.keywords,
			comments:        savedFields.comments,
			username:        savedFields.username,
			numWords:        savedFields.numWords,
			numChars:        savedFields.numChars,
			totalMinutes:    savedFields.totalMinutes,
			createDate:      savedFields.createDate,
			saveDate:        savedFields.saveDate,
			printDate:       savedFields.printDate,
			seqCounters:     savedFields.seqCounters,
			bookmarks:       savedFields.bookmarks,
			bookmarkPages:   savedFields.bookmarkPages,
			docProperties:   savedFields.docProperties,
		}

		hdr := sec.HeaderBlocks
		ftr := sec.FooterBlocks
		pageWithinSection := i - sectionPageStart[sectionOf(i)] + 1
		// Even-page H/F apply only when BOTH this section declared an even
		// reference AND the doc-level setting (w:evenAndOddHeaders in
		// settings.xml) is on. Either flag alone is insufficient per Word's
		// behavior: the setting is the master switch.
		evenActive := sec.EvenAndOddHeaders && r.doc.Settings.EvenAndOddHeaders
		if sec.TitlePg && pageWithinSection == 1 {
			if len(sec.HeaderFirstBlocks) > 0 {
				hdr = sec.HeaderFirstBlocks
			}
			if len(sec.FooterFirstBlocks) > 0 {
				ftr = sec.FooterFirstBlocks
			}
		} else if evenActive && i%2 == 0 {
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

// measureBlocks does a dry layout pass over a block list and returns the
// total rendered height. Used by section vAlign to know the slack space
// before drawing. Tables are estimated via measureCell; paragraphs reuse
// the same atom math as layoutLine without drawing.
func (r *renderer) measureBlocks(blocks []docx.Block) float64 {
	total := 0.0
	savedLine := r.lineHeight
	savedFootnotes := r.pendingFootnotes
	defer func() {
		r.lineHeight = savedLine
		r.pendingFootnotes = savedFootnotes
	}()
	for _, b := range blocks {
		switch v := b.(type) {
		case docx.Paragraph:
			total += r.measureParagraph(v)
		case docx.Table:
			total += r.measureTable(v)
		}
	}
	return total
}

// measureParagraph estimates one paragraph's rendered height at the
// current contentW. Mirrors drawParagraph's geometry decisions but
// without touching the renderer's drawing state.
func (r *renderer) measureParagraph(p docx.Paragraph) float64 {
	if len(p.Runs) == 0 && p.List == nil {
		return r.opts.DefaultFontSize * 1.2
	}
	r.lineHeight = p.LineHeight
	atoms := r.runsToAtoms(p.Runs)
	innerW := r.contentW
	if p.IndentLeftPt > 0 {
		innerW -= p.IndentLeftPt
	}
	if innerW <= 0 {
		innerW = r.contentW
	}
	h := p.SpacingBefore + p.SpacingAfter
	lineW := 0.0
	lineH := r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
	hadAny := false
	flush := func() {
		h += lineH
		lineW = 0
		lineH = r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
		hadAny = false
	}
	for _, a := range atoms {
		if a.kind == atomBookmark {
			continue
		}
		if a.kind == atomBreak || a.kind == atomPageBreak {
			flush()
			continue
		}
		ah := atomHeight(a, r.opts.DefaultFontSize)
		if lineW+a.width > innerW && lineW > 0 {
			flush()
			if a.kind == atomSpace {
				continue
			}
		}
		lineW += a.width
		scaled := r.applyLineHeight(ah)
		if scaled > lineH {
			lineH = scaled
		}
		hadAny = true
	}
	if hadAny || lineW > 0 || len(atoms) == 0 {
		h += lineH
	}
	return h
}

// measureTable estimates a table's rendered height.
func (r *renderer) measureTable(t docx.Table) float64 {
	cols := 0
	for _, row := range t.Rows {
		if len(row.Cells) > cols {
			cols = len(row.Cells)
		}
	}
	if cols == 0 {
		return 0
	}
	widths := r.resolveColumnWidths(t, cols)
	total := 0.0
	for _, row := range t.Rows {
		rowH := 0.0
		col := 0
		for _, cell := range row.Cells {
			if col >= len(widths) {
				break
			}
			span := cell.GridSpan
			if span < 1 {
				span = 1
			}
			w := sumWidths(widths, col, span)
			if cell.VMerge != "continue" {
				if ch := r.measureCell(cell, w); ch > rowH {
					rowH = ch
				}
			}
			col += span
		}
		if rowH < r.opts.DefaultFontSize*1.4 {
			rowH = r.opts.DefaultFontSize * 1.4
		}
		if row.HeightTwips > 0 {
			minH := float64(row.HeightTwips) / 20.0
			if row.HeightRuleExact || minH > rowH {
				rowH = minH
			}
		}
		total += rowH
	}
	return total
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

// resolveChapterNumber returns the chapter-number string for the given
// page when w:pgNumType@w:chapStyle is in effect. Strategy: among
// pre-collected headings at chapStyle outline level, count those whose
// bookmark resolves to a page ≤ pageNum. If the heading text itself
// begins with a numeric prefix (e.g. "3 Background", "Chapter 12 — …")
// we surface that number instead of the sequential count. Returns "" when
// no chapStyle heading precedes this page.
func resolveChapterNumber(r *renderer, pageNum, chapStyle int) string {
	if r == nil || pageNum <= 0 || chapStyle <= 0 || chapStyle > 9 {
		return ""
	}
	headings := r.fields.headings
	bp := r.fields.bookmarkPages
	if len(headings) == 0 {
		return ""
	}
	count := 0
	last := ""
	for _, h := range headings {
		if h.Level != chapStyle {
			continue
		}
		hp := 0
		if h.Bookmark != "" && bp != nil {
			hp = bp[h.Bookmark]
		}
		if hp == 0 || hp > pageNum {
			continue
		}
		count++
		// Try to extract a leading numeric token from the heading text
		// ("3 Introduction" / "12. Implementation" / "Chapter 5 — Notes").
		if n := leadingHeadingNumber(h.Text); n != "" {
			last = n
		} else {
			last = strconv.Itoa(count)
		}
	}
	return last
}

// leadingHeadingNumber returns the first integer that looks like a
// chapter marker in s. Accepts forms like "3 Title", "12. Title",
// "Chapter 7 — Title", "Part II" (Roman numerals not honored here —
// we surface only Arabic). Returns "" when no leading number is found.
func leadingHeadingNumber(s string) string {
	s = strings.TrimSpace(s)
	// Skip a leading english "Chapter " / "Part " / "Section " keyword.
	for _, kw := range []string{"Chapter ", "chapter ", "CHAPTER ", "Part ", "Section "} {
		if strings.HasPrefix(s, kw) {
			s = s[len(kw):]
			break
		}
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return ""
	}
	return s[:i]
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
// When the active section declares a w:docGrid line pitch we additionally
// snap the result UP to a multiple of that pitch so consecutive lines
// land on the CJK grid (Word's "line grid" feature).
func (r *renderer) applyLineHeight(natural float64) float64 {
	var h float64
	switch r.lineHeight.Rule {
	case "exact":
		if r.lineHeight.Pt > 0 {
			h = r.lineHeight.Pt
		} else {
			h = natural
		}
	case "atLeast":
		if r.lineHeight.Pt > natural {
			h = r.lineHeight.Pt
		} else {
			h = natural
		}
	case "auto":
		if r.lineHeight.Mul > 0 {
			h = natural * r.lineHeight.Mul
		} else {
			h = natural
		}
	default:
		h = natural
	}
	// docGrid snap: only for the line-grid modes ("lines" or
	// "linesAndChars"). The pitch is in 1/20 pt units.
	if (r.activeDocGrid.Type == "lines" || r.activeDocGrid.Type == "linesAndChars") &&
		r.activeDocGrid.LinePitch > 0 && r.lineHeight.Rule != "exact" {
		pitch := float64(r.activeDocGrid.LinePitch) / 20.0
		if pitch > 0 {
			// Snap upward to the next whole pitch step.
			steps := h / pitch
			rounded := float64(int(steps))
			if rounded < steps {
				rounded++
			}
			snapped := rounded * pitch
			if snapped > h {
				h = snapped
			}
		}
	}
	return h
}

// activeCharSpacePt returns extra letter-spacing in points to apply to
// CJK text in the current section, derived from w:docGrid/@w:charSpace.
// Returns 0 unless the section enables the linesAndChars grid mode.
//
// CharSpace is stored in 1/100 pt; OOXML uses it as the additional space
// inserted between CJK characters so the grid spacing equals
// LinePitch + CharSpace/100 (per the spec). For Latin text the effect
// is negligible at typical CharSpace values, so we apply it uniformly —
// the visible difference on CJK is what authors are after.
func (r *renderer) activeCharSpacePt() float64 {
	if r.activeDocGrid.Type != "linesAndChars" {
		return 0
	}
	if r.activeDocGrid.CharSpace <= 0 {
		return 0
	}
	return float64(r.activeDocGrid.CharSpace) / 100.0
}

// appendPermissionRangesSection lists w:permStart / w:permEnd protected
// ranges as a trailing reference section so the audit information is
// visible in the PDF. Word's edit-protection has no PDF analogue beyond
// the read-only flag; surfacing the editor/group lets reviewers see who
// could change which section in the source document.
func (r *renderer) appendPermissionRangesSection(doc *docx.Document) error {
	if len(doc.PermissionRanges) == 0 {
		return nil
	}
	ids := make([]string, 0, len(doc.PermissionRanges))
	for k := range doc.PermissionRanges {
		ids = append(ids, k)
	}
	sortStringDecimals(ids)
	r.ensureRoom(36)
	r.cursorY += 18
	title := docx.Paragraph{
		Runs:         []docx.Run{{Text: "Protected Ranges", Props: docx.RunProps{Bold: true, FontSize: 14}}},
		SpacingAfter: 6,
	}
	if err := r.drawParagraph(title); err != nil {
		return err
	}
	for _, id := range ids {
		pr := doc.PermissionRanges[id]
		desc := "[" + pr.ID + "]"
		who := pr.Editor
		if who == "" {
			who = pr.EditorGroup
		}
		if who != "" {
			desc += " " + who
		}
		entry := docx.Paragraph{
			Runs: []docx.Run{{Text: desc, Props: docx.RunProps{Color: "BF8F00"}}},
		}
		if err := r.drawParagraph(entry); err != nil {
			return err
		}
	}
	return nil
}

// appendCommentsSection renders the Comments trailer with author/date
// headers and reply-thread indentation. Threads are reconstructed from
// w:commentsExtended (paraId / paraIdParent), with replies sorted right
// after their parent and indented per nesting depth.
func (r *renderer) appendCommentsSection(doc *docx.Document) error {
	if len(doc.Comments) == 0 {
		return nil
	}
	ids := make([]string, 0, len(doc.Comments))
	for k := range doc.Comments {
		ids = append(ids, k)
	}
	sortStringDecimals(ids)

	paraToCommentID := map[string]string{}
	for _, id := range ids {
		if meta, ok := doc.CommentMeta[id]; ok && meta.ParaID != "" {
			paraToCommentID[meta.ParaID] = id
		}
	}

	// Build a per-comment "thread depth" by walking the parent chain.
	depths := computeCommentDepths(doc, paraToCommentID)
	// Reorder ids so replies follow their parent (DFS by parent links).
	ordered := orderCommentsByThread(ids, doc, paraToCommentID)

	r.ensureRoom(36)
	r.cursorY += 18
	title := docx.Paragraph{
		Runs:         []docx.Run{{Text: "Comments", Props: docx.RunProps{Bold: true, FontSize: 14}}},
		SpacingAfter: 6,
	}
	if err := r.drawParagraph(title); err != nil {
		return err
	}
	for _, id := range ordered {
		meta := doc.CommentMeta[id]
		author := meta.Author
		if author == "" {
			author = "Reviewer"
		}
		header := "[" + id + "] " + author
		if meta.Initials != "" {
			header += " (" + meta.Initials + ")"
		}
		if meta.Date != "" {
			header += " • " + meta.Date
		}
		resolved := false
		if ext, ok := doc.CommentsExtended[meta.ParaID]; ok && ext.Done {
			resolved = true
			header += " (resolved)"
		}
		indent := float64(depths[id]) * 18.0
		// Author color-coding: re-use the revision palette so a reviewer
		// who appears as both a tracked-change author and a comment author
		// shows the same color across the document. Prefer a per-person
		// identifier (provider id / email) over the display name when
		// available so two reviewers with the same display name still get
		// distinct colors. Resolved comments mute the color toward grey.
		colorKey := meta.Author
		if doc.PeopleByID != nil {
			if person, ok := doc.PeopleByID[meta.Author]; ok {
				if person.ProviderID != "" {
					colorKey = person.ProviderID
				} else if person.Email != "" {
					colorKey = person.Email
				}
			}
		}
		headerColor := ""
		if colorKey != "" {
			headerColor = revisionColorForAuthor(colorKey, "")
		}
		if resolved {
			headerColor = "808080" // mute resolved-comment header
		}
		markerProps := docx.RunProps{Bold: true, Color: headerColor}
		// Resolved-comment styling: strikethrough the bold header so it
		// reads as visually "done" rather than just textually-labeled.
		if resolved {
			markerProps.Strike = true
		}
		marker := docx.Paragraph{
			Runs:         []docx.Run{{Text: header, Props: markerProps}},
			IndentLeftPt: indent,
		}
		if err := r.drawParagraph(marker); err != nil {
			return err
		}
		for _, b := range doc.Comments[id] {
			switch v := b.(type) {
			case docx.Paragraph:
				v.IndentLeftPt += indent
				// Resolved comments: mute body text to grey so the
				// reader's eye glides over "done" threads. Done at the
				// run-prop level (only overrides when the run had no
				// explicit color of its own).
				if resolved {
					for i := range v.Runs {
						if v.Runs[i].Props.Color == "" {
							v.Runs[i].Props.Color = "808080"
						}
					}
				}
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

// computeCommentDepths walks each comment's parent chain (via paraIdParent
// in commentsExtended) and returns a map of commentID → nesting depth.
// Root comments have depth 0. Cycle-safe by capping at len(comments).
func computeCommentDepths(doc *docx.Document, paraToCommentID map[string]string) map[string]int {
	out := map[string]int{}
	for id, meta := range doc.CommentMeta {
		depth := 0
		paraID := meta.ParaID
		guard := len(doc.CommentMeta) + 1
		for paraID != "" && guard > 0 {
			ext, ok := doc.CommentsExtended[paraID]
			if !ok || ext.ParentParaID == "" {
				break
			}
			if _, isComment := paraToCommentID[ext.ParentParaID]; !isComment {
				break
			}
			depth++
			paraID = ext.ParentParaID
			guard--
		}
		out[id] = depth
	}
	return out
}

// orderCommentsByThread reshuffles a list of comment IDs so replies fall
// immediately after their parent (DFS), preserving the original order
// among siblings. Comments without a parent stay in the original spot.
func orderCommentsByThread(ids []string, doc *docx.Document, paraToCommentID map[string]string) []string {
	parentOf := map[string]string{}
	childrenOf := map[string][]string{}
	for _, id := range ids {
		meta, ok := doc.CommentMeta[id]
		if !ok {
			continue
		}
		ext, ok := doc.CommentsExtended[meta.ParaID]
		if !ok || ext.ParentParaID == "" {
			continue
		}
		parentID, ok := paraToCommentID[ext.ParentParaID]
		if !ok {
			continue
		}
		parentOf[id] = parentID
		childrenOf[parentID] = append(childrenOf[parentID], id)
	}
	visited := map[string]bool{}
	out := make([]string, 0, len(ids))
	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		out = append(out, id)
		for _, child := range childrenOf[id] {
			visit(child)
		}
	}
	for _, id := range ids {
		if _, hasParent := parentOf[id]; hasParent {
			continue
		}
		visit(id)
	}
	// Stragglers (orphaned replies) come last in original order.
	for _, id := range ids {
		if !visited[id] {
			visit(id)
		}
	}
	return out
}

// allSectionsHaveSectEndnotes reports whether every section in the
// document declares w:endnotePr w:pos="sectEnd". When true the renderer
// suppresses the doc-end endnote trailer because each section already
// emitted its endnotes inline.
func allSectionsHaveSectEndnotes(secs []docx.Section) bool {
	if len(secs) == 0 {
		return false
	}
	for _, s := range secs {
		if s.EndnotePr == nil || s.EndnotePr.Pos != "sectEnd" {
			return false
		}
	}
	return true
}

// anySectionDocEndFootnotes reports whether any section declares
// w:footnotePr w:pos="docEnd" — in which case the doc-end trailer prints
// footnotes (instead of per-page bottom-of-page render). "sectEnd" is
// approximated by docEnd here since our footnote queue is process-global,
// not per-section; "beneathText" falls back to default pageBottom.
func anySectionDocEndFootnotes(secs []docx.Section) bool {
	for _, s := range secs {
		if s.FootnotePr != nil && (s.FootnotePr.Pos == "docEnd" || s.FootnotePr.Pos == "sectEnd") {
			return true
		}
	}
	return false
}

// appendSectionEndnotes renders the endnotes referenced from sec.Blocks
// inline (at the current cursor) under a "Section N endnotes" heading.
func (r *renderer) appendSectionEndnotes(sec docx.Section, secIdx int) error {
	ids := collectNoteRefs(sec.Blocks, true)
	if len(ids) == 0 {
		return nil
	}
	// Filter to unique IDs that actually have bodies.
	seen := map[string]bool{}
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if _, ok := r.doc.Endnotes[id]; !ok {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil
	}
	r.ensureRoom(24)
	r.cursorY += 12
	heading := docx.Paragraph{
		Runs:         []docx.Run{{Text: "Endnotes", Props: docx.RunProps{Bold: true, FontSize: 11}}},
		SpacingAfter: 4,
	}
	_ = secIdx // section index unused in label; reserved for callers that want "Section N Endnotes"
	if err := r.drawParagraph(heading); err != nil {
		return err
	}
	for _, id := range uniq {
		label := id
		if lbl, ok := r.endnoteLabels[id]; ok && lbl != "" {
			label = lbl
		}
		marker := docx.Paragraph{
			Runs: []docx.Run{{Text: label + ". ", Props: docx.RunProps{Bold: true}}},
		}
		if err := r.drawParagraph(marker); err != nil {
			return err
		}
		for _, b := range r.doc.Endnotes[id] {
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
		label := id
		// appendNotesSection is called for both footnotes and endnotes;
		// pick the right label map by checking which one contains the
		// id (the maps are disjoint by source).
		if lbl, ok := r.footnoteLabels[id]; ok && lbl != "" {
			label = lbl
		} else if lbl, ok := r.endnoteLabels[id]; ok && lbl != "" {
			label = lbl
		}
		marker := docx.Paragraph{
			Runs: []docx.Run{{Text: label + ". ", Props: docx.RunProps{Bold: true}}},
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
	// w:distance is the horizontal gap between the body text edge and the
	// line-number column, in twips. When unset (zero) fall back to the
	// historical 18pt inset.
	distance := 18.0
	if sec.LineNumbering.DistanceTwips > 0 {
		distance = float64(sec.LineNumbering.DistanceTwips) / 20.0
	}
	x := r.marL - distance
	if x < 2 {
		x = 2
	}
	_ = r.pdf.SetFont(defaultFamily, "", 9)
	r.pdf.SetTextColor(120, 120, 120)
	n := r.lineNumCounter
	for y := r.marT; y+lineH < r.pageH-r.marB; y += lineH {
		// w:suppressLineNumbers paragraphs don't advance the counter or
		// emit a marker. We check the suppressed bands rather than
		// per-paragraph since the line-number gutter is rasterized after
		// the page body is finished.
		if isSuppressedY(r.suppressedLineNumRanges, y, y+lineH) {
			continue
		}
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

// isSuppressedY reports whether a line whose top/bottom y straddle any of
// the suppressed ranges should be skipped. A line that partially overlaps
// a suppressed band still skips — Word's behavior is conservative.
func isSuppressedY(ranges []suppressedRange, top, bottom float64) bool {
	for _, sr := range ranges {
		if bottom < sr.top || top > sr.bottom {
			continue
		}
		return true
	}
	return false
}

// pageBorderRect computes the four border-line positions for the current
// page. With offsetFrom="page" the inset is measured from the page edge;
// with offsetFrom="text" it's measured outward from the text margin (so
// the border sits BETWEEN the page edge and the margin).
func pageBorderRect(r *renderer, b docx.PageBorders) (x1, y1, x2, y2 float64) {
	top := b.OffsetTopPt
	bot := b.OffsetBottomPt
	lef := b.OffsetLeftPt
	rig := b.OffsetRightPt
	if top == 0 {
		top = 24
	}
	if bot == 0 {
		bot = 24
	}
	if lef == 0 {
		lef = 24
	}
	if rig == 0 {
		rig = 24
	}
	if b.OffsetFromText {
		// Sit `offset` outward from the text edge — i.e. closer to the
		// page edge by the offset amount.
		x1 = r.marL - lef
		y1 = r.marT - top
		x2 = r.pageW - r.marR + rig
		y2 = r.pageH - r.marB + bot
		if x1 < 1 {
			x1 = 1
		}
		if y1 < 1 {
			y1 = 1
		}
		if x2 > r.pageW-1 {
			x2 = r.pageW - 1
		}
		if y2 > r.pageH-1 {
			y2 = r.pageH - 1
		}
		return
	}
	x1 = lef
	y1 = top
	x2 = r.pageW - rig
	y2 = r.pageH - bot
	return
}

// shouldDrawPageBorder reports whether the page-border frame should appear
// on this page given the section's w:display selector:
//
//	"" / "allPages": draw on every page.
//	"firstPage":     only on the section's first page.
//	"notFirstPage": every page except the section's first.
//	"notLastPage":  every page except the section's last (Word actually
//	                spells this "lastPage" with inverted semantics; we
//	                accept either form to stay forgiving).
func shouldDrawPageBorder(display string, pageInSection int, isLastSecPage bool) bool {
	switch display {
	case "", "allPages":
		return true
	case "firstPage":
		return pageInSection == 1
	case "notFirstPage":
		return pageInSection != 1
	case "lastPage":
		return isLastSecPage
	case "notLastPage":
		return !isLastSecPage
	}
	return true
}

// drawPageBorders draws the four w:pgBorders edges. Position respects
// w:offsetFrom + each edge's w:space (offsets in points). Edges that
// carry a w:art preset render as a tiled motif glyph instead of a line.
func drawPageBorders(r *renderer, b docx.PageBorders) {
	if !(b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has()) {
		return
	}
	x1, y1, x2, y2 := pageBorderRect(r, b)
	drawEdge := func(e docx.BorderEdge, ax1, ay1, ax2, ay2 float64) {
		if e.Art != "" {
			drawArtBorder(r, e, ax1, ay1, ax2, ay2)
			return
		}
		drawCellEdge(r, e, ax1, ay1, ax2, ay2)
	}
	if b.Top.Has() {
		drawEdge(b.Top, x1, y1, x2, y1)
	}
	if b.Bottom.Has() {
		drawEdge(b.Bottom, x1, y2, x2, y2)
	}
	if b.Left.Has() {
		drawEdge(b.Left, x1, y1, x1, y2)
	}
	if b.Right.Has() {
		drawEdge(b.Right, x2, y1, x2, y2)
	}
}

// artBorderGlyph returns the unicode tile glyph + a default tint for one
// of the 165 w:pgBorders/@art preset IDs. Unknown IDs fall back to a
// generic block. Glyphs are chosen from the BMP so embedded TTFs without
// emoji coverage still render them; tint matches Word's typical color.
func artBorderGlyph(art string) (glyph string, hex string) {
	switch art {
	case "hearts", "heartGray", "heartBalloon":
		return "♥", "C00000" // ♥
	case "stars", "starsBlack", "starsShadowed", "stars3d", "starsTop":
		return "★", "FFC000" // ★
	case "snowflakes", "snowflakeFancy":
		return "❄", "5B9BD5" // ❄
	case "moons":
		return "☾", "808080" // ☾
	case "sun":
		return "☼", "FFC000" // ☼
	case "musicNotes":
		return "♫", "000000" // ♫
	case "flowersBlockPrint", "flowersDaisies", "flowersModern1",
		"flowersModern2", "flowersPansy", "flowersRedRose",
		"flowersRoses", "flowersTeacup", "flowersTiny",
		"whiteFlowers":
		return "⚘", "C00060" // ⚘
	case "christmasTree", "trees":
		return "⛄", "00B050" // ⛄ (close enough; trees rare)
	case "apples":
		return "☘", "C00000" // ☘ leaf as fallback
	case "checkered", "checkedBarBlack", "checkedBarColor":
		return "■", "000000" // ■
	case "basicBlackDots", "basicWhiteDots":
		return "•", "000000" // •
	case "basicBlackSquares", "basicWhiteSquares", "decoBlocks":
		return "■", "000000"
	case "basicBlackDashes", "basicWhiteDashes", "basicThinLines",
		"basicWideInline", "basicWideMidline", "basicWideOutline":
		return "─", "000000" // ─
	case "triangles", "triangle1", "triangle2",
		"triangleParty", "triangleCircle1", "triangleCircle2",
		"zanyTriangles":
		return "▲", "000000" // ▲
	case "circlesLines", "circlesRectangles", "rings", "ovals":
		return "○", "000000" // ○
	case "diamondsGray", "doubleDiamonds":
		return "◆", "808080" // ◆
	case "zigZag", "zigZagStitch":
		return "≀", "000000" // ⊀ zigzag
	case "sawtooth", "sawtoothGray":
		return "⁄", "000000"
	case "waveline", "classicalWave":
		return "∿", "5B9BD5" // ∿
	case "lightning1", "lightning2":
		return "⚡", "FFC000" // ⚡
	case "lightBulb":
		return "☀", "FFC000"
	case "pencils":
		return "✎", "000000" // ✎
	case "paperClips":
		return "⁂", "808080"
	case "compass":
		return "⌖", "000000" // ⌖
	case "clocks":
		return "⏰", "000000"
	case "earth1", "earth2", "earth3":
		return "♁", "0070C0" // ♁ earth
	case "fans":
		return "¤", "C00000" // ¤
	case "confetti", "confettiGrays", "confettiOutline",
		"confettiStreamers", "confettiWhite":
		return "✴", "C00060"
	case "celticKnotwork":
		return "⚝", "000000"
	case "scaredCat":
		return "¤", "000000"
	case "bats":
		return "¤", "5B5B5B"
	case "birds", "birdsFlight", "shorebirdTracks":
		return "¤", "000000"
	case "creaturesButterfly", "creaturesFish",
		"creaturesInsects", "creaturesLadyBug":
		return "⚘", "C00060"
	case "people", "peopleHats", "peopleWaving":
		return "☺", "000000" // ☺
	case "swirligig", "hypnotic":
		return "≋", "000000"
	case "vine":
		return "⚘", "00B050"
	case "holly", "poinsettias", "mapleLeaf":
		return "⚘", "00B050"
	case "gradient":
		return "█", "808080"
	case "marquee", "marqueeToothed":
		return "□", "000000" // □
	case "":
		return "", ""
	default:
		// Unknown art ID — use a small filled square so the frame is at
		// least visible as something decorative.
		return "■", "808080"
	}
}

// drawArtBorder paints a tile-glyph border for one edge. Step size is
// driven by edge.Sz (interpreted as the tile size in points; defaults to
// 10pt). Direction is inferred from coordinate equality.
func drawArtBorder(r *renderer, e docx.BorderEdge, x1, y1, x2, y2 float64) {
	glyph, fallbackHex := artBorderGlyph(e.Art)
	if glyph == "" {
		return
	}
	hex := e.Color
	if hex == "" || hex == "auto" {
		hex = fallbackHex
	}
	if hex == "" {
		hex = "000000"
	}
	size := e.Sz
	if size <= 0 {
		size = 10
	}
	if size < 4 {
		size = 4
	}
	if size > 36 {
		size = 36
	}
	rr, gg, bb := parseHexColor(hex)
	savedFontSize := r.opts.DefaultFontSize
	if err := r.pdf.SetFont(defaultFamily, "", size); err != nil {
		_ = r.pdf.SetFont(defaultFamily, "", savedFontSize)
		return
	}
	r.pdf.SetTextColor(rr, gg, bb)
	// Step along the edge by ~tile width with a small overlap so the
	// motif looks continuous.
	step := size * 0.95
	if x1 == x2 { // vertical edge
		for y := y1; y <= y2-size*0.5; y += step {
			r.pdf.SetX(x1 - size*0.5)
			r.pdf.SetY(y)
			_ = r.pdf.Cell(nil, glyph)
		}
	} else if y1 == y2 { // horizontal edge
		for x := x1; x <= x2-size*0.25; x += step {
			r.pdf.SetX(x)
			r.pdf.SetY(y1 - size*0.5)
			_ = r.pdf.Cell(nil, glyph)
		}
	}
	r.pdf.SetTextColor(0, 0, 0)
	_ = r.pdf.SetFont(defaultFamily, "", savedFontSize)
}

// applyPageBorderMargins enlarges the current marL/marR/marT/marB so they
// don't overlap with the visible page-border lines. Called after the
// section's page geometry has been set up but before content is drawn.
//
// When offsetFrom="text" the borders sit OUTSIDE the text edge, so no
// margin adjustment is necessary. When offsetFrom="page", the borders are
// at fixed insets from the page edge; if a tiny user margin would let
// content cross the border, push the margin out to clear it.
func applyPageBorderMargins(r *renderer, b docx.PageBorders) {
	if b.OffsetFromText {
		return
	}
	if !(b.Top.Has() || b.Bottom.Has() || b.Left.Has() || b.Right.Has()) {
		return
	}
	const padPt = 4.0 // extra breathing room between border and text
	getOffset := func(o float64) float64 {
		if o == 0 {
			return 24
		}
		return o
	}
	if b.Top.Has() {
		if min := getOffset(b.OffsetTopPt) + b.Top.Sz + padPt; r.marT < min {
			r.marT = min
		}
	}
	if b.Bottom.Has() {
		if min := getOffset(b.OffsetBottomPt) + b.Bottom.Sz + padPt; r.marB < min {
			r.marB = min
		}
	}
	if b.Left.Has() {
		if min := getOffset(b.OffsetLeftPt) + b.Left.Sz + padPt; r.marL < min {
			r.marL = min
		}
	}
	if b.Right.Has() {
		if min := getOffset(b.OffsetRightPt) + b.Right.Sz + padPt; r.marR < min {
			r.marR = min
		}
	}
	r.contentW = r.pageW - r.marL - r.marR
}

func (r *renderer) ensureRoom(h float64) {
	if r.noPageBreak {
		return
	}
	if r.cursorY+h > r.pageH-r.marB {
		if r.numColumns > 1 && r.colIdx < int(r.numColumns)-1 {
			r.colIdx++
			r.applyColumn(r.colIdx)
			r.cursorY = r.marT
			return
		}
		r.drawFootnotesAtBottom()
		r.newPage()
		if r.numColumns > 1 {
			r.colIdx = 0
			r.applyColumn(0)
		}
	}
}

// applyColumn sets marL / contentW for column idx. When colSpecs is set
// (unequal widths) the per-column rect is used; otherwise we use the
// equal-distribution formula.
func (r *renderer) applyColumn(idx int) {
	if len(r.colSpecs) > 0 && idx < len(r.colSpecs) {
		r.marL = r.colSpecs[idx].x
		r.contentW = r.colSpecs[idx].w
		r.colW = r.colSpecs[idx].w
		return
	}
	r.marL = r.colBaseX + float64(idx)*(r.colW+r.colGap)
	r.contentW = r.colW
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

	// If the doc declared a custom <w:footnote w:type="separator"> body,
	// draw that instead of our default thin rule. The custom separator is
	// a list of paragraphs (usually one paragraph with a tab-stop and a
	// thin line), so we render it via drawAt at the bottom-of-page anchor.
	customSep := r.doc.FootnoteSeparators["separator"]
	if len(customSep) > 0 {
		_ = r.drawAt(customSep, r.marL, startY, r.contentW)
		r.cursorY = startY + 8
	} else {
		r.pdf.SetLineWidth(0.5)
		r.pdf.SetStrokeColor(120, 120, 120)
		r.pdf.Line(r.marL, startY, r.marL+r.contentW*0.3, startY)
		r.cursorY = startY + 2
	}

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
		labels := r.footnoteLabels
		if pn.endnote {
			blocks = r.doc.Endnotes[pn.id]
			labels = r.endnoteLabels
		} else {
			blocks = r.doc.Footnotes[pn.id]
		}
		label := pn.id
		if labels != nil {
			if lbl, ok := labels[pn.id]; ok && lbl != "" {
				label = lbl
			}
		}
		marker := docx.Paragraph{
			Runs: []docx.Run{{
				Text: label + ". ", Props: docx.RunProps{Bold: true, FontSize: 9},
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
	// Wrap bands belong to the page they were established on — drop any
	// active band so content on the new page starts at full width.
	r.floatBand = nil
	r.suppressedLineNumRanges = r.suppressedLineNumRanges[:0]
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
