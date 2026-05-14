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
					base := mergeForRender(ts.Run, extra)
					p.Runs[k].Props = mergeForRender(base, p.Runs[k].Props)
				}
				cell.Blocks[bi] = p
			}
		}
	}
}

// mergeForRender is the renderer-side equivalent of mergeRunProps from
// the docx package. "child" wins where set.
func mergeForRender(parent, child docx.RunProps) docx.RunProps {
	out := parent
	if child.Bold {
		out.Bold = true
	}
	if child.Italic {
		out.Italic = true
	}
	if child.Underline {
		out.Underline = true
	}
	if child.Strike {
		out.Strike = true
	}
	if child.Caps {
		out.Caps = true
	}
	if child.SmallCaps {
		out.SmallCaps = true
	}
	if child.FontSize != 0 {
		out.FontSize = child.FontSize
	}
	if child.FontFamily != "" {
		out.FontFamily = child.FontFamily
	}
	if child.Color != "" {
		out.Color = child.Color
	}
	if child.ThemeColor != "" {
		out.ThemeColor = child.ThemeColor
	}
	if child.Highlight != "" {
		out.Highlight = child.Highlight
	}
	if child.Shading != "" {
		out.Shading = child.Shading
	}
	if child.VertAlign != "" {
		out.VertAlign = child.VertAlign
	}
	if child.LetterSpacingPt != 0 {
		out.LetterSpacingPt = child.LetterSpacingPt
	}
	if child.TextEffect != "" {
		out.TextEffect = child.TextEffect
	}
	return out
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
			for i, w := range t.ColumnWidthsTwips {
				widths[i] = r.contentW * float64(w) / float64(total)
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
		pageBefore := r.pdf.GetNumberOfPages()
		if err := r.drawRow(row, widths); err != nil {
			return err
		}
		pageAfter := r.pdf.GetNumberOfPages()
		if pageAfter > pageBefore && len(headerRows) > 0 && i >= len(headerRows) {
			for _, hr := range headerRows {
				if err := r.drawRow(hr, widths); err != nil {
					return err
				}
			}
		}
	}
	return nil
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

	r.ensureRoom(rowH)
	if r.cursorY != rowTop {
		rowTop = r.cursorY
	}

	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(0, 0, 0)

	x := r.marL
	col = 0
	const defaultCellPad = 4.0
	for _, cell := range row.Cells {
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
			contentH := cellContentHeight(cell)
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

// drawCellEdge draws one of a cell's four edges. The default (zero edge)
// is a thin black solid line matching docx4j's behavior. Width is the
// Word-stored sz in points (1/8 pt units already converted upstream).
func drawCellEdge(r *renderer, e docx.BorderEdge, x1, y1, x2, y2 float64) {
	if e.Style == "none" || e.Style == "nil" {
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
func cellContentHeight(c docx.TableCell) float64 {
	n := 0
	for _, p := range c.Paragraphs() {
		if len(p.Runs) == 0 {
			n++
			continue
		}
		n += 1
	}
	if n == 0 {
		n = 1
	}
	return float64(n) * 13.2 // approx 11pt × 1.2
}

func sumWidths(ws []float64, start, n int) float64 {
	sum := 0.0
	for i := start; i < start+n && i < len(ws); i++ {
		sum += ws[i]
	}
	return sum
}

// measureCell estimates rendered height for a cell at the given content
// width. Does a dry layout reusing the line-breaker math without drawing.
func (r *renderer) measureCell(cell docx.TableCell, width float64) float64 {
	const cellPad = 4.0
	h := 2 * cellPad
	innerW := width - 2*cellPad
	savedLine := r.lineHeight
	defer func() { r.lineHeight = savedLine }()
	for _, p := range cell.Paragraphs() {
		r.lineHeight = p.LineHeight
		atoms := r.runsToAtoms(p.Runs)
		lineW := 0.0
		lineH := r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
		hadAny := false
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
			ah := atomHeight(a, r.opts.DefaultFontSize)
			if lineW+a.width > innerW && lineW > 0 {
				h += lineH
				lineW = 0
				lineH = r.applyLineHeight(r.opts.DefaultFontSize * 1.2)
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
		h += p.SpacingBefore + p.SpacingAfter
	}
	return h
}
