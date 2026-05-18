package render

import "unicode"

// Atom-level approximation of the Unicode Bidirectional Algorithm
// (UAX#9). Each atom is a self-contained breakable unit (one Latin
// word, one CJK character, one space, one image, etc.), so we resolve
// directionality at the atom level rather than per-character. That's
// less faithful than full UAX#9 but covers the dominant real-world
// cases:
//
//   - LTR paragraph with an embedded Arabic / Hebrew word
//   - RTL paragraph with embedded English brand names or digits
//   - All-LTR or all-RTL paragraphs (the simple case)
//
// What this does NOT cover (yet): higher embedding levels (LRE/RLE/
// LRO/RLO control characters, isolate runs), full neutral resolution
// rules (N0/N1/N2), and explicit directional formatting characters.
// Those are rare enough in Word documents that the atom-level
// approximation is a good first step.

// bidiStrong is the simplified UAX#9 directional class used to assign
// an embedding level to an atom. Full UAX#9 has ~20 classes; we only
// distinguish strong-L, strong-R/AL, and neutral.
type bidiStrong int

const (
	bidiNeutral bidiStrong = iota
	bidiStrongL            // letters that read left-to-right (Latin, CJK, Cyrillic, ...)
	bidiStrongR            // letters that read right-to-left (Arabic, Hebrew, ...)
)

// bidiStrongClass returns the simplified directional class of one
// rune. isRTL (in fonts.go) covers the canonical RTL scripts; any
// other letter is treated as strong-L. Non-letters (digits,
// punctuation, whitespace) are neutral and resolve to the paragraph
// base when an atom is otherwise empty of strong characters.
func bidiStrongClass(r rune) bidiStrong {
	if isRTL(r) {
		return bidiStrongR
	}
	if unicode.IsLetter(r) {
		return bidiStrongL
	}
	return bidiNeutral
}

// firstStrongClass returns the directional class of the first
// strong-classed rune in s, or bidiNeutral if none exists. This is
// UAX#9 P2/P3 applied at atom granularity — sufficient for cases like
// "Hello مرحبا World" where each word atom has a clear first-strong.
func firstStrongClass(s string) bidiStrong {
	for _, r := range s {
		if c := bidiStrongClass(r); c != bidiNeutral {
			return c
		}
	}
	return bidiNeutral
}

// atomBidiLevel returns the embedding level (0–2) for one atom given
// its text and paragraph base direction.
//
//   - Strong-R atom: level 1 (RTL, glyphs reversed at draw time).
//   - Strong-L atom in LTR paragraph: level 0.
//   - Strong-L atom in RTL paragraph: level 2 (LTR fragment embedded
//     inside RTL flow — characters stay logical, atom is reordered).
//   - Neutral atom: inherits paragraph base level (0 or 1).
//
// Levels beyond 2 (deep nesting via LRE/RLE/etc.) are not modeled.
func atomBidiLevel(text string, paragraphRTL bool) uint8 {
	switch firstStrongClass(text) {
	case bidiStrongR:
		return 1
	case bidiStrongL:
		if paragraphRTL {
			return 2
		}
		return 0
	}
	if paragraphRTL {
		return 1
	}
	return 0
}

// paragraphBaseLevel returns the embedding level for atoms with no
// intrinsic directionality (spaces, tabs, soft breaks). Such atoms
// follow the paragraph's base direction.
func paragraphBaseLevel(paragraphRTL bool) uint8 {
	if paragraphRTL {
		return 1
	}
	return 0
}

// reorderAtomsL2 reorders atoms in visual order per UAX#9 rule L2.
// Atoms whose level is odd produce RTL output and have their text
// rune-sequence already reversed (see flushBuf in text.go). L2 only
// handles the visual ordering of atoms on a line — not the glyph
// order inside any one atom.
//
// Algorithm (UAX#9 §3.4 L2):
//
//	maxLevel := max(level across atoms)
//	for lvl := maxLevel; lvl >= 1; lvl-- {
//	    for each maximal subsequence of atoms whose level >= lvl:
//	        reverse it
//	}
//
// Returns a fresh slice; input is not mutated.
func reorderAtomsL2(atoms []atom) []atom {
	if len(atoms) == 0 {
		return atoms
	}
	out := make([]atom, len(atoms))
	copy(out, atoms)

	var maxLevel uint8
	for _, a := range out {
		if a.bidiLevel > maxLevel {
			maxLevel = a.bidiLevel
		}
	}
	if maxLevel == 0 {
		return out // all level 0 — already visual order
	}
	for level := maxLevel; level >= 1; level-- {
		i := 0
		for i < len(out) {
			if out[i].bidiLevel < level {
				i++
				continue
			}
			j := i
			for j < len(out) && out[j].bidiLevel >= level {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				out[a], out[b] = out[b], out[a]
			}
			i = j
		}
	}
	return out
}
