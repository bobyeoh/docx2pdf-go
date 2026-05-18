package docx

import "strings"

// mapSymbolGlyph translates a w:sym (font, codepoint) pair into a
// canonical Unicode rune. The Wingdings and Symbol fonts encode their
// glyphs in the private-use area F020-F0FF; mapping into the main
// Unicode planes lets a regular text font render the glyph correctly
// without us having to bundle the source font.
//
// For unknown (font, codepoint) combinations we return the raw rune so
// docs that target a custom font still surface a deterministic
// character (typically tofu in the rendered PDF, but at least the
// content is round-tripped to text extraction).
func mapSymbolGlyph(font string, cp rune) rune {
	if cp == 0 {
		return cp
	}
	// Strip any locale suffix Word sometimes appends ("Wingdings-Regular"
	// would still want Wingdings semantics).
	font = strings.SplitN(font, "-", 2)[0]
	font = strings.ToLower(font)
	// Most symbol fonts mirror the ASCII range at U+F020-U+F07E with the
	// same glyphs they ship at U+0020-U+007E, so when the destination
	// font is unknown we can use that overlap as a fallback. We only
	// translate non-PUA targets when we have a known mapping.
	switch font {
	case "wingdings":
		if r, ok := wingdings[cp]; ok {
			return r
		}
	case "wingdings 2", "wingdings2":
		if r, ok := wingdings2[cp]; ok {
			return r
		}
	case "wingdings 3", "wingdings3":
		if r, ok := wingdings3[cp]; ok {
			return r
		}
	case "symbol":
		if r, ok := symbolTable[cp]; ok {
			return r
		}
	case "webdings":
		if r, ok := webdings[cp]; ok {
			return r
		}
	}
	// Fallback: if the codepoint is in the PUA mirror of ASCII, peel off
	// the F000 offset so a basic font still has something to render.
	if cp >= 0xF020 && cp <= 0xF07E {
		return cp - 0xF000
	}
	return cp
}

// wingdings maps the Wingdings PUA code points to canonical Unicode.
// We only carry the glyphs Word's "Insert Symbol" dialog hands out most
// often — arrows, check/cross marks, basic geometric shapes. Anything
// else falls through to the raw rune.
var wingdings = map[rune]rune{
	0xF020: ' ',
	0xF021: '✏', // pencil
	0xF022: '✂', // scissors
	0xF023: '✁',
	0xF024: '👓',
	0xF025: '🔔',
	0xF026: '📖',
	0xF027: '🕯',
	0xF028: '📞',
	0xF029: '✆',
	0xF02A: '✉',
	0xF02B: '📩',
	0xF037: '☞',
	0xF038: '☜',
	0xF039: '☝',
	0xF03A: '☟',
	0xF04A: '☺',
	0xF04C: '☹',
	0xF051: '❄',
	0xF055: '★',
	0xF058: '✘',
	0xF06C: '◆',
	0xF06E: '■',
	0xF06F: '□',
	0xF071: '◆',
	0xF076: '◆',
	0xF0A8: '✦',
	0xF0E0: '→',
	0xF0E1: '←',
	0xF0E2: '↑',
	0xF0E3: '↓',
	0xF0F0: '⇐',
	0xF0F1: '⇒',
	0xF0FA: '✓',
	0xF0FB: '✗',
	0xF0FC: '✓',
	0xF0FD: '☑',
	0xF0FE: '☐',
}

var wingdings2 = map[rune]rune{
	0xF050: '✓',
	0xF052: '✗',
	0xF053: '✘',
}

var wingdings3 = map[rune]rune{
	0xF075: '→',
	0xF076: '↗',
	0xF077: '↘',
	0xF078: '↙',
	0xF079: '↖',
	0xF07A: '↑',
	0xF07B: '↓',
	0xF07C: '←',
}

// symbolTable maps Adobe Symbol-font PUA code points to canonical
// Unicode math/Greek glyphs.
var symbolTable = map[rune]rune{
	0xF022: '∀', // for all
	0xF024: '∃', // there exists
	0xF026: '&',
	0xF027: '∍',
	0xF028: '(',
	0xF029: ')',
	0xF02A: '∗',
	0xF02B: '+',
	0xF02D: '−',
	0xF03C: '<',
	0xF03D: '=',
	0xF03E: '>',
	0xF040: '≅',
	0xF041: 'Α', // Alpha
	0xF042: 'Β',
	0xF044: 'Δ',
	0xF045: '∈', // element of
	0xF050: 'Π',
	0xF053: 'Σ',
	0xF057: 'Ω',
	0xF061: 'α', // alpha
	0xF062: 'β',
	0xF064: 'δ',
	0xF065: 'ε',
	0xF06D: 'μ',
	0xF070: 'π',
	0xF072: 'ρ',
	0xF073: 'σ',
	0xF0A4: '∞', // infinity
	0xF0B6: '∂', // partial
	0xF0B7: '·', // middle dot
	0xF0B1: '±',
	0xF0B3: '≥',
	0xF0B9: '≠',
	0xF0BB: '≈',
	0xF0BD: '≡',
	0xF0CE: '⊂',
	0xF0CF: '⊆',
	0xF0D0: '∉',
	0xF0D7: '×',
	0xF0F8: '⌡',
}

// webdings (Microsoft) — small subset covering common UI glyphs.
var webdings = map[rune]rune{
	0xF021: '🕷',
	0xF030: '☼',
	0xF048: '☐',
	0xF055: '✺',
}
