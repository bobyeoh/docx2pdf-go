package render

// Arabic shaping. Arabic letters have up to four contextual glyph
// forms — isolated, initial (start of a word), medial (between two
// joinable letters), and final (end of a word) — but Word stores text
// as the base letter (U+0621..U+064A) and expects the renderer to
// pick the right form. gopdf has no shaping engine, so we substitute
// presentation-form codepoints from the Arabic Presentation Forms-B
// block (U+FE70..U+FEFF). Every modern CJK / Noto fallback covers
// that block, so the substituted glyphs render correctly.
//
// This is a pragmatic implementation — not a full OpenType shaper.
// What it covers:
//   - The 36 base Arabic letters in the main block (U+0620..U+064A).
//   - LAM+ALEF ligatures (4 variants × isolated/final).
//   - Tatweel (kashida) U+0640 — joining causal letter.
//   - Tashkeel (U+064B..U+0652) treated as transparent for joining.
//
// What's NOT covered:
//   - Arabic Supplement (U+0750..U+077F) and extended forms.
//   - Optional ligatures like LAM+HAH, LAM+MEEM, BEH+JEEM, etc.
//   - Full OpenType GSUB lookups (kerning, alternates, contextual
//     swashes). A real shaper like HarfBuzz does this.
//
// Input must already be in logical order (not visually reversed).
// shapeArabic returns logical-order text with presentation forms
// substituted; the caller is responsible for any rune reversal
// needed for RTL drawing (see flushBuf in text.go).

// arabicJoiningType encodes how a rune participates in cursive
// joining. Mirrors the Unicode "Joining_Type" property values that
// matter for shaping decisions.
type arabicJoiningType uint8

const (
	ajtNon         arabicJoiningType = iota // U — never joins
	ajtRight                                // R — joins previous letter only
	ajtDual                                 // D — joins both sides
	ajtCausing                              // C — joins both sides, no shaping itself (Tatweel)
	ajtTransparent                          // T — skipped when looking at neighbors (combining marks)
)

// arabicForms holds the four presentation-form codepoints for one
// base letter. Zero means "this form doesn't exist for this letter"
// — happens with R letters that only have isolated + final.
type arabicForms struct {
	Isolated, Final, Initial, Medial rune
}

// arabicJoiningTable returns the joining type for one rune. The
// table is the subset of the Unicode joining-type database that
// covers the main Arabic block. Runes outside the block return
// ajtNon (effectively "not Arabic, doesn't shape").
func arabicJoiningTable(r rune) arabicJoiningType {
	switch r {
	// U — non-joining.
	case 0x0621:
		return ajtNon
	// R — joins previous only.
	case 0x0622, 0x0623, 0x0624, 0x0625, 0x0627,
		0x0629, 0x062F, 0x0630, 0x0631, 0x0632, 0x0648:
		return ajtRight
	// D — joins both.
	case 0x0626, 0x0628, 0x062A, 0x062B, 0x062C, 0x062D, 0x062E,
		0x0633, 0x0634, 0x0635, 0x0636, 0x0637, 0x0638, 0x0639, 0x063A,
		0x0641, 0x0642, 0x0643, 0x0644, 0x0645, 0x0646, 0x0647,
		0x0649, 0x064A:
		return ajtDual
	// C — causing (Tatweel/kashida).
	case 0x0640:
		return ajtCausing
	}
	// Tashkeel (diacritics) — transparent.
	if r >= 0x064B && r <= 0x0652 {
		return ajtTransparent
	}
	if r == 0x0670 { // ARABIC LETTER SUPERSCRIPT ALEF — transparent
		return ajtTransparent
	}
	return ajtNon
}

