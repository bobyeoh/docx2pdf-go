package render

// hyphenation_lang.go holds Knuth-Liang pattern tables for languages
// other than English. The tables are curated subsets — they don't
// reproduce TeX's full hyph-XX.tex but cover the most common
// prefixes/suffixes and consonant-cluster split points for each
// language so multi-language documents at least get reasonable break
// placement instead of word-overflow.
//
// Each table follows the same Liang encoding as englishHyphenPatterns
// (see hyphenation.go for the slot/priority semantics).

// germanHyphenPatterns covers common German prefixes (be-, ver-, ge-,
// zer-, ent-, etc.), suffixes (-keit, -heit, -ung, -lich, -isch), and
// the canonical "split before vowel after a consonant cluster" rule.
//
//nolint:lll
var germanHyphenPatterns = map[string][]int{
	// Prefixes — break AFTER the prefix.
	".be":    {0, 0, 1},
	".ge":    {0, 0, 1},
	".ver":   {0, 0, 0, 1},
	".vor":   {0, 0, 0, 1},
	".zer":   {0, 0, 0, 1},
	".ent":   {0, 0, 0, 1},
	".er":    {0, 0, 1},
	".un":    {0, 0, 1},
	".aus":   {0, 0, 0, 1},
	".ein":   {0, 0, 0, 1},
	".ab":    {0, 0, 1},
	".an":    {0, 0, 1},
	".auf":   {0, 0, 0, 1},
	".mit":   {0, 0, 0, 1},
	".über":  {0, 0, 0, 0, 1},
	".unter": {0, 0, 0, 0, 0, 1},

	// Suffixes — break BEFORE the suffix.
	"keit.":  {1, 0, 0, 0, 0, 0},
	"heit.":  {1, 0, 0, 0, 0, 0},
	"ung.":   {1, 0, 0, 0, 0},
	"ungen.": {1, 0, 0, 0, 0, 0, 0},
	"lich.":  {1, 0, 0, 0, 0, 0},
	"isch.":  {1, 0, 0, 0, 0, 0},
	"bar.":   {1, 0, 0, 0, 0},
	"sam.":   {1, 0, 0, 0, 0},
	"chen.":  {1, 0, 0, 0, 0, 0},
	"lein.":  {1, 0, 0, 0, 0, 0},
	"voll.":  {1, 0, 0, 0, 0, 0},
	"haft.":  {1, 0, 0, 0, 0, 0},
	"los.":   {1, 0, 0, 0, 0},

	// Common consonant clusters and digraphs.
	"sch": {0, 0, 0, 1},
	"ch":  {0, 0, 1},
	"ck":  {0, 0, 1},
	"st":  {0, 0, 1},
	"sp":  {0, 0, 1},
	"pf":  {0, 0, 1},
	"tz":  {0, 0, 1},
	"ng":  {0, 0, 1},
	"nk":  {0, 0, 1},
	"th":  {0, 0, 1},
	"qu":  {0, 0, 1},
	"ll":  {0, 1, 0},
	"mm":  {0, 1, 0},
	"nn":  {0, 1, 0},
	"rr":  {0, 1, 0},
	"ss":  {0, 1, 0},
	"tt":  {0, 1, 0},
	"ff":  {0, 1, 0},
	"pp":  {0, 1, 0},
}

// frenchHyphenPatterns. Heavily simplified — French hyphenation in TeX
// is famously elaborate (LiaisonÉlision-aware). We cover the most
// common consonant pairs and a few prefixes/suffixes.
//
//nolint:lll
var frenchHyphenPatterns = map[string][]int{
	".dé":    {0, 0, 0, 1},
	".re":    {0, 0, 1},
	".pré":   {0, 0, 0, 1},
	".pro":   {0, 0, 0, 1},
	".sur":   {0, 0, 0, 1},
	".inter": {0, 0, 0, 0, 0, 1},
	".trans": {0, 0, 0, 0, 0, 1},

	"ment.": {1, 0, 0, 0, 0, 0},
	"tion.": {1, 0, 0, 0, 0, 0},
	"sion.": {1, 0, 0, 0, 0, 0},
	"able.": {1, 0, 0, 0, 0, 0},
	"ible.": {1, 0, 0, 0, 0, 0},
	"ique.": {1, 0, 0, 0, 0, 0},
	"isme.": {1, 0, 0, 0, 0, 0},
	"iste.": {1, 0, 0, 0, 0, 0},
	"ais.":  {1, 0, 0, 0, 0},
	"eur.":  {1, 0, 0, 0, 0},
	"eux.":  {1, 0, 0, 0, 0},

	"th": {0, 0, 1},
	"ch": {0, 0, 1},
	"ph": {0, 0, 1},
	"gn": {0, 0, 1},
	"qu": {0, 0, 1},
	"ll": {0, 1, 0},
	"rr": {0, 1, 0},
	"ss": {0, 1, 0},
	"tt": {0, 1, 0},
	"mm": {0, 1, 0},
	"nn": {0, 1, 0},
	"pp": {0, 1, 0},
}

// spanishHyphenPatterns. Spanish syllabification is mostly mechanical
// (one vowel per syllable, consonants migrate forward), so a small
// pattern set already covers the bulk of cases.
//
//nolint:lll
var spanishHyphenPatterns = map[string][]int{
	".des":   {0, 0, 0, 1},
	".pre":   {0, 0, 0, 1},
	".sub":   {0, 0, 0, 1},
	".super": {0, 0, 0, 0, 0, 1},
	".trans": {0, 0, 0, 0, 0, 1},

	"ción.":   {1, 0, 0, 0, 0, 0},
	"mente.":  {1, 0, 0, 0, 0, 0, 0},
	"dad.":    {1, 0, 0, 0, 0},
	"idad.":   {1, 0, 0, 0, 0, 0},
	"miento.": {1, 0, 0, 0, 0, 0, 0, 0},

	"ch": {0, 0, 1},
	"ll": {0, 0, 1},
	"rr": {0, 0, 1},
	"qu": {0, 0, 1},
	"gu": {0, 0, 1},
}

// italianHyphenPatterns. Italian, like Spanish, has a transparent
// vowel-consonant rhythm; this small table captures the most useful
// double-consonant breaks plus a few suffixes.
//
//nolint:lll
var italianHyphenPatterns = map[string][]int{
	".pre":   {0, 0, 0, 1},
	".dis":   {0, 0, 0, 1},
	".sotto": {0, 0, 0, 0, 0, 1},
	".sopra": {0, 0, 0, 0, 0, 1},

	"mente.": {1, 0, 0, 0, 0, 0, 0},
	"zione.": {1, 0, 0, 0, 0, 0, 0},
	"zioni.": {1, 0, 0, 0, 0, 0, 0},
	"bile.":  {1, 0, 0, 0, 0, 0},
	"mento.": {1, 0, 0, 0, 0, 0, 0},

	"gh": {0, 0, 1},
	"gl": {0, 0, 1},
	"gn": {0, 0, 1},
	"sc": {0, 0, 1},
	"ch": {0, 0, 1},
	"qu": {0, 0, 1},
	"ll": {0, 1, 0},
	"rr": {0, 1, 0},
	"ss": {0, 1, 0},
	"tt": {0, 1, 0},
	"mm": {0, 1, 0},
	"nn": {0, 1, 0},
	"pp": {0, 1, 0},
}
