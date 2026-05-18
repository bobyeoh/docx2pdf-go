package render

import (
	"strconv"
	"strings"
	"unicode"
)

// applyGeneralFormatSwitch implements Word's `\*` general-format switch.
//
// `\* MERGEFORMAT` and `\* CHARFORMAT` are stylistic hints with no effect on
// the visible text — they pass through. The other tokens transform the
// value: case folds (Upper / Lower / FirstCap / Caps) and numeric formats
// (arabic / roman / Roman / alphabetic / ALPHABETIC / Hex / Ordinal /
// CardText / OrdText / DollarText).
//
// Multiple `\*` switches stack in left-to-right order; the last one wins per
// category (case vs. number) — mirroring Word's behavior.
func applyGeneralFormatSwitch(value, instrFull string) string {
	if value == "" || instrFull == "" {
		return value
	}
	if !strings.Contains(instrFull, `\*`) {
		return value
	}
	tokens := parseGeneralSwitchTokens(instrFull)
	for _, tok := range tokens {
		value = applyOneGeneralFormat(value, tok)
	}
	return value
}

// parseGeneralSwitchTokens collects every `\* TOKEN` from instrFull in
// document order. Tokens are returned without quotes; the parser is
// forgiving about whitespace and stops at the next `\` (the next switch).
func parseGeneralSwitchTokens(instrFull string) []string {
	var out []string
	rest := instrFull
	for {
		i := strings.Index(rest, `\*`)
		if i < 0 {
			return out
		}
		rest = rest[i+2:]
		rest = strings.TrimLeft(rest, " \t")
		// Token is the next whitespace- or backslash-delimited word.
		end := len(rest)
		for j, r := range rest {
			if r == ' ' || r == '\t' || r == '\\' {
				end = j
				break
			}
		}
		tok := strings.Trim(rest[:end], `"`)
		if tok != "" {
			out = append(out, tok)
		}
		rest = rest[end:]
	}
}

func applyOneGeneralFormat(value, tok string) string {
	switch tok {
	case "MERGEFORMAT", "CHARFORMAT":
		return value
	case "Upper":
		return strings.ToUpper(value)
	case "Lower":
		return strings.ToLower(value)
	case "FirstCap":
		return firstCap(value)
	case "Caps":
		return titleCase(value)
	case "Arabic", "arabic", "ARABIC":
		// "arabic" forces decimal display when the source was alphabetic
		// (e.g. PAGE in a roman section). If value already parses as a
		// number we return it as-is; otherwise leave it.
		if n, ok := parseIntFlexible(value); ok {
			return strconv.Itoa(n)
		}
		return value
	case "ArabicDash":
		// "- 1 -" page-number bracketing common in technical reports.
		if n, ok := parseIntFlexible(value); ok {
			return "- " + strconv.Itoa(n) + " -"
		}
		return value
	case "ZOdiac", "Zodiac":
		// Zodiac calendars: we don't ship the tables but mustn't break the
		// pipeline — pass the value through.
		return value
	case "SBCHAR", "sbchar":
		// Half-width / single-byte digit form — already what we emit.
		return value
	case "DBCHAR", "dbchar":
		// Full-width digit form (e.g. ASCII '1' → '１'). Map ASCII 0..9
		// onto the Unicode full-width block; non-digit chars pass through.
		var b strings.Builder
		for _, r := range value {
			if r >= '0' && r <= '9' {
				b.WriteRune(0xFF10 + (r - '0'))
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	case "roman":
		if n, ok := parseIntFlexible(value); ok && n > 0 {
			return roman(n, false)
		}
	case "Roman":
		if n, ok := parseIntFlexible(value); ok && n > 0 {
			return roman(n, true)
		}
	case "alphabetic":
		if n, ok := parseIntFlexible(value); ok && n > 0 {
			return alphaLabel(n, false)
		}
	case "ALPHABETIC":
		if n, ok := parseIntFlexible(value); ok && n > 0 {
			return alphaLabel(n, true)
		}
	case "Hex":
		if n, ok := parseIntFlexible(value); ok {
			return strings.ToUpper(strconv.FormatInt(int64(n), 16))
		}
	case "Ordinal":
		if n, ok := parseIntFlexible(value); ok {
			return ordinalSuffix(n)
		}
	case "CardText":
		if n, ok := parseIntFlexible(value); ok {
			return cardText(n)
		}
	case "OrdText":
		if n, ok := parseIntFlexible(value); ok {
			return ordText(n)
		}
	case "DollarText":
		if n, ok := parseIntFlexible(value); ok {
			return dollarText(n)
		}
	}
	return value
}

// firstCap uppercases the first rune of every sentence (matched on
// sentence-ending punctuation), lowercasing the rest. Word's "FirstCap"
// actually only capitalizes the FIRST WORD of the value; we follow that.
func firstCap(s string) string {
	for i, r := range s {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r)) + strings.ToLower(s[i+len(string(r)):])
		}
	}
	return s
}

