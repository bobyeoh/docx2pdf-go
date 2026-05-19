package render

import (
	"unicode"

	"golang.org/x/text/unicode/bidi"
)

// reorderBidi runs UAX#9 paragraph-level resolution on s and returns the
// visual-order string for a paragraph whose default direction is `paraRTL`
// (Right-to-Left when true). For pure-LTR input the result is identical
// to the input; mixed input gets segmented into runs and each RTL run
// is reverse-ordered to produce the visual flow gopdf will draw left-to-right.
//
// Compared to the previous reverseRunes(allRTL) heuristic, this handles:
//   - LTR digits / punctuation embedded in Arabic prose
//   - LTR English words embedded in RTL paragraphs
//   - RTL Hebrew words embedded in LTR paragraphs
//
// Returns the original string if the bidi resolver errors (defensive).
func reorderBidi(s string, paraRTL bool) string {
	if s == "" {
		return s
	}
	// Fast path: if no character has even a hint of RTL, return as-is.
	any := false
	for _, r := range s {
		if isRTL(r) {
			any = true
			break
		}
	}
	if !any && !paraRTL {
		return s
	}
	p := bidi.Paragraph{}
	def := bidi.LeftToRight
	if paraRTL {
		def = bidi.RightToLeft
	}
	p.SetString(s, bidi.DefaultDirection(def))
	order, err := p.Order()
	if err != nil {
		return s
	}
	var out []rune
	for i := 0; i < order.NumRuns(); i++ {
		r := order.Run(i)
		txt := r.String()
		runes := []rune(txt)
		if r.Direction() == bidi.RightToLeft {
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
		}
		out = append(out, runes...)
	}
	return string(out)
}

// shapeArabic performs Arabic letter shaping on s. For every Arabic
// codepoint in the input we pick its Initial / Medial / Final /
// Isolated presentation form (U+FE70 .. U+FEFC range, "Arabic
// Presentation Forms-B") based on its neighbors' join semantics.
//
// The algorithm:
//  1. Walk the string and classify each rune as Non-joining ("U"),
//     Right-joining ("R"), Dual-joining ("D"), or Transparent ("T",
//     skip when looking at neighbors).
//  2. For each Arabic letter compute "joins-previous" (previous non-T
//     rune is D or L) and "joins-next" (next non-T rune is D or R).
//  3. Select the form: Initial if (!prev && next), Medial if (prev &&
//     next), Final if (prev && !next), Isolated otherwise.
//  4. Look up the presentation codepoint from arabicShapingTable.
//
// Non-Arabic runes pass through unchanged. The input is assumed to
// already be in visual order (post-bidi).
func shapeArabic(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	// Pre-classify joins for each position.
	// joinPrev[i] = letter at i links to its right (in logical reading
	// order), i.e. its predecessor in the visual sequence above.
	hasArabic := false
	for _, r := range runes {
		if r >= 0x0600 && r <= 0x06FF {
			hasArabic = true
			break
		}
	}
	if !hasArabic {
		return s
	}
	// Lam-alef ligature substitution: collapse a lam (U+0644) followed
	// in *logical* order by an alef variant into a single visual ligature.
	// reorderBidi has already mapped to visual order, so in this slice an
	// Arabic word's last logical char is at the lowest index. That makes
	// the lam appear AFTER its alef neighbor here — so the pattern to
	// match is "alef at i, lam at i+1".
	//
	// The combined codepoint comes from the Arabic Presentation Forms-A
	// (FEF5..FEFC) block:
	//
	//	lam + alef-madda   → FEF5 (isolated) / FEF6 (final)
	//	lam + alef-hamza-a → FEF7 (isolated) / FEF8 (final)
	//	lam + alef-hamza-b → FEF9 (isolated) / FEFA (final)
	//	lam + alef plain   → FEFB (isolated) / FEFC (final)
	//
	// Selection between isolated and final depends on whether the lam
	// connects to a previous letter (visual right neighbour). The substitution
	// reuses arabicJoinClass to detect that, then writes the ligature in the
	// alef slot and removes the lam.
	if len(runes) >= 2 {
		out := make([]rune, 0, len(runes))
		for i := 0; i < len(runes); i++ {
			if i+1 < len(runes) && runes[i+1] == 0x0644 {
				switch runes[i] {
				case 0x0622, 0x0623, 0x0625, 0x0627:
					// Lam's predecessor in logical reading order is the
					// rune to its right in logical order — which sits at
					// i+2 in our already-bidi-reordered visual slice. If
					// that letter joins on its left, the lam takes the
					// FINAL form; otherwise the ligature is ISOLATED.
					joinsNext := false
					for k := i + 2; k < len(runes); k++ {
						if isTransparentJoin(runes[k]) {
							continue
						}
						jc := arabicJoinClass(runes[k])
						if jc == "D" || jc == "R" {
							joinsNext = true
						}
						break
					}
					var lig rune
					switch runes[i] {
					case 0x0622:
						if joinsNext {
							lig = 0xFEF6
						} else {
							lig = 0xFEF5
						}
					case 0x0623:
						if joinsNext {
							lig = 0xFEF8
						} else {
							lig = 0xFEF7
						}
					case 0x0625:
						if joinsNext {
							lig = 0xFEFA
						} else {
							lig = 0xFEF9
						}
					case 0x0627:
						if joinsNext {
							lig = 0xFEFC
						} else {
							lig = 0xFEFB
						}
					}
					out = append(out, lig)
					i++ // consume the lam
					continue
				}
			}
			out = append(out, runes[i])
		}
		runes = out
	}
	out := make([]rune, len(runes))
	for i, r := range runes {
		shape, ok := arabicShapingTable[r]
		if !ok {
			out[i] = r
			continue
		}
		joinsPrev := false
		joinsNext := false
		// Look left (visual): we've already reversed via reorderBidi,
		// so "left in visual order" corresponds to "next in logical
		// order" for Arabic words. Treat i+1 as "previous in logical
		// reading order" and i-1 as "next in logical reading order".
		for j := i + 1; j < len(runes); j++ {
			if isTransparentJoin(runes[j]) {
				continue
			}
			jc := arabicJoinClass(runes[j])
			if jc == "D" || jc == "L" {
				joinsPrev = true
			}
			break
		}
		for j := i - 1; j >= 0; j-- {
			if isTransparentJoin(runes[j]) {
				continue
			}
			jc := arabicJoinClass(runes[j])
			if jc == "D" || jc == "R" {
				joinsNext = true
			}
			break
		}
		switch {
		case joinsPrev && joinsNext:
			out[i] = shape.Medial
		case joinsPrev:
			out[i] = shape.Final
		case joinsNext:
			out[i] = shape.Initial
		default:
			out[i] = shape.Isolated
		}
		// Fallback to isolated when the shaped slot is empty (rare —
		// some Arabic letters lack a Medial form).
		if out[i] == 0 {
			out[i] = shape.Isolated
		}
		if out[i] == 0 {
			out[i] = r
		}
	}
	return string(out)
}

