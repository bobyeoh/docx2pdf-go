package render

import "github.com/bobyeoh/docx2pdf-go/internal/docx"

// applyTableStyleToCells merges the named tblStyle's run defaults into
// every cell text run that doesn't already specify its own value, then
// layers in w:tblLook conditional emphasis (firstRow / lastRow / etc).
func (r *renderer) applyTableStyleToCells(t *docx.Table) {
	if t.StyleID == "" {
		return
	}
	ts, ok := r.doc.TableStyles[t.StyleID]
	if !ok {
		return
	}
	nRows := len(t.Rows)
	for ri := range t.Rows {
		nCols := len(t.Rows[ri].Cells)
		for ci := range t.Rows[ri].Cells {
			cell := &t.Rows[ri].Cells[ci]

			extra := docx.RunProps{}
			if t.Look.FirstRow && ri == 0 {
				extra.Bold = true
			}
			if t.Look.LastRow && ri == nRows-1 && nRows > 1 {
				extra.Bold = true
			}
			if t.Look.FirstColumn && ci == 0 {
				extra.Bold = true
			}
			if t.Look.LastColumn && ci == nCols-1 && nCols > 1 {
				extra.Bold = true
			}

			for bi := range cell.Blocks {
				p, ok := cell.Blocks[bi].(docx.Paragraph)
				if !ok {
					continue
				}
				for k := range p.Runs {
					base := docx.MergeRunProps(ts.Run, extra)
					p.Runs[k].Props = docx.MergeRunProps(base, p.Runs[k].Props)
				}
				cell.Blocks[bi] = p
			}
		}
	}
}

func (r *renderer) drawTable(t docx.Table) error {
	r.applyTableStyleToCells(&t)
	cols := 0
	for _, row := range t.Rows {
		if len(row.Cells) > cols {
			cols = len(row.Cells)
		}
	}
	if cols == 0 {
		return nil
	}
	widths := make([]float64, cols)
	if len(t.ColumnWidthsTwips) == cols {
		total := 0
		for _, w := range t.ColumnWidthsTwips {
			total += w
		}
		if total > 0 {
			// Use the docx-specified twip widths directly. Word stores
			// these as absolute and renders the table at exactly that
			// width — even when it overflows the page margin. Our
			// previous proportional-to-contentW scaling silently
			// shrank columns whenever the table was slightly wider than
			// the page, which forced text like "Name" in a narrow
			// header column to wrap mid-word ("Nam\ne"). Only fall back
			// to scaling when total exceeds contentW by a meaningful
			// margin AND no column width is itself wider than contentW
			// (i.e. don't try to fit a fundamentally over-wide table
			// that would just clip glyphs anyway).
			for i, w := range t.ColumnWidthsTwips {
				widths[i] = float64(w) / 20.0 // twips → pt
			}
		} else {
			for i := range widths {
				widths[i] = r.contentW / float64(cols)
			}
		}
	} else {
		for i := range widths {
			widths[i] = r.contentW / float64(cols)
		}
	}

	// Header rows repeat after each page break (leading consecutive
	// header-flagged rows per ECMA-376).
	var headerRows []docx.TableRow
	for _, row := range t.Rows {
		if !row.IsHeader {
			break
		}
		headerRows = append(headerRows, row)
	}

	for i, row := range t.Rows {
		// Pre-flight: if this is a body row and it won't fit on the
		// current page, force a page break and re-draw the header rows
		// BEFORE the row that triggered the break. Without this the
		// header lands after the row, mid-page, on every page where
		// the table continues.
		if len(headerRows) > 0 && i >= len(headerRows) && !r.noPageBreak {
			rowH := r.predictRowHeight(row, widths)
			if r.cursorY+rowH > r.pageH-r.marB {
				r.drawFootnotesAtBottom()
				r.newPage()
				for _, hr := range headerRows {
					if err := r.drawRow(hr, widths); err != nil {
						return err
					}
				}
			}
		}
		if err := r.drawRow(row, widths); err != nil {
			return err
		}
	}
	return nil
}