// arabicShapeTable maps a base Arabic letter to its 4 presentation
// forms. Zero in any field means "fall back to isolated" so an R
// letter mistakenly placed in initial position still renders.
func arabicShapeTable(r rune) (arabicForms, bool) {
	switch r {
	case 0x0621:
		return arabicForms{Isolated: 0xFE80}, true
	case 0x0622:
		return arabicForms{Isolated: 0xFE81, Final: 0xFE82}, true
	case 0x0623:
		return arabicForms{Isolated: 0xFE83, Final: 0xFE84}, true
	case 0x0624:
		return arabicForms{Isolated: 0xFE85, Final: 0xFE86}, true
	case 0x0625:
		return arabicForms{Isolated: 0xFE87, Final: 0xFE88}, true
	case 0x0626:
		return arabicForms{Isolated: 0xFE89, Final: 0xFE8A, Initial: 0xFE8B, Medial: 0xFE8C}, true
	case 0x0627:
		return arabicForms{Isolated: 0xFE8D, Final: 0xFE8E}, true
	case 0x0628:
		return arabicForms{Isolated: 0xFE8F, Final: 0xFE90, Initial: 0xFE91, Medial: 0xFE92}, true
	case 0x0629:
		return arabicForms{Isolated: 0xFE93, Final: 0xFE94}, true
	case 0x062A:
		return arabicForms{Isolated: 0xFE95, Final: 0xFE96, Initial: 0xFE97, Medial: 0xFE98}, true
	case 0x062B:
		return arabicForms{Isolated: 0xFE99, Final: 0xFE9A, Initial: 0xFE9B, Medial: 0xFE9C}, true
	case 0x062C:
		return arabicForms{Isolated: 0xFE9D, Final: 0xFE9E, Initial: 0xFE9F, Medial: 0xFEA0}, true
	case 0x062D:
		return arabicForms{Isolated: 0xFEA1, Final: 0xFEA2, Initial: 0xFEA3, Medial: 0xFEA4}, true
	case 0x062E:
		return arabicForms{Isolated: 0xFEA5, Final: 0xFEA6, Initial: 0xFEA7, Medial: 0xFEA8}, true
	case 0x062F:
		return arabicForms{Isolated: 0xFEA9, Final: 0xFEAA}, true
	case 0x0630:
		return arabicForms{Isolated: 0xFEAB, Final: 0xFEAC}, true
	case 0x0631:
		return arabicForms{Isolated: 0xFEAD, Final: 0xFEAE}, true
	case 0x0632:
		return arabicForms{Isolated: 0xFEAF, Final: 0xFEB0}, true
	case 0x0633:
		return arabicForms{Isolated: 0xFEB1, Final: 0xFEB2, Initial: 0xFEB3, Medial: 0xFEB4}, true
	case 0x0634:
		return arabicForms{Isolated: 0xFEB5, Final: 0xFEB6, Initial: 0xFEB7, Medial: 0xFEB8}, true
	case 0x0635:
		return arabicForms{Isolated: 0xFEB9, Final: 0xFEBA, Initial: 0xFEBB, Medial: 0xFEBC}, true
	case 0x0636:
		return arabicForms{Isolated: 0xFEBD, Final: 0xFEBE, Initial: 0xFEBF, Medial: 0xFEC0}, true
	case 0x0637:
		return arabicForms{Isolated: 0xFEC1, Final: 0xFEC2, Initial: 0xFEC3, Medial: 0xFEC4}, true
	case 0x0638:
		return arabicForms{Isolated: 0xFEC5, Final: 0xFEC6, Initial: 0xFEC7, Medial: 0xFEC8}, true
	case 0x0639:
		return arabicForms{Isolated: 0xFEC9, Final: 0xFECA, Initial: 0xFECB, Medial: 0xFECC}, true
	case 0x063A:
		return arabicForms{Isolated: 0xFECD, Final: 0xFECE, Initial: 0xFECF, Medial: 0xFED0}, true
	case 0x0641:
		return arabicForms{Isolated: 0xFED1, Final: 0xFED2, Initial: 0xFED3, Medial: 0xFED4}, true
	case 0x0642:
		return arabicForms{Isolated: 0xFED5, Final: 0xFED6, Initial: 0xFED7, Medial: 0xFED8}, true
	case 0x0643:
		return arabicForms{Isolated: 0xFED9, Final: 0xFEDA, Initial: 0xFEDB, Medial: 0xFEDC}, true
	case 0x0644:
		return arabicForms{Isolated: 0xFEDD, Final: 0xFEDE, Initial: 0xFEDF, Medial: 0xFEE0}, true
	case 0x0645:
		return arabicForms{Isolated: 0xFEE1, Final: 0xFEE2, Initial: 0xFEE3, Medial: 0xFEE4}, true
	case 0x0646:
		return arabicForms{Isolated: 0xFEE5, Final: 0xFEE6, Initial: 0xFEE7, Medial: 0xFEE8}, true
	case 0x0647:
		return arabicForms{Isolated: 0xFEE9, Final: 0xFEEA, Initial: 0xFEEB, Medial: 0xFEEC}, true
	case 0x0648:
		return arabicForms{Isolated: 0xFEED, Final: 0xFEEE}, true
	case 0x0649:
		return arabicForms{Isolated: 0xFEEF, Final: 0xFEF0}, true
	case 0x064A:
		return arabicForms{Isolated: 0xFEF1, Final: 0xFEF2, Initial: 0xFEF3, Medial: 0xFEF4}, true
	}
	return arabicForms{}, false
}

