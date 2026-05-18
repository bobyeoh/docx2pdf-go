package render

import (
	"sort"
	"strconv"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// buildNoteLabels computes a display label for every footnote/endnote ID
// in the document, honoring per-section NumFmt + NumStart + Restart
// settings from w:footnotePr / w:endnotePr.
//
// Numbering rules:
//   - "continuous" (default): one counter for the whole doc.
//   - "eachSect": counter resets at every section boundary.
//   - "eachPage": ideally resets at every page break, but we don't
//     have page-break visibility at this stage. Fall back to continuous.
//
// Note IDs that don't appear in any section's body still get a label —
// they're indexed in document order to give the trailing "Endnotes" list
// a stable, reproducible ordering.
func buildNoteLabels(doc *docx.Document, endnote bool) map[string]string {
	noteMap := doc.Footnotes
	if endnote {
		noteMap = doc.Endnotes
	}
	if len(noteMap) == 0 {
		return nil
	}
	// Per section, collect referenced IDs in body order.
	type secRefs struct {
		secIdx int
		ids    []string
	}
	var allRefs []secRefs
	for i, sec := range doc.Sections {
		refs := collectNoteRefs(sec.Blocks, endnote)
		allRefs = append(allRefs, secRefs{secIdx: i, ids: refs})
	}
	if len(allRefs) == 0 {
		// Fallback: walk top-level body.
		refs := collectNoteRefs(doc.Body, endnote)
		allRefs = append(allRefs, secRefs{secIdx: 0, ids: refs})
	}
	// Determine effective NumFmt + Restart from doc + per-section overrides.
	out := map[string]string{}
	counter := 0
	defaultFmt := "decimal"
	defaultStart := 1
	defaultRestart := "continuous"
	for _, sr := range allRefs {
		fmt := defaultFmt
		start := defaultStart
		restart := defaultRestart
		if sr.secIdx < len(doc.Sections) {
			sec := doc.Sections[sr.secIdx]
			cfg := sec.FootnotePr
			if endnote {
				cfg = sec.EndnotePr
			}
			if cfg != nil {
				if cfg.NumFmt != "" {
					fmt = cfg.NumFmt
				}
				if cfg.NumStart > 0 {
					start = cfg.NumStart
				}
				if cfg.Restart != "" {
					restart = cfg.Restart
				}
			}
		}
		if restart == "eachSect" || restart == "eachPage" {
			// eachPage is best-effort: without auto-pagination at this
			// stage we cannot tell where page boundaries land, so we
			// degrade to per-section reset. Users get more correct
			// resets than treating it as continuous; a fully accurate
			// page-aware pass would have to run after layout.
			counter = start - 1
		} else if counter == 0 {
			counter = start - 1
		}
		for _, id := range sr.ids {
			counter++
			out[id] = formatNoteNumber(counter, fmt)
		}
	}
	// Any ID we didn't observe a reference for (orphan in the notes XML)
	// still gets a label so the trailer page renders something stable.
	missing := make([]string, 0)
	for id := range noteMap {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		a, _ := strconv.Atoi(missing[i])
		b, _ := strconv.Atoi(missing[j])
		return a < b
	})
	for _, id := range missing {
		counter++
		out[id] = formatNoteNumber(counter, defaultFmt)
	}
	return out
}

func collectNoteRefs(blocks []docx.Block, endnote bool) []string {
	var out []string
	var walk func(blocks []docx.Block)
	walk = func(blocks []docx.Block) {
		for _, b := range blocks {
			switch v := b.(type) {
			case docx.Paragraph:
				for _, r := range v.Runs {
					if r.FootnoteID == "" {
						continue
					}
					if r.IsEndnote != endnote {
						continue
					}
					out = append(out, r.FootnoteID)
				}
			case docx.Table:
				for _, row := range v.Rows {
					for _, cell := range row.Cells {
						walk(cell.Blocks)
					}
				}
			}
		}
	}
	walk(blocks)
	return out
}

// formatNoteNumber renders n in the requested Word numFmt. Supported:
// "decimal" (default), "upperRoman", "lowerRoman", "upperLetter",
// "lowerLetter", "chicago" (= *, †, ‡, §, ‖, ¶, then repeat).
func formatNoteNumber(n int, fmt string) string {
	switch fmt {
	case "upperRoman":
		return roman(n, true)
	case "lowerRoman":
		return roman(n, false)
	case "upperLetter":
		return alphaLabel(n, true)
	case "lowerLetter":
		return alphaLabel(n, false)
	case "chicago":
		// Chicago footnote symbols.
		symbols := []string{"*", "†", "‡", "§", "‖", "¶"}
		idx := (n - 1) % len(symbols)
		dup := (n - 1) / len(symbols)
		s := symbols[idx]
		for i := 0; i < dup; i++ {
			s += s
		}
		return s
	}
	return strconv.Itoa(n)
}