// arabicForms holds the four presentation forms of an Arabic letter.
// A zero codepoint means "this form doesn't exist for this letter"
// (most non-joining letters lack Medial/Initial).
type arabicForms struct {
	Isolated, Initial, Medial, Final rune
}

// arabicJoinClass returns the UCD Joining_Type letter for a codepoint:
// "D" Dual_Joining, "R" Right_Joining, "L" Left_Joining (rare), "U"
// Non_Joining, "T" Transparent. Coverage is the Arabic block + a few
// transparent marks. Letters outside the table get "U".
func arabicJoinClass(r rune) string {
	switch r {
	// Transparent: combining marks above/below Arabic letters.
	case 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0618,
		0x0619, 0x061A,
		0x064B, 0x064C, 0x064D, 0x064E, 0x064F, 0x0650, 0x0651, 0x0652, 0x0653,
		0x0654, 0x0655, 0x0656, 0x0657, 0x0658, 0x0659, 0x065A, 0x065B, 0x065C,
		0x065D, 0x065E, 0x065F, 0x0670:
		return "T"
	}
	if c, ok := arabicShapingTable[r]; ok {
		// If only isolated+final exist, it's Right-joining; if all four
		// forms exist, Dual-joining.
		if c.Initial != 0 || c.Medial != 0 {
			return "D"
		}
		return "R"
	}
	return "U"
}

func isTransparentJoin(r rune) bool {
	return arabicJoinClass(r) == "T"
}

