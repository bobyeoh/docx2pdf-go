package render

import (
	"hash/fnv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// applyRevisionPolicy filters or decorates runs based on Options.ShowRevisions.
//
//   - Default (ShowRevisions=false): runs whose RevisionType is "del" or
//     "moveFrom" are dropped — preserving the "Accept All Changes" behavior
//     the parser used to bake in.
//   - ShowRevisions=true: every revision-tagged run is kept and decorated:
//     ins/moveTo get an underline and a color (default blue, or derived
//     from the author name); del/moveFrom get a strikethrough and a color
//     (default red). The existing run-level props win when they conflict.
//
// Marker runs (field structure, bookmarks, footnotes, etc.) pass through
// unchanged regardless of mode — they carry layout, not visible text.
func (r *renderer) applyRevisionPolicy(runs []docx.Run) []docx.Run {
	if !r.opts.ShowRevisions {
		out := runs[:0:0]
		dropped := false
		for _, run := range runs {
			switch run.RevisionType {
			case "del", "moveFrom":
				dropped = true
				continue
			}
			out = append(out, run)
		}
		if !dropped {
			return runs
		}
		return out
	}
	out := make([]docx.Run, 0, len(runs))
	for _, run := range runs {
		switch run.RevisionType {
		case "ins", "moveTo":
			if !run.Props.Underline {
				run.Props.Underline = true
			}
			if run.Props.Color == "" {
				run.Props.Color = revisionColorForAuthor(run.RevisionAuthor, "0000C0")
			}
		case "del", "moveFrom":
			if !run.Props.Strike {
				run.Props.Strike = true
			}
			if run.Props.Color == "" {
				run.Props.Color = revisionColorForAuthor(run.RevisionAuthor, "C00000")
			}
		}
		out = append(out, run)
	}
	return out
}

// paragraphHasRevision reports whether any run in the paragraph carries a
// tracked-change tag. Marker / structural runs (FieldBegin, IsBreak only,
// bookmarks) without a RevisionType count as "no change here" so an
// inserted paragraph break alone doesn't paint a bar.
//
// Property changes (w:pPrChange / w:rPrChange) also count — Word puts a
// change bar in the margin whenever any aspect of the paragraph was edited,
// not just the run text itself.
func paragraphHasRevision(p docx.Paragraph) bool {
	if p.PrChange != nil {
		return true
	}
	for _, r := range p.Runs {
		switch r.RevisionType {
		case "ins", "del", "moveFrom", "moveTo":
			return true
		}
		if r.PrChange != nil {
			return true
		}
	}
	return false
}

// tableHasRevision returns true when the table itself or any of its rows /
// cells carries a tracked property change. The renderer uses this to draw
// a margin change bar alongside the table body.
func tableHasRevision(t docx.Table) bool {
	if t.PrChange != nil {
		return true
	}
	for _, row := range t.Rows {
		if row.PrChange != nil {
			return true
		}
		for _, c := range row.Cells {
			if c.PrChange != nil || c.CellRevision != nil {
				return true
			}
		}
	}
	return false
}

// drawCellRevisionMarker paints a small marker on the cell to surface the
// w:cellIns / w:cellDel / w:cellMerge tracked-change tag. Colors follow the
// run-level palette: blue for insertions, red for deletions, purple for
// merges. The cell's text content already renders normally — the marker
// sits on top in the cell's top-left corner so reviewers can see what
// changed without losing readability.
func (r *renderer) drawCellRevisionMarker(rev *docx.CellRevision, x, y, width, height float64) {
	if rev == nil || height <= 0 || width <= 0 {
		return
	}
	color := "0000C0"
	letter := "I"
	switch rev.Kind {
	case "del":
		color = "C00000"
		letter = "D"
	case "merge":
		color = "7030A0"
		letter = "M"
	}
	if rev.Author != "" {
		color = revisionColorForAuthor(rev.Author, color)
	}
	rr, gg, bb := parseHexColor(color)
	r.pdf.SetLineWidth(1.2)
	r.pdf.SetStrokeColor(rr, gg, bb)
	r.pdf.Line(x, y, x, y+height) // change bar along the cell's left edge
	r.pdf.SetFillColor(rr, gg, bb)
	r.pdf.Oval(x+1, y+1, x+9, y+9)
	r.pdf.SetFontSize(6)
	r.pdf.SetX(x + 3)
	r.pdf.SetY(y + 2)
	r.pdf.SetTextColor(255, 255, 255)
	_ = r.pdf.Cell(nil, letter)
	r.pdf.SetTextColor(0, 0, 0)
	r.pdf.SetFontSize(r.opts.DefaultFontSize)
}

// drawRevisionChangeBar paints a vertical bar to the left of the content
// area between topY and bottomY. The bar lives in the left margin so it
// never crowds the body text. We reset stroke state to gopdf's defaults
// afterwards rather than saving/restoring — the renderer always sets
// stroke color + width before drawing, so a stale state here is harmless.
func (r *renderer) drawRevisionChangeBar(topY, bottomY float64) {
	if bottomY <= topY {
		return
	}
	x := r.marL - 6
	if x < 2 {
		x = 2
	}
	r.pdf.SetLineWidth(1)
	r.pdf.SetStrokeColor(0x80, 0x80, 0x80)
	r.pdf.Line(x, topY, x, bottomY)
}

// revisionColorForAuthor hashes an author string into one of a handful of
// readable hex colors so different reviewers' edits visually disambiguate.
// Empty author returns the supplied fallback.
func revisionColorForAuthor(author, fallback string) string {
	if author == "" {
		return fallback
	}
	palette := []string{
		"C00000", // deep red
		"0070C0", // strong blue
		"007A33", // forest green
		"7030A0", // purple
		"BF6900", // burnt orange
		"006A6A", // teal
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(author))
	return palette[int(h.Sum32())%len(palette)]
}