// arabicLamAlefLigature returns the LAM+ALEF presentation form
// when prev is LAM (0x0644) and curr is one of the four ALEF
// variants. ok=false means it isn't a ligature pair. The two
// returned codepoints are (isolated, final) — the caller picks
// based on context (whether the LAM had a previous letter).
func arabicLamAlefLigature(prev, curr rune) (iso, final rune, ok bool) {
	if prev != 0x0644 {
		return 0, 0, false
	}
	switch curr {
	case 0x0622: // ALEF WITH MADDA
		return 0xFEF5, 0xFEF6, true
	case 0x0623: // ALEF WITH HAMZA ABOVE
		return 0xFEF7, 0xFEF8, true
	case 0x0625: // ALEF WITH HAMZA BELOW
		return 0xFEF9, 0xFEFA, true
	case 0x0627: // plain ALEF
		return 0xFEFB, 0xFEFC, true
	}
	return 0, 0, false
}

// containsArabic reports whether s has at least one Arabic-block
// rune that could benefit from shaping. Cheap pre-check so non-RTL
// text skips the shaping pass entirely.
func containsArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

// shapeArabic returns s with each shapeable Arabic letter replaced by
// its contextual presentation-form codepoint. The string remains in
// logical (memory) order — call reverseRunes afterward when handing
// to gopdf for visual right-to-left rendering.
//
// Algorithm: for each rune, look at the closest non-transparent
// neighbors on either side. A letter joins to the left if its
// joining type is R, D, or C and the previous non-transparent letter
// has type L, D, or C (we don't model L). Joining to the right is
// the mirror.
func shapeArabic(s string) string {
	if !containsArabic(s) {
		return s
	}
	runes := []rune(s)

	// First pass: handle LAM+ALEF ligatures. The pair (LAM, ALEF*)
	// collapses to a single ligature glyph. We replace the LAM in
	// place with the ligature and mark the ALEF for deletion via a
	// sentinel value, compacted at the end.
	const removed = -1
	for i := 0; i < len(runes)-1; i++ {
		iso, final, ok := arabicLamAlefLigature(runes[i], runes[i+1])
		if !ok {
			continue
		}
		// Pick form based on whether the LAM has a joinable
		// neighbor on its left (would have been "initial" or
		// "medial" — both render as the "final" ligature, since
		// the ligature itself is a 2-form letter).
		prevType := prevJoiningType(runes, i)
		form := iso
		if prevJoinsNext(prevType) {
			form = final
		}
		runes[i] = form
		runes[i+1] = removed
		i++ // skip the consumed ALEF
	}

	// Second pass: per-letter form selection. Transparent runes pass
	// through unchanged.
	out := make([]rune, 0, len(runes))
	for i, r := range runes {
		if r == removed {
			continue
		}
		// Skip already-substituted presentation-form runes (the LAM
		// pass might have written one).
		if r >= 0xFE70 && r <= 0xFEFF {
			out = append(out, r)
			continue
		}
		forms, ok := arabicShapeTable(r)
		if !ok {
			out = append(out, r)
			continue
		}
		curType := arabicJoiningTable(r)
		prevType := prevJoiningType(runes, i)
		nextType := nextJoiningType(runes, i)
		// canJoinLeft means "this letter accepts a join from the
		// previous letter" → R or D or C.
		canJoinLeft := curType == ajtRight || curType == ajtDual || curType == ajtCausing
		// canJoinRight: this letter offers a join to the next
		// letter → D or C (R does not).
		canJoinRight := curType == ajtDual || curType == ajtCausing
		joinsLeft := canJoinLeft && prevJoinsNext(prevType)
		joinsRight := canJoinRight && nextJoinsPrev(nextType)
		var form rune
		switch {
		case joinsLeft && joinsRight && forms.Medial != 0:
			form = forms.Medial
		case joinsLeft && forms.Final != 0:
			form = forms.Final
		case joinsRight && forms.Initial != 0:
			form = forms.Initial
		default:
			form = forms.Isolated
		}
		if form == 0 {
			form = r
		}
		out = append(out, form)
	}

	// Common short-circuit: if shaping didn't actually change the
	// string, return s to avoid the allocation.
	if string(out) == s {
		return s
	}
	return string(out)
}

