package docx

import (
	"encoding/xml"
	"strings"
)

// Math support: we walk the OMML (Office Math Markup Language) subtree
// and emit a structurally-aware *string* approximation rather than a true
// math-typesetting layout. The output is meant to be readable inline:
//
//	m:f  (fraction)        → "a/b"
//	m:rad (radical)        → "√(x)" or "ⁿ√(x)" for nth-root
//	m:sSup (superscript)   → "x^(2)" with Unicode superscripts for 0-9 + - = ( )
//	m:sSub (subscript)     → "x_(2)" with Unicode subscripts
//	m:sSubSup              → "x_(i)^(2)"
//	m:nary (∑/∫/∏ etc.)    → "∑_(i=1)^(n) f(i)"
//	m:d   (delimited)      → "(a, b)" or "{a; b}" depending on declared chars
//	m:func (function-apply)→ "sin(x)"
//	m:limLow / m:limUpp    → "lim_(x→0) f(x)" / "lim^∞ f"
//	m:acc  (accent)        → "x̂" by stacking the accent char after the base
//	m:bar / m:box          → "‾x" or "⌜x⌝"
//	m:groupChr             → "⟨x⟩" using its declared bracket char
//	m:matrix / m:eqArr     → "[a b; c d]" / "{a; b}"
//	m:r/m:t (run / text)   → the visible text content
//
// This is far better than the previous flat-extract: variables stay
// grouped, exponents and limits read correctly, and matrices keep their
// row structure. It is NOT a substitute for proper glyph positioning —
// that requires a math engine which is out of scope.

// extractMathText walks an m:oMath / m:oMathPara subtree starting at start
// and returns a single readable string. The caller emits this as an italic
// run.
func extractMathText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	mr, err := decodeMathNode(dec, start)
	if err != nil {
		return "", err
	}
	return mr.render(), nil
}

// mathNode is an OMML subtree we know how to render to a string. Each
// node has a kind plus a small set of slots — argument lists for things
// like sub/sup, delimiters, n-ary operators, accents.
type mathNode struct {
	kind     string
	text     string      // raw text for "r" / "t" / accentChar / numerator-tex etc.
	children []*mathNode // generic ordered children (e.g. inside m:e, m:oMath body)
	// Named slots for structured elements. Empty when not applicable.
	num   *mathNode // m:f numerator
	den   *mathNode // m:f denominator
	base  *mathNode // m:sSup / m:sSub / m:sSubSup / m:rad / m:nary / m:limLow / m:limUpp / m:acc / m:groupChr / m:bar / m:box / m:func base
	sup   *mathNode // superscript
	sub   *mathNode // subscript
	deg   *mathNode // m:rad degree
	limLo *mathNode // m:limLow / m:nary lower limit
	limUp *mathNode // m:limUpp / m:nary upper limit
	arg   *mathNode // m:func argument
	// matrix rows; each row is a list of "e" cells.
	rows [][]*mathNode
	// Per-element formatting hints pulled from props.
	begChar  string // m:dPr begChr
	endChar  string // m:dPr endChr
	sepChar  string // m:dPr sepChar (defaults to ",")
	naryChar string // m:naryPr chr (∑, ∫, ∏ ...)
	accChar  string // m:accPr chr
}

func newMathNode(kind string) *mathNode { return &mathNode{kind: kind} }