// predictRowHeight computes the row's rendered height without drawing
// anything. Used by drawTable for pre-flight page-break detection so we
// can inject the repeating header BEFORE the row that overflows
// (otherwise the header lands after the row, mid-page).
func (r *renderer) predictRowHeight(row docx.TableRow, widths []float64) float64 {
	cellHeights := make([]float64, len(row.Cells))
	col := 0
	for i, cell := range row.Cells {
		if col >= len(widths) {
			break
		}
		span := cell.GridSpan
		if span < 1 {
			span = 1
		}
		w := sumWidths(widths, col, span)
		if cell.VMerge == "continue" {
			cellHeights[i] = 0
		} else {
			cellHeights[i] = r.measureCell(cell, w)
		}
		col += span
	}
	rowH := 0.0
	for _, h := range cellHeights {
		if h > rowH {
			rowH = h
		}
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
	return rowH
}

func (r *renderer) drawRow(row docx.TableRow, widths []float64) error {
	rowTop := r.cursorY
	cellHeights := make([]float64, len(row.Cells))
	col := 0
	for i, cell := range row.Cells {
		if col >= len(widths) {
			break
		}
		span := cell.GridSpan
		if span < 1 {
			span = 1
		}
		w := sumWidths(widths, col, span)
		if cell.VMerge == "continue" {
			cellHeights[i] = 0
		} else {
			cellHeights[i] = r.measureCell(cell, w)
		}
		col += span
	}
	rowH := 0.0
	for _, h := range cellHeights {
		if h > rowH {
			rowH = h
		}
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

	// CantSplit: if the row won't fit on the current page, push it to the
	// next page intact rather than letting ensureRoom break it mid-row.
	// ensureRoom is already conservative when noPageBreak is set (header /
	// footer regions), so we only act when free flow is in effect.
	if row.CantSplit && !r.noPageBreak && r.cursorY+rowH > r.pageH-r.marB {
		r.drawFootnotesAtBottom()
		r.newPage()
		rowTop = r.cursorY
	} else {
		r.ensureRoom(rowH)
		if r.cursorY != rowTop {
			rowTop = r.cursorY
		}
	}

	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0, 0, 0)

	x := r.marL
	col = 0
	const defaultCellPad = 4.0
	for ci, cell := range row.Cells {
		if col >= len(widths) {
			break
		}
		span := cell.GridSpan
		if span < 1 {
			span = 1
		}
		w := sumWidths(widths, col, span)

		padL := cell.MarginLeftPt
		if padL == 0 {
			padL = defaultCellPad
		}
		padR := cell.MarginRightPt
		if padR == 0 {
			padR = defaultCellPad
		}
		padT := cell.MarginTopPt
		if padT == 0 {
			padT = defaultCellPad
		}
		padB := cell.MarginBottomPt
		if padB == 0 {
			padB = defaultCellPad
		}

		left, right := x, x+w
		top, bottom := rowTop, rowTop+rowH

		if cell.Shading != "" {
			sr, sg, sb := parseHexColor(cell.Shading)
			r.pdf.SetFillColor(sr, sg, sb)
			r.pdf.Rectangle(left, top, right, bottom, "F", 0, 0)
		}

		// Continuation cells suppress the top edge so the vMerge region
		// looks like one connected box.
		if cell.VMerge != "continue" {
			drawCellEdge(r, cell.Borders.Top, left, top, right, top)
		}
		drawCellEdge(r, cell.Borders.Bottom, left, bottom, right, bottom)
		drawCellEdge(r, cell.Borders.Left, left, top, left, bottom)
		drawCellEdge(r, cell.Borders.Right, right, top, right, bottom)

		if cell.VMerge != "continue" {
			savedY := r.cursorY
			savedMarL := r.marL
			savedContentW := r.contentW
			r.marL = x + padL
			r.contentW = w - (padL + padR)
			// Content height is what measureCell returned MINUS the
			// pad-both-sides it added internally. Using the actual
			// measurement (not a one-line-per-paragraph stub) keeps
			// vAlign="center" from pushing wrapped content past the
			// row's bottom edge — multi-line cells in this row would
			// otherwise overflow when their column happens to be the
			// tallest.
			const cellPad = 4.0
			contentH := cellHeights[ci] - 2*cellPad
			if contentH < 0 {
				contentH = 0
			}
			startY := rowTop + padT
			switch cell.VAlign {
			case "center":
				slack := rowH - contentH - (padT + padB)
				if slack > 0 {
					startY += slack / 2
				}
			case "bottom":
				slack := rowH - contentH - (padT + padB)
				if slack > 0 {
					startY += slack
				}
			}
			r.cursorY = startY
			for _, b := range cell.Blocks {
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
			r.marL = savedMarL
			r.contentW = savedContentW
			r.cursorY = savedY
		}

		x += w
		col += span
	}
	r.cursorY = rowTop + rowH
	return nil
}

// drawCellEdge draws one of a cell's four edges. An empty edge (zero
// BorderEdge) means "no border" — Word renders a table that lacks
// tblBorders/tcBorders without any lines, and we match that. Tables that
// want gridlines must declare tblBorders or tcBorders; the parser
// propagates tblBorders into each cell at parse time (see
// propagateTableBorders), so the renderer only needs to read CellBorders.
// Width is the Word-stored sz in points (1/8 pt units already converted
// upstream).
func drawCellEdge(r *renderer, e docx.BorderEdge, x1, y1, x2, y2 float64) {
	if !e.Has() || e.Style == "none" || e.Style == "nil" {
		return
	}
	width := e.Sz
	if width <= 0 {
		width = 0.5
	}
	if e.Color != "" && e.Color != "auto" {
		rr, gg, bb := parseHexColor(e.Color)
		r.pdf.SetStrokeColor(rr, gg, bb)
	} else {
		r.pdf.SetStrokeColor(0, 0, 0)
	}
	r.pdf.SetLineWidth(width)

	switch e.Style {
	case "double":
		offX, offY := 0.0, 0.0
		if y1 == y2 {
			offY = 1
		} else {
			offX = 1
		}
		r.pdf.Line(x1-offX, y1-offY, x2-offX, y2-offY)
		r.pdf.Line(x1+offX, y1+offY, x2+offX, y2+offY)
	case "dashed":
		drawDashedLine(r, x1, y1, x2, y2, 3, 2)
	case "dotted":
		drawDashedLine(r, x1, y1, x2, y2, 1, 2)
	default:
		r.pdf.Line(x1, y1, x2, y2)
	}
}

// drawDashedLine renders a dash pattern by stepping in fixed-length segments.
// gopdf has SetLineType but it's globally stateful and easy to leak — drawing
// the dashes ourselves keeps each call self-contained.
func drawDashedLine(r *renderer, x1, y1, x2, y2, dash, gap float64) {
	dx, dy := x2-x1, y2-y1
	length := dx*dx + dy*dy
	if length == 0 {
		return
	}
	if y1 == y2 {
		for x := x1; x < x2; x += dash + gap {
			end := x + dash
			if end > x2 {
				end = x2
			}
			r.pdf.Line(x, y1, end, y1)
		}
	} else if x1 == x2 {
		for y := y1; y < y2; y += dash + gap {
			end := y + dash
			if end > y2 {
				end = y2
			}
			r.pdf.Line(x1, y, x1, end)
		}
	}
}

// cellContentHeight estimates the rendered height of a cell's contents at
// the renderer's current contentW. Used for vAlign slack math.
func sumWidths(ws []float64, start, n int) float64 {
	sum := 0.0
	for i := start; i < start+n && i < len(ws); i++ {
		sum += ws[i]
	}
	return sum
}

// measureCell estimates rendered height for a cell at the given content
// width. Does a dry layout reusing the line-breaker math without drawing.
//
// runsToAtoms has the side effect of queuing footnote IDs onto
// pendingFootnotes, so we save and restore that slice — otherwise table
// cells with footnote refs would queue each note twice (once in measure,
// once in the real draw) and the page bottom would render duplicates.
func (r *renderer) measureCell(cell docx.TableCell, width float64) float64 {
	const cellPad = 4.0
	h := 2 * cellPad
	innerW := width - 2*cellPad
	savedLine := r.lineHeight
	savedFootnotes := r.pendingFootnotes
	defer func() {
		r.lineHeight = savedLine
		r.pendingFootnotes = savedFootnotes
	}()
	for _, p := range cell.Paragraphs() {
		r.lineHeight = p.LineHeight
		atoms := r.runsToAtoms(p.Runs)
		lineW := 0.0
		lineH := r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
		hadAny := false
		// Inline helper mirroring layoutLine's accumulator. Defined as a
		// closure so the per-rune fallback below can recurse cleanly.
		accumulate := func(a atom) {
			ah := atomHeight(a, r.opts.DefaultFontSize)
			if lineW+a.width > innerW && lineW > 0 {
				h += lineH
				lineW = 0
				lineH = r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
				if a.kind == atomSpace {
					return
				}
			}
			lineW += a.width
			scaled := r.applyLineHeight(ah)
			if scaled > lineH {
				lineH = scaled
			}
			hadAny = true
		}
		for _, a := range atoms {
			if a.kind == atomBookmark {
				continue
			}
			if a.kind == atomBreak || a.kind == atomPageBreak {
				h += lineH
				lineW = 0
				lineH = r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
				hadAny = false
				continue
			}
			// Over-wide word — same fresh-line-first fallback as
			// layoutLine. Flush the current line so the atom can try a
			// fresh line; only split per rune if it still doesn't fit.
			// Without this, measureCell would compute a too-small height
			// for the cell and the real draw would overflow into the
			// row below.
			if a.kind == atomWord && innerW > 0 && a.width > innerW && a.text != "" {
				if lineW > 0 {
					h += lineH
					lineW = 0
					lineH = r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
				}
				if a.width > innerW {
					for _, sub := range r.splitWordAtomByRune(a) {
						accumulate(sub)
					}
					continue
				}
			}
			accumulate(a)
		}
		if hadAny || lineW > 0 || len(atoms) == 0 {
			h += lineH
		}
		h += p.SpacingBefore + p.SpacingAfter
	}
	return h
}
