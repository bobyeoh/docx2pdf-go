package render

import (
	"strings"
	"unicode"
)

// hyphenation.go implements a small Knuth-Liang hyphenator for English.
//
// Liang's pattern encoding: for a pattern of N letters, the digit
// vector has length N+1. vec[i] is the break priority at the slot
// BETWEEN letters[i-1] and letters[i] (vec[0] = before the first
// letter, vec[N] = after the last). An odd priority is a legal break;
// even/zero is not. Patterns are slid over a sentinel-padded word
// (".word.") and per-slot priorities are taken as the maximum across
// all matches.
//
// The hyphenator never breaks the first 2 / last 2 letters and accepts
// only words ≥ 6 letters to avoid over-segmenting.

// hyphenateEnglish returns a slice of byte offsets within s where a
// soft-break (hyphen) is legal. The offset k means "the hyphen sits
// between s[:k] and s[k:]". Returns nil when no breaks are
// recommended. s should be a single word (no surrounding whitespace).
func hyphenateEnglish(s string) []int {
	return hyphenateWithPatterns(s, englishHyphenPatterns)
}

// hyphenateForLang dispatches to a language-specific pattern table by
// BCP-47 language code. Falls back to English when the language isn't
// configured; returns nil when even English produces no breaks. The
// language matcher only looks at the primary subtag ("de", "de-DE" →
// both map to German).
func hyphenateForLang(s, lang string) []int {
	lang = strings.ToLower(lang)
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	switch lang {
	case "de", "deu", "ger":
		return hyphenateWithPatterns(s, germanHyphenPatterns)
	case "fr", "fra", "fre":
		return hyphenateWithPatterns(s, frenchHyphenPatterns)
	case "es", "spa":
		return hyphenateWithPatterns(s, spanishHyphenPatterns)
	case "it", "ita":
		return hyphenateWithPatterns(s, italianHyphenPatterns)
	default:
		return hyphenateWithPatterns(s, englishHyphenPatterns)
	}
}

// hyphenateWithPatterns is the shared Knuth-Liang engine: given a
// Liang-style pattern table, slide it over s and emit the legal break
// offsets. The word is sentinel-padded with "." on each end before the
// scan so prefix/suffix patterns can match the beginning / end.
func hyphenateWithPatterns(s string, table map[string][]int) []int {
	if len(s) < 6 {
		return nil
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return nil
		}
	}
	lc := strings.ToLower(s)
	w := "." + lc + "."
	prio := make([]int, len(w)+1)
	for pat, vec := range table {
		idx := 0
		for {
			pos := strings.Index(w[idx:], pat)
			if pos < 0 {
				break
			}
			abs := idx + pos
			for j, v := range vec {
				p := abs + j
				if p >= len(prio) {
					continue
				}
				if v > prio[p] {
					prio[p] = v
				}
			}
			idx = abs + 1
		}
	}
	var breaks []int
	for k := 2; k < len(lc)-2; k++ {
		if prio[k+1]%2 == 1 {
			breaks = append(breaks, k)
		}
	}
	return breaks
}