// prevJoiningType walks backward from i skipping transparent and
// removed runes. Returns ajtNon when there's no joinable predecessor.
func prevJoiningType(runes []rune, i int) arabicJoiningType {
	for j := i - 1; j >= 0; j-- {
		if runes[j] == -1 {
			continue
		}
		t := arabicJoiningTable(runes[j])
		if t == ajtTransparent {
			continue
		}
		// Already-shaped presentation forms still count as joinable
		// for the previous letter only if the original was joining-
		// causing. Conservatively, treat them as ajtNon — they're
		// already shaped, so their effect on later context is moot
		// because we process runes in order and the LAM has already
		// decided its form.
		if runes[j] >= 0xFE70 && runes[j] <= 0xFEFF {
			// Map back to ajtDual for forms in dual-letter ranges so
			// chains like LAM-MEEM-DAL still get medial-initial-final.
			return presentationFormJoiningType(runes[j])
		}
		return t
	}
	return ajtNon
}

// nextJoiningType is the mirror of prevJoiningType: walk forward
// past transparents.
func nextJoiningType(runes []rune, i int) arabicJoiningType {
	for j := i + 1; j < len(runes); j++ {
		if runes[j] == -1 {
			continue
		}
		t := arabicJoiningTable(runes[j])
		if t == ajtTransparent {
			continue
		}
		if runes[j] >= 0xFE70 && runes[j] <= 0xFEFF {
			return presentationFormJoiningType(runes[j])
		}
		return t
	}
	return ajtNon
}

// prevJoinsNext is true when a left-side neighbor with the given
// joining type would extend a join to us (i.e. the neighbor has the
// "joins-right" capability).
func prevJoinsNext(t arabicJoiningType) bool {
	// L doesn't exist in Arabic; D and C join both sides.
	return t == ajtDual || t == ajtCausing
}

// nextJoinsPrev mirrors prevJoinsNext for the right side: the
// neighbor with joining type t reaches a join *back* to us when
// they have the "joins-left" capability (R, D, or C).
func nextJoinsPrev(t arabicJoiningType) bool {
	return t == ajtRight || t == ajtDual || t == ajtCausing
}

// presentationFormJoiningType returns the underlying joining type
// for a rune that's already been shaped (i.e. in U+FE70..U+FEFF).
// Used so a LAM that has been written into the array as a ligature
// still influences the joining decisions of letters that follow it.
// Approximation: ligatures end in a right-joining base, so the form
// behaves like ajtRight for the purpose of "next character joins
// previous". This produces correct results for the most common
// pattern (... LAM-ALEF-ligature WORD-START ...).
func presentationFormJoiningType(r rune) arabicJoiningType {
	// LAM-ALEF ligatures (FEF5..FEFC) end in ALEF, which is R.
	if r >= 0xFEF5 && r <= 0xFEFC {
		return ajtRight
	}
	// For everything else, fall through to ajtNon so over-eager
	// medial/initial selections don't happen.
	return ajtNon
}

// shapeArabicAtoms applies shapeArabic to every atom whose text
// contains Arabic runes. Called from runsToAtoms after atom
// construction but before any RTL rune-reversal.
func shapeArabicAtoms(atoms []atom) {
	for i := range atoms {
		if atoms[i].kind != atomWord {
			continue
		}
		if !containsArabic(atoms[i].text) {
			continue
		}
		atoms[i].text = shapeArabic(atoms[i].text)
	}
}