// arabicShapingTable maps each Arabic letter codepoint to its four
// presentation forms in the U+FE70..FEFC block. Coverage is the
// 36 basic letters of the Arabic alphabet plus alef-hamza variants and
// the lam-alef ligature. Letters not in the table render as-is.
//
// Source: Unicode 15 "Arabic Presentation Forms-B" (FE70..FEFC). The
// table is hand-curated from that block to avoid pulling in a large
// data dependency. Entries marked Initial=0 or Medial=0 mean that
// letter only takes Isolated and Final forms.
var arabicShapingTable = map[rune]arabicForms{
	0x0621: {0xFE80, 0, 0, 0},                // ء HAMZA
	0x0622: {0xFE81, 0, 0, 0xFE82},           // آ ALEF WITH MADDA ABOVE
	0x0623: {0xFE83, 0, 0, 0xFE84},           // أ ALEF WITH HAMZA ABOVE
	0x0624: {0xFE85, 0, 0, 0xFE86},           // ؤ WAW WITH HAMZA ABOVE
	0x0625: {0xFE87, 0, 0, 0xFE88},           // إ ALEF WITH HAMZA BELOW
	0x0626: {0xFE89, 0xFE8B, 0xFE8C, 0xFE8A}, // ئ YEH WITH HAMZA ABOVE
	0x0627: {0xFE8D, 0, 0, 0xFE8E},           // ا ALEF
	0x0628: {0xFE8F, 0xFE91, 0xFE92, 0xFE90}, // ب BEH
	0x0629: {0xFE93, 0, 0, 0xFE94},           // ة TEH MARBUTA
	0x062A: {0xFE95, 0xFE97, 0xFE98, 0xFE96}, // ت TEH
	0x062B: {0xFE99, 0xFE9B, 0xFE9C, 0xFE9A}, // ث THEH
	0x062C: {0xFE9D, 0xFE9F, 0xFEA0, 0xFE9E}, // ج JEEM
	0x062D: {0xFEA1, 0xFEA3, 0xFEA4, 0xFEA2}, // ح HAH
	0x062E: {0xFEA5, 0xFEA7, 0xFEA8, 0xFEA6}, // خ KHAH
	0x062F: {0xFEA9, 0, 0, 0xFEAA},           // د DAL
	0x0630: {0xFEAB, 0, 0, 0xFEAC},           // ذ THAL
	0x0631: {0xFEAD, 0, 0, 0xFEAE},           // ر REH
	0x0632: {0xFEAF, 0, 0, 0xFEB0},           // ز ZAIN
	0x0633: {0xFEB1, 0xFEB3, 0xFEB4, 0xFEB2}, // س SEEN
	0x0634: {0xFEB5, 0xFEB7, 0xFEB8, 0xFEB6}, // ش SHEEN
	0x0635: {0xFEB9, 0xFEBB, 0xFEBC, 0xFEBA}, // ص SAD
	0x0636: {0xFEBD, 0xFEBF, 0xFEC0, 0xFEBE}, // ض DAD
	0x0637: {0xFEC1, 0xFEC3, 0xFEC4, 0xFEC2}, // ط TAH
	0x0638: {0xFEC5, 0xFEC7, 0xFEC8, 0xFEC6}, // ظ ZAH
	0x0639: {0xFEC9, 0xFECB, 0xFECC, 0xFECA}, // ع AIN
	0x063A: {0xFECD, 0xFECF, 0xFED0, 0xFECE}, // غ GHAIN
	0x0640: {0x0640, 0x0640, 0x0640, 0x0640}, // ـ TATWEEL (always joining bridge)
	0x0641: {0xFED1, 0xFED3, 0xFED4, 0xFED2}, // ف FEH
	0x0642: {0xFED5, 0xFED7, 0xFED8, 0xFED6}, // ق QAF
	0x0643: {0xFED9, 0xFEDB, 0xFEDC, 0xFEDA}, // ك KAF
	0x0644: {0xFEDD, 0xFEDF, 0xFEE0, 0xFEDE}, // ل LAM
	0x0645: {0xFEE1, 0xFEE3, 0xFEE4, 0xFEE2}, // م MEEM
	0x0646: {0xFEE5, 0xFEE7, 0xFEE8, 0xFEE6}, // ن NOON
	0x0647: {0xFEE9, 0xFEEB, 0xFEEC, 0xFEEA}, // ه HEH
	0x0648: {0xFEED, 0, 0, 0xFEEE},           // و WAW
	0x0649: {0xFEEF, 0, 0, 0xFEF0},           // ى ALEF MAKSURA
	0x064A: {0xFEF1, 0xFEF3, 0xFEF4, 0xFEF2}, // ي YEH
}

// isRTLOrArabic is the shaping-aware sibling of isRTL: it also marks
// the U+FE70..FEFC presentation block (post-shaping output) as RTL so
// downstream code that mixes shaped+unshaped strings stays consistent.
func isRTLOrArabic(r rune) bool {
	if isRTL(r) {
		return true
	}
	return r >= 0xFE70 && r <= 0xFEFC
}

// Silence unused warning when downstream callers shift around.
var _ = unicode.IsLetter