// englishHyphenPatterns is a curated subset of TeX's hyphen.tex.
//
// Encoding: vec[i] = priority at slot i in the pattern. The slot is
// BEFORE letters[i] for i < len(letters), or "after last letter" for
// i = len(letters). Prefix patterns end with "." sentinel — break
// after the prefix → vec[len-1] (last slot before ".") = 1. Suffix
// patterns start with "." — break before the suffix → vec[1] = 1
// (slot just after the leading ".").
//
//nolint:lll
var englishHyphenPatterns = map[string][]int{
	// Prefixes: ".xxx" wants a break AFTER the prefix. The break sits
	// between the prefix's last letter and what follows it in the word.
	// In Liang form that's the slot BEFORE the next letter in the
	// matched window — for ".xxx" of length 4, that's vec[4].
	".anti":  {0, 0, 0, 0, 1},
	".auto":  {0, 0, 0, 0, 1},
	".bio":   {0, 0, 0, 1},
	".co":    {0, 0, 1},
	".de":    {0, 0, 1},
	".dis":   {0, 0, 0, 1},
	".en":    {0, 0, 1},
	".ex":    {0, 0, 1},
	".extra": {0, 0, 0, 0, 0, 1},
	".im":    {0, 0, 1},
	".in":    {0, 0, 1},
	".inter": {0, 0, 0, 0, 0, 1},
	".micro": {0, 0, 0, 0, 0, 1},
	".mid":   {0, 0, 0, 1},
	".mis":   {0, 0, 0, 1},
	".multi": {0, 0, 0, 0, 0, 1},
	".non":   {0, 0, 0, 1},
	".over":  {0, 0, 0, 0, 1},
	".para":  {0, 0, 0, 0, 1},
	".pre":   {0, 0, 0, 1},
	".pro":   {0, 0, 0, 1},
	".re":    {0, 0, 0, 1},
	".semi":  {0, 0, 0, 0, 1},
	".sub":   {0, 0, 0, 1},
	".super": {0, 0, 0, 0, 0, 1},
	".trans": {0, 0, 0, 0, 0, 1},
	".ultra": {0, 0, 0, 0, 0, 1},
	".un":    {0, 0, 0, 1},

	// Suffixes: "xxx." wants a break BEFORE the suffix. The break sits
	// between the previous letter and the suffix's first letter — that
	// is, the slot BEFORE letters[0] in the matched window: vec[0].
	"able.":  {1, 0, 0, 0, 0, 0},
	"ably.":  {1, 0, 0, 0, 0, 0},
	"age.":   {1, 0, 0, 0, 0},
	"ance.":  {1, 0, 0, 0, 0, 0},
	"ant.":   {1, 0, 0, 0, 0},
	"ary.":   {1, 0, 0, 0, 0},
	"ate.":   {1, 0, 0, 0, 0},
	"ation.": {1, 0, 0, 0, 0, 0, 0},
	"ed.":    {1, 0, 0, 0},
	"ence.":  {1, 0, 0, 0, 0, 0},
	"ent.":   {1, 0, 0, 0, 0},
	"er.":    {1, 0, 0, 0},
	"es.":    {1, 0, 0, 0},
	"est.":   {1, 0, 0, 0, 0},
	"ful.":   {1, 0, 0, 0, 0},
	"ible.":  {1, 0, 0, 0, 0, 0},
	"ic.":    {1, 0, 0, 0},
	"ical.":  {1, 0, 0, 0, 0, 0},
	"ing.":   {1, 0, 0, 0, 0},
	"ion.":   {1, 0, 0, 0, 0},
	"ish.":   {1, 0, 0, 0, 0},
	"ism.":   {1, 0, 0, 0, 0},
	"ist.":   {1, 0, 0, 0, 0},
	"ity.":   {1, 0, 0, 0, 0},
	"ive.":   {1, 0, 0, 0, 0},
	"ize.":   {1, 0, 0, 0, 0},
	"less.":  {1, 0, 0, 0, 0, 0},
	"ly.":    {1, 0, 0, 0},
	"ment.":  {1, 0, 0, 0, 0, 0},
	"ness.":  {1, 0, 0, 0, 0, 0},
	"ous.":   {1, 0, 0, 0, 0},
	"sion.":  {1, 0, 0, 0, 0, 0},
	"tion.":  {1, 0, 0, 0, 0, 0},
	"ture.":  {1, 0, 0, 0, 0, 0},

	// Internal patterns — common consonant-vowel split priorities.
	// vec[i]=1 means "break between letters[i-1] and letters[i]".
	"vv":   {0, 1, 0},
	"ll":   {0, 1, 0},
	"rr":   {0, 1, 0},
	"ss":   {0, 1, 0},
	"tt":   {0, 1, 0},
	"ck":   {0, 0, 1},
	"sch":  {0, 0, 1, 0},
	"st":   {0, 0, 1},
	"nt":   {0, 0, 1},
	"mp":   {0, 0, 1},
	"nd":   {0, 0, 1},
	"ph":   {0, 0, 1},
	"th":   {0, 1, 0},
	"oth":  {0, 1, 0, 0},
	"orth": {0, 1, 0, 0, 0},
	"ster": {0, 0, 1, 0, 0},
}