// decodeMathNode walks one element and returns its mathNode. Recognizes
// OMML structural elements; treats unknown ones as opaque text wrappers
// (their CharData content is concatenated into the node's text).
func decodeMathNode(dec *xml.Decoder, start xml.StartElement) (*mathNode, error) {
	n := newMathNode(start.Name.Local)
	// Pull display attributes off props elements at decode time.
	for {
		tok, err := dec.Token()
		if err != nil {
			return n, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeMathNode(dec, t)
			if err != nil {
				return n, err
			}
			// Route the child into the appropriate slot based on its
			// element name (m:num / m:den / m:e / m:sup / m:sub / etc.).
			switch t.Name.Local {
			case "num":
				n.num = child
			case "den":
				n.den = child
			case "e":
				if n.kind == "mr" {
					// matrix row child cells appear as m:e directly inside m:mr.
					if len(n.rows) == 0 {
						n.rows = append(n.rows, nil)
					}
					n.rows[0] = append(n.rows[0], child)
				} else {
					n.base = child
				}
			case "sup":
				n.sup = child
			case "sub":
				n.sub = child
			case "deg":
				n.deg = child
			case "lim":
				// m:limLow and m:limUpp wrap a m:lim element holding the
				// limit expression.
				if n.kind == "limLow" || n.kind == "naryLimLow" {
					n.limLo = child
				} else if n.kind == "limUpp" || n.kind == "naryLimUpp" {
					n.limUp = child
				} else {
					n.children = append(n.children, child)
				}
			case "fName":
				n.arg = child
			case "mr":
				n.rows = append(n.rows, child.rows[0])
			case "dPr":
				n.begChar = child.begChar
				n.endChar = child.endChar
				n.sepChar = child.sepChar
			case "naryPr":
				n.naryChar = child.naryChar
				// nary props also carry sub-position / lim-loc info; we
				// ignore those (formatting only).
			case "accPr":
				n.accChar = child.accChar
			case "begChr":
				n.begChar = child.text
			case "endChr":
				n.endChar = child.text
			case "sepChr":
				n.sepChar = child.text
			case "chr":
				// chr is reused across many props elements; n.kind tells
				// us which to assign to. naryPr.chr → naryChar;
				// accPr.chr → accChar; groupChrPr.chr → base text.
				if n.kind == "naryPr" {
					n.naryChar = child.text
				} else if n.kind == "accPr" {
					n.accChar = child.text
				} else if n.kind == "groupChrPr" {
					n.accChar = child.text
				}
			default:
				n.children = append(n.children, child)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return n, nil
			}
		case xml.CharData:
			// m:t holds the literal glyph text; everything else is
			// structural. Accumulate CharData into n.text.
			n.text += string(t)
		}
		// For attribute-bearing elements (begChr / endChr / sepChr / chr),
		// we also want the "val" attribute (some writers put the literal
		// glyph in val instead of in CharData).
		if start.Name.Local == "begChr" || start.Name.Local == "endChr" || start.Name.Local == "sepChr" || start.Name.Local == "chr" {
			if n.text == "" {
				for _, a := range start.Attr {
					if a.Name.Local == "val" && a.Value != "" {
						n.text = a.Value
					}
				}
			}
		}
	}
}

// render returns the readable approximation for this node's subtree.
func (n *mathNode) render() string {
	if n == nil {
		return ""
	}
	switch n.kind {
	case "t":
		return n.text
	case "r", "e", "num", "den", "deg", "sup", "sub", "lim", "fName", "oMath", "oMathPara":
		return n.joinChildren()
	case "f":
		// Fraction: render as "num/den" with parens around multi-token
		// halves so the precedence reads sensibly.
		num, den := n.num.render(), n.den.render()
		return wrapIfComplex(num) + "/" + wrapIfComplex(den)
	case "rad":
		base := n.base.render()
		deg := n.deg.render()
		if deg != "" {
			return supScript(deg) + "√(" + base + ")"
		}
		return "√(" + base + ")"
	case "sSup":
		return n.base.render() + supScript(n.sup.render())
	case "sSub":
		return n.base.render() + subScript(n.sub.render())
	case "sSubSup":
		return n.base.render() + subScript(n.sub.render()) + supScript(n.sup.render())
	case "nary":
		op := n.naryChar
		if op == "" {
			op = "∑"
		}
		lo := n.limLo.render()
		up := n.limUp.render()
		body := n.base.render()
		s := op
		if lo != "" {
			s += subScript(lo)
		}
		if up != "" {
			s += supScript(up)
		}
		if body != "" {
			s += " " + body
		}
		return s
	case "d":
		// Delimited group: render with the begChar/endChar pair, defaulting
		// to round brackets. If multiple m:e children exist they are
		// separated by sepChar (default ",").
		open, close := n.begChar, n.endChar
		if open == "" {
			open = "("
		}
		if close == "" {
			close = ")"
		}
		sep := n.sepChar
		if sep == "" {
			sep = ", "
		}
		parts := make([]string, 0, len(n.children))
		for _, c := range n.children {
			if c.kind == "e" {
				parts = append(parts, c.render())
			}
		}
		if len(parts) == 0 {
			parts = append(parts, n.joinChildren())
		}
		return open + strings.Join(parts, sep) + close
	case "func":
		return n.arg.render() + "(" + n.base.render() + ")"
	case "limLow":
		return n.base.render() + subScript(n.limLo.render())
	case "limUpp":
		return n.base.render() + supScript(n.limUp.render())
	case "acc":
		// Accent: stack the accent char after the base (Unicode combining
		// behaviour). If the accent char is empty fall back to U+0302
		// (combining circumflex).
		ch := n.accChar
		if ch == "" {
			ch = "̂"
		}
		return n.base.render() + ch
	case "bar":
		return "‾" + n.base.render()
	case "box":
		return "⌜" + n.base.render() + "⌝"
	case "borderBox":
		return "[" + n.base.render() + "]"
	case "groupChr":
		ch := n.accChar
		if ch == "" {
			ch = "⏞"
		}
		return ch + "{" + n.base.render() + "}"
	case "m", "matrix":
		// Matrix: rows separated by "; ", cells in a row by " ".
		out := make([]string, 0, len(n.rows))
		for _, row := range n.rows {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = c.render()
			}
			out = append(out, strings.Join(cells, " "))
		}
		return "[" + strings.Join(out, "; ") + "]"
	case "eqArr":
		// Equation array: stack of equations separated by "; ".
		parts := make([]string, 0, len(n.children))
		for _, c := range n.children {
			if c.kind == "e" {
				parts = append(parts, c.render())
			}
		}
		return "{" + strings.Join(parts, "; ") + "}"
	case "phant":
		// Phantom: invisible — drop the content (matches Word's intent).
		return ""
	default:
		// Unknown structural wrapper: render its children inline.
		return n.joinChildren()
	}
}