// titleCase capitalizes the first letter of every word (split on whitespace).
func titleCase(s string) string {
	var b strings.Builder
	atStart := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			atStart = true
			b.WriteRune(r)
			continue
		}
		if atStart && unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
		atStart = false
	}
	return b.String()
}

func parseIntFlexible(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Strip a trailing percent or unit suffix for the common "12px" /
	// "75%" case.
	for i, r := range s {
		if !unicode.IsDigit(r) && !(i == 0 && (r == '-' || r == '+')) {
			s = s[:i]
			break
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ordinalSuffix produces "1st", "2nd", "3rd", "4th", ... — Word's Ordinal.
func ordinalSuffix(n int) string {
	suffix := "th"
	r := n % 100
	if r < 11 || r > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// cardText writes a non-negative integer in English words. We cover the
// 0..999,999 range that real documents hit (page numbers, paragraph counts).
// Larger values fall back to the decimal string.
func cardText(n int) string {
	if n == 0 {
		return "Zero"
	}
	if n < 0 {
		return "Negative " + cardText(-n)
	}
	if n >= 1_000_000 {
		return strconv.Itoa(n)
	}
	var parts []string
	if k := n / 1000; k > 0 {
		parts = append(parts, cardTextUnder1000(k)+" Thousand")
		n %= 1000
	}
	if n > 0 {
		parts = append(parts, cardTextUnder1000(n))
	}
	return strings.Join(parts, " ")
}

func cardTextUnder1000(n int) string {
	ones := []string{"", "One", "Two", "Three", "Four", "Five",
		"Six", "Seven", "Eight", "Nine"}
	teens := []string{"Ten", "Eleven", "Twelve", "Thirteen", "Fourteen",
		"Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	tens := []string{"", "", "Twenty", "Thirty", "Forty", "Fifty",
		"Sixty", "Seventy", "Eighty", "Ninety"}
	var b strings.Builder
	if h := n / 100; h > 0 {
		b.WriteString(ones[h])
		b.WriteString(" Hundred")
		n %= 100
		if n > 0 {
			b.WriteByte(' ')
		}
	}
	switch {
	case n >= 20:
		b.WriteString(tens[n/10])
		if r := n % 10; r > 0 {
			b.WriteByte('-')
			b.WriteString(ones[r])
		}
	case n >= 10:
		b.WriteString(teens[n-10])
	case n > 0:
		b.WriteString(ones[n])
	}
	return b.String()
}

// ordText spells the ordinal: 1 → "First", 2 → "Second", ..., 21 → "Twenty-First".
func ordText(n int) string {
	if n <= 0 {
		return strconv.Itoa(n)
	}
	specials := map[int]string{
		1: "First", 2: "Second", 3: "Third", 4: "Fourth", 5: "Fifth",
		6: "Sixth", 7: "Seventh", 8: "Eighth", 9: "Ninth", 10: "Tenth",
		11: "Eleventh", 12: "Twelfth", 13: "Thirteenth", 14: "Fourteenth",
		15: "Fifteenth", 16: "Sixteenth", 17: "Seventeenth",
		18: "Eighteenth", 19: "Nineteenth", 20: "Twentieth",
		30: "Thirtieth", 40: "Fortieth", 50: "Fiftieth", 60: "Sixtieth",
		70: "Seventieth", 80: "Eightieth", 90: "Ninetieth",
		100: "One Hundredth", 1000: "One Thousandth",
	}
	if v, ok := specials[n]; ok {
		return v
	}
	if n < 100 && n%10 != 0 {
		tens := (n / 10) * 10
		ones := n % 10
		return cardText(tens) + "-" + specials[ones]
	}
	// Fallback: "<cardinal>th".
	return cardText(n) + "th"
}

// dollarText writes a currency-style spell-out: "Twelve and 34/100".
// We treat n as whole units (no cents); callers pass integer dollar amounts.
func dollarText(n int) string {
	return cardText(n) + " and 00/100"
}

// symbolFontSwitch extracts a SYMBOL field's `\f "Font"` switch. Returns ""
// when not present.
func symbolFontSwitch(instrFull string) string {
	i := strings.Index(instrFull, `\f`)
	if i < 0 {
		return ""
	}
	rest := instrFull[i+2:]
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return rest[1:]
		}
		return rest[1 : 1+end]
	}
	// Unquoted form: take to next whitespace.
	for j, r := range rest {
		if r == ' ' || r == '\t' || r == '\\' {
			return rest[:j]
		}
	}
	return rest
}

// symbolFontSizeSwitch extracts a SYMBOL field's `\s sizeInHalfPoints`
// switch. Returns 0 if not present.
func symbolFontSizeSwitch(instrFull string) float64 {
	parts := strings.Fields(instrFull)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == `\s` {
			if n, err := strconv.ParseFloat(parts[i+1], 64); err == nil {
				return n / 2.0
			}
		}
	}
	return 0
}

// hexSymbolSwitch reports whether SYMBOL declared `\h` (hex code point).
func hexSymbolSwitch(instrFull string) bool {
	parts := strings.Fields(instrFull)
	for _, p := range parts {
		if p == `\h` {
			return true
		}
	}
	return false
}