func (n *mathNode) joinChildren() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(n.text)
	for _, c := range n.children {
		b.WriteString(c.render())
	}
	return b.String()
}

// wrapIfComplex wraps a string in parentheses if it contains any
// "operator-like" character (space, +, -, *, /, =). Single tokens like
// "x" or "23" are returned unchanged so the fraction reads naturally.
func wrapIfComplex(s string) string {
	for _, r := range s {
		switch r {
		case ' ', '+', '-', '*', '/', '=':
			return "(" + s + ")"
		}
	}
	return s
}

// supScript / subScript convert a string into Unicode superscript /
// subscript glyphs when possible, otherwise wraps with "^( )" / "_( )"
// so the structure is at least visible.
var supTable = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵',
	'6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
	'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾',
	'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ', 'f': 'ᶠ',
	'g': 'ᵍ', 'h': 'ʰ', 'i': 'ⁱ', 'j': 'ʲ', 'k': 'ᵏ', 'l': 'ˡ',
	'm': 'ᵐ', 'n': 'ⁿ', 'o': 'ᵒ', 'p': 'ᵖ', 'r': 'ʳ', 's': 'ˢ',
	't': 'ᵗ', 'u': 'ᵘ', 'v': 'ᵛ', 'w': 'ʷ', 'x': 'ˣ', 'y': 'ʸ',
	'z': 'ᶻ',
}

var subTable = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅',
	'6': '₆', '7': '₇', '8': '₈', '9': '₉',
	'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎',
	'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ', 'k': 'ₖ',
	'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ', 'p': 'ₚ', 'r': 'ᵣ',
	's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ', 'v': 'ᵥ', 'x': 'ₓ',
}

func supScript(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	allMapped := true
	for _, r := range s {
		if m, ok := supTable[r]; ok {
			out = append(out, m)
		} else {
			allMapped = false
			break
		}
	}
	if allMapped {
		return string(out)
	}
	return "^(" + s + ")"
}

func subScript(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	allMapped := true
	for _, r := range s {
		if m, ok := subTable[r]; ok {
			out = append(out, m)
		} else {
			allMapped = false
			break
		}
	}
	if allMapped {
		return string(out)
	}
	return "_(" + s + ")"
}

// mathRun returns a Run carrying the extracted math text styled in italic,
// inheriting the surrounding paragraph's run properties.
func mathRun(text string, paraRPr RunProps) Run {
	rp := paraRPr
	rp.Italic = true
	return Run{Text: text, Props: rp}
}
