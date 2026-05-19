package docx

import (
	"encoding/xml"
	"strconv"
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

// ExtractMathTree walks an m:oMath / m:oMathPara subtree and returns the
// structural MathNode tree plus the flat string approximation (suitable
// as a textual fallback). The renderer prefers the tree when it can paint
// 2D math; the string keeps text-search of the PDF working.
func ExtractMathTree(dec *xml.Decoder, start xml.StartElement) (*MathNode, string, error) {
	mr, err := decodeMathNode(dec, start)
	if err != nil {
		return nil, "", err
	}
	if mr == nil {
		return nil, "", nil
	}
	return mr, mr.render(), nil
}

// mathNode is an OMML subtree we know how to render to a string. Each
// node has a kind plus a small set of slots — argument lists for things
// like sub/sup, delimiters, n-ary operators, accents.
type mathNode = MathNode

// MathNode is an OMML subtree the renderer can either flatten to a string
// (via render()) or lay out structurally on the PDF canvas (the render
// package walks the tree directly). Each node has a kind plus a small set
// of slots for sub/sup, delimiters, n-ary operators, accents, etc. All
// fields are exported so the render package can read them without going
// through accessor methods.
type MathNode struct {
	Kind     string
	Text     string      // raw text for "r" / "t" / accentChar / numerator-tex etc.
	Children []*MathNode // generic ordered children (e.g. inside m:e, m:oMath body)
	// Named slots for structured elements. Empty when not applicable.
	Num   *MathNode // m:f numerator
	Den   *MathNode // m:f denominator
	Base  *MathNode // m:sSup / m:sSub / m:sSubSup / m:rad / m:nary / m:limLow / m:limUpp / m:acc / m:groupChr / m:bar / m:box / m:func base
	Sup   *MathNode // superscript
	Sub   *MathNode // subscript
	Deg   *MathNode // m:rad degree
	LimLo *MathNode // m:limLow / m:nary lower limit
	LimUp *MathNode // m:limUpp / m:nary upper limit
	Arg   *MathNode // m:func argument
	// matrix rows; each row is a list of "e" cells.
	Rows [][]*MathNode
	// Per-element formatting hints pulled from props.
	BegChar  string // m:dPr begChr
	EndChar  string // m:dPr endChr
	SepChar  string // m:dPr sepChar (defaults to ",")
	NaryChar string // m:naryPr chr (∑, ∫, ∏ ...)
	AccChar  string // m:accPr chr
	// FracType from m:fPr/m:type val: "" (bar — default), "skw" (skewed),
	// "lin" (in-line a/b), "noBar" (stack with no horizontal line).
	FracType string
	// NaryLimLoc from m:naryPr/m:limLoc val: "" (default per script style),
	// "subSup" (sub/sup positioning), "undOvr" (under/over positioning).
	NaryLimLoc string
	// NarySupHide / NarySubHide from m:naryPr — when true the sup/sub
	// limit of the n-ary operator is suppressed (no script rendered).
	NarySupHide, NarySubHide bool
	// DGrow from m:dPr/m:grow val: when "1"/"true", the delimiters should
	// scale vertically to match the height of their content.
	DGrow bool
	// AccUnder from m:groupChrPr/m:pos val: "over" (default) or "under" —
	// places the group character above or below the base. Same field is
	// reused by m:barPr.
	AccUnder bool
	// Math run properties pulled from m:rPr (only meaningful on "r" / "t"
	// nodes, propagated to descendant "t" during decode).
	//   Nor      → true when m:nor is present (force upright/normal style,
	//              overriding the variable-italic default)
	//   StyleB   → m:sty val=b  (bold)
	//   StyleI   → m:sty val=i  (italic; default for math variables)
	//   StyleBI  → m:sty val=bi (bold-italic)
	//   StyleP   → m:sty val=p  (plain — same effect as Nor)
	//   Script   → m:scr val=roman | script | fraktur | sansSerif | monospace | doubleStruck
	Nor     bool
	StyleB  bool
	StyleI  bool
	StyleBI bool
	StyleP  bool
	Script  string
	// Align is the display-block alignment hint from m:oMathParaPr/m:jc on
	// an oMathPara wrapper: "" (Word default — center), "left", "center",
	// "right", "centerGroup". Only meaningful on the root node when its
	// Kind is "oMathPara".
	Align string
	// MatrixColJc holds per-column alignments parsed from m:mPr/m:mcs.
	// Each entry is "l", "c", or "r"; an entry expands to cover m:count
	// columns via repetition during decode. Empty → all columns center.
	MatrixColJc []string
	// Strike flags from m:borderBoxPr (a m:box variant). A single node
	// may carry one or more strikes overlaid on its base.
	StrikeH    bool // horizontal cancel line
	StrikeV    bool // vertical cancel line
	StrikeBLTR bool // bottom-left to top-right diagonal
	StrikeTLBR bool // top-left to bottom-right diagonal
	// Per-edge hide flags from m:borderBoxPr — when set, the matching
	// border edge of the surrounding box is suppressed.
	HideTop, HideBot, HideLeft, HideRight bool
	// DShape carries m:dPr/m:shp val (e.g. "centered" / "match"). When
	// "match" the brackets are drawn to match the content height; when
	// empty Word uses its default vertical centering.
	DShape string
	// DegHide is m:radPr/m:degHide — when true the radical's degree is
	// suppressed even if a non-empty m:deg subtree was provided.
	DegHide bool
	// Phantom variants from m:phantPr. Show=false hides the rendered glyph
	// but reserves layout space (default phantom semantics). ZeroWid /
	// ZeroAsc / ZeroDesc collapse the phantom's width / ascent / descent
	// to zero. Transp draws the phantom in the background color.
	PhShow, PhZeroWid, PhZeroAsc, PhZeroDesc, PhTransp bool
	// MatRowSpRule and MatColSpRule are m:mPr/m:rSpRule and m:cSpRule:
	// 1=single, 2=1.5 lines, 3=double, 4=at least, 5=exactly, 6=multiple.
	// Combined with the (unimplemented) m:rSp / m:cSp gap to position rows.
	MatRowSpRule, MatColSpRule int
	// MatPlcHide is m:mPr/m:plcHide — when true, empty cells skip their
	// placeholder rendering.
	MatPlcHide bool
	// EqMaxDist / EqObjDist / EqRowSpRule from m:eqArrPr. EqMaxDist=true
	// puts the rows of an equation array at the maximum needed distance;
	// EqObjDist=true centers each row's object on the row's vertical
	// midline.
	EqMaxDist, EqObjDist bool
	EqRowSpRule          int
	// Operator-emulator and break/align hints from m:boxPr.
	OpEmu      bool
	BoxBrk     int    // m:brk val (1-based break point)
	BoxAln     string // m:aln val (alignment hint within box)
	BoxNoBreak bool   // m:noBreak — disallow line break inside this box
	BoxDiff    bool   // m:diff — operator differentiates the base
	// ArgSz is m:argPr/m:argSz val — an integer in [-2, 2] that scales
	// the argument's display size relative to its parent.
	ArgSz int
}

func newMathNode(kind string) *mathNode { return &mathNode{Kind: kind} }

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
				n.Num = child
			case "den":
				n.Den = child
			case "e":
				if n.Kind == "mr" {
					// matrix row child cells appear as m:e directly inside m:mr.
					if len(n.Rows) == 0 {
						n.Rows = append(n.Rows, nil)
					}
					n.Rows[0] = append(n.Rows[0], child)
				} else {
					n.Base = child
				}
			case "sup":
				n.Sup = child
			case "sub":
				n.Sub = child
			case "deg":
				n.Deg = child
			case "lim":
				// m:limLow and m:limUpp wrap a m:lim element holding the
				// limit expression.
				if n.Kind == "limLow" || n.Kind == "naryLimLow" {
					n.LimLo = child
				} else if n.Kind == "limUpp" || n.Kind == "naryLimUpp" {
					n.LimUp = child
				} else {
					n.Children = append(n.Children, child)
				}
			case "fName":
				n.Arg = child
			case "mr":
				n.Rows = append(n.Rows, child.Rows[0])
			case "dPr":
				n.BegChar = child.BegChar
				n.EndChar = child.EndChar
				n.SepChar = child.SepChar
				if child.DGrow {
					n.DGrow = true
				}
				if child.DShape != "" {
					n.DShape = child.DShape
				}
			case "fPr":
				// Fraction-properties wrapper. m:type val carries the
				// fraction style. Decoder threads the child's FracType up.
				if child.FracType != "" {
					n.FracType = child.FracType
				}
			case "naryPr":
				n.NaryChar = child.NaryChar
				if child.NaryLimLoc != "" {
					n.NaryLimLoc = child.NaryLimLoc
				}
				if child.NarySupHide {
					n.NarySupHide = true
				}
				if child.NarySubHide {
					n.NarySubHide = true
				}
			case "accPr":
				n.AccChar = child.AccChar
			case "groupChrPr", "barPr":
				if child.AccChar != "" {
					n.AccChar = child.AccChar
				}
				if child.AccUnder {
					n.AccUnder = true
				}
			case "type":
				// m:type lives inside m:fPr (val=bar/skw/lin/noBar).
				if n.Kind == "fPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.FracType = a.Value
						}
					}
				}
			case "limLoc":
				// m:limLoc lives inside m:naryPr (val=subSup or undOvr).
				if n.Kind == "naryPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.NaryLimLoc = a.Value
						}
					}
				}
			case "supHide":
				if n.Kind == "naryPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" && (a.Value == "1" || a.Value == "true" || a.Value == "on") {
							n.NarySupHide = true
						}
					}
					if len(t.Attr) == 0 {
						n.NarySupHide = true
					}
				}
			case "subHide":
				if n.Kind == "naryPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" && (a.Value == "1" || a.Value == "true" || a.Value == "on") {
							n.NarySubHide = true
						}
					}
					if len(t.Attr) == 0 {
						n.NarySubHide = true
					}
				}
			case "grow":
				if n.Kind == "dPr" {
					on := true
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							switch a.Value {
							case "0", "false", "off":
								on = false
							}
						}
					}
					n.DGrow = on
				}
			case "pos":
				// m:pos lives in m:groupChrPr / m:barPr. val=top/bot.
				if n.Kind == "groupChrPr" || n.Kind == "barPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" && a.Value == "bot" {
							n.AccUnder = true
						}
					}
				}
			case "jc":
				// m:jc lives inside m:oMathParaPr (display alignment). The
				// child decoder doesn't recognize the element, so capture
				// the val attribute on the parent props node and surface
				// via oMathPara → child propagation below.
				if n.Kind == "oMathParaPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.Align = a.Value
						}
					}
				}
			case "oMathParaPr":
				if child.Align != "" {
					n.Align = child.Align
				}
			case "begChr":
				n.BegChar = child.Text
			case "endChr":
				n.EndChar = child.Text
			case "sepChr":
				n.SepChar = child.Text
			case "chr":
				// chr is reused across many props elements; n.Kind tells
				// us which to assign to. naryPr.chr → naryChar;
				// accPr.chr → accChar; groupChrPr.chr → base text.
				if n.Kind == "naryPr" {
					n.NaryChar = child.Text
				} else if n.Kind == "accPr" {
					n.AccChar = child.Text
				} else if n.Kind == "groupChrPr" {
					n.AccChar = child.Text
				}
			case "rPr":
				// Math run properties: copy the styling flags onto the
				// parent (typically an m:r) so layout can pick them up.
				n.Nor = child.Nor
				n.StyleB = child.StyleB
				n.StyleI = child.StyleI
				n.StyleBI = child.StyleBI
				n.StyleP = child.StyleP
				if child.Script != "" {
					n.Script = child.Script
				}
			case "nor":
				// Inside m:rPr: force upright (override default italic).
				if n.Kind == "rPr" {
					n.Nor = true
				}
			case "sty":
				// Inside m:rPr: bold / italic style override.
				if n.Kind == "rPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							switch a.Value {
							case "b":
								n.StyleB = true
							case "i":
								n.StyleI = true
							case "bi":
								n.StyleBI = true
							case "p":
								n.StyleP = true
							}
						}
					}
				}
			case "scr":
				// Inside m:rPr: script-style override (roman, script,
				// fraktur, sansSerif, monospace, doubleStruck).
				if n.Kind == "rPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.Script = a.Value
						}
					}
				}
			case "mPr":
				// Matrix properties — propagate column-alignment list to
				// the enclosing m:m node.
				if len(child.MatrixColJc) > 0 {
					n.MatrixColJc = append(n.MatrixColJc, child.MatrixColJc...)
				}
			case "mcs":
				// m:mcs is the list of m:mc column-property definitions
				// inside m:mPr. The decoded child carries the assembled
				// MatrixColJc slice.
				if len(child.MatrixColJc) > 0 {
					n.MatrixColJc = append(n.MatrixColJc, child.MatrixColJc...)
				}
			case "mc":
				// One column-property entry — assembled in child.MatrixColJc
				// (mcPr/mcJc + count repetitions).
				if len(child.MatrixColJc) > 0 {
					n.MatrixColJc = append(n.MatrixColJc, child.MatrixColJc...)
				}
			case "mcPr":
				// Inside m:mc: holds m:mcJc + m:count.
				if len(child.MatrixColJc) > 0 {
					n.MatrixColJc = append(n.MatrixColJc, child.MatrixColJc...)
				}
			case "mcJc":
				// m:mcJc val="l|c|r" — recorded on m:mcPr; combined with
				// m:count it expands during this same decode step. We stash
				// the single-column value here; m:count widens later.
				if n.Kind == "mcPr" {
					jc := "c"
					for _, a := range t.Attr {
						if a.Name.Local == "val" && a.Value != "" {
							jc = a.Value
						}
					}
					n.MatrixColJc = []string{jc}
				}
			case "count":
				// Inside m:mcPr: repeat the captured mcJc value N times.
				if n.Kind == "mcPr" {
					reps := 1
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if n2, err := strconv.Atoi(a.Value); err == nil && n2 > 0 {
								reps = n2
							}
						}
					}
					base := "c"
					if len(n.MatrixColJc) > 0 {
						base = n.MatrixColJc[0]
					}
					n.MatrixColJc = make([]string, reps)
					for i := range n.MatrixColJc {
						n.MatrixColJc[i] = base
					}
				}
			case "strikeH":
				n.StrikeH = true
			case "strikeV":
				n.StrikeV = true
			case "strikeBLTR":
				n.StrikeBLTR = true
			case "strikeTLBR":
				n.StrikeTLBR = true
			case "hideTop":
				if n.Kind == "borderBoxPr" {
					n.HideTop = true
				}
			case "hideBot":
				if n.Kind == "borderBoxPr" {
					n.HideBot = true
				}
			case "hideLeft":
				if n.Kind == "borderBoxPr" {
					n.HideLeft = true
				}
			case "hideRight":
				if n.Kind == "borderBoxPr" {
					n.HideRight = true
				}
			case "borderBoxPr":
				if child.HideTop {
					n.HideTop = true
				}
				if child.HideBot {
					n.HideBot = true
				}
				if child.HideLeft {
					n.HideLeft = true
				}
				if child.HideRight {
					n.HideRight = true
				}
			case "shp":
				if n.Kind == "dPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.DShape = a.Value
						}
					}
				}
			case "degHide":
				if n.Kind == "radPr" {
					n.DegHide = true
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							switch a.Value {
							case "0", "false", "off":
								n.DegHide = false
							}
						}
					}
				}
			case "radPr":
				if child.DegHide {
					n.DegHide = true
				}
			// Phantom-properties wrapper (m:phantPr).
			case "phantPr":
				if child.PhShow {
					n.PhShow = true
				}
				if child.PhZeroWid {
					n.PhZeroWid = true
				}
				if child.PhZeroAsc {
					n.PhZeroAsc = true
				}
				if child.PhZeroDesc {
					n.PhZeroDesc = true
				}
				if child.PhTransp {
					n.PhTransp = true
				}
			case "show":
				if n.Kind == "phantPr" {
					n.PhShow = true
				}
			case "zeroWid":
				if n.Kind == "phantPr" {
					n.PhZeroWid = true
				}
			case "zeroAsc":
				if n.Kind == "phantPr" {
					n.PhZeroAsc = true
				}
			case "zeroDesc":
				if n.Kind == "phantPr" {
					n.PhZeroDesc = true
				}
			case "transp":
				if n.Kind == "phantPr" {
					n.PhTransp = true
				}
			// Matrix and equation-array spacing rules.
			case "rSpRule":
				if n.Kind == "mPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if x, err := strconv.Atoi(a.Value); err == nil {
								n.MatRowSpRule = x
							}
						}
					}
				} else if n.Kind == "eqArrPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if x, err := strconv.Atoi(a.Value); err == nil {
								n.EqRowSpRule = x
							}
						}
					}
				}
			case "cSpRule":
				if n.Kind == "mPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if x, err := strconv.Atoi(a.Value); err == nil {
								n.MatColSpRule = x
							}
						}
					}
				}
			case "plcHide":
				if n.Kind == "mPr" {
					n.MatPlcHide = true
				}
			case "maxDist":
				if n.Kind == "eqArrPr" {
					n.EqMaxDist = true
				}
			case "objDist":
				if n.Kind == "eqArrPr" {
					n.EqObjDist = true
				}
			case "eqArrPr":
				if child.EqMaxDist {
					n.EqMaxDist = true
				}
				if child.EqObjDist {
					n.EqObjDist = true
				}
				if child.EqRowSpRule != 0 {
					n.EqRowSpRule = child.EqRowSpRule
				}
			// Box-properties — operator emulator + break/alignment hints.
			case "opEmu":
				if n.Kind == "boxPr" {
					n.OpEmu = true
				}
			case "noBreak":
				if n.Kind == "boxPr" {
					n.BoxNoBreak = true
				}
			case "diff":
				if n.Kind == "boxPr" {
					n.BoxDiff = true
				}
			case "brk":
				if n.Kind == "boxPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if x, err := strconv.Atoi(a.Value); err == nil {
								n.BoxBrk = x
							}
						}
					}
				}
			case "aln":
				if n.Kind == "boxPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							n.BoxAln = a.Value
						}
					}
				}
			case "boxPr":
				if child.OpEmu {
					n.OpEmu = true
				}
				if child.BoxNoBreak {
					n.BoxNoBreak = true
				}
				if child.BoxDiff {
					n.BoxDiff = true
				}
				if child.BoxBrk != 0 {
					n.BoxBrk = child.BoxBrk
				}
				if child.BoxAln != "" {
					n.BoxAln = child.BoxAln
				}
			// Argument size hint.
			case "argSz":
				if n.Kind == "argPr" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							if x, err := strconv.Atoi(a.Value); err == nil {
								n.ArgSz = x
							}
						}
					}
				}
			case "argPr":
				if child.ArgSz != 0 {
					n.ArgSz = child.ArgSz
				}
			default:
				n.Children = append(n.Children, child)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return n, nil
			}
		case xml.CharData:
			// m:t holds the literal glyph text; everything else is
			// structural. Accumulate CharData into n.Text.
			n.Text += string(t)
		}
		// For attribute-bearing elements (begChr / endChr / sepChr / chr),
		// we also want the "val" attribute (some writers put the literal
		// glyph in val instead of in CharData).
		if start.Name.Local == "begChr" || start.Name.Local == "endChr" || start.Name.Local == "sepChr" || start.Name.Local == "chr" {
			if n.Text == "" {
				for _, a := range start.Attr {
					if a.Name.Local == "val" && a.Value != "" {
						n.Text = a.Value
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
	switch n.Kind {
	case "t":
		return n.Text
	case "r", "e", "num", "den", "deg", "sup", "sub", "lim", "fName", "oMath", "oMathPara":
		return n.joinChildren()
	case "f":
		// Fraction: render as "num/den" with parens around multi-token
		// halves so the precedence reads sensibly.
		num, den := n.Num.render(), n.Den.render()
		return wrapIfComplex(num) + "/" + wrapIfComplex(den)
	case "rad":
		base := n.Base.render()
		deg := n.Deg.render()
		if deg != "" {
			return supScript(deg) + "√(" + base + ")"
		}
		return "√(" + base + ")"
	case "sSup":
		return n.Base.render() + supScript(n.Sup.render())
	case "sSub":
		return n.Base.render() + subScript(n.Sub.render())
	case "sSubSup":
		return n.Base.render() + subScript(n.Sub.render()) + supScript(n.Sup.render())
	case "sPre":
		// Pre-script: subscripts/superscripts placed BEFORE the base.
		// Common in chemistry/isotope notation (e.g. ²³⁵U) and tensor
		// indices. Layout order: ⁢sub⁢sup base.
		return subScript(n.Sub.render()) + supScript(n.Sup.render()) + n.Base.render()
	case "nary":
		op := n.NaryChar
		if op == "" {
			op = "∑"
		}
		// nary's lower/upper limits live in m:sub/m:sup in the wire
		// format — the decoder routes them to n.Sub / n.Sup. Older
		// docs use m:limLow/m:limUpp inside m:nary; respect both.
		lo := n.LimLo.render()
		if lo == "" {
			lo = n.Sub.render()
		}
		up := n.LimUp.render()
		if up == "" {
			up = n.Sup.render()
		}
		body := n.Base.render()
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
		open, close := n.BegChar, n.EndChar
		if open == "" {
			open = "("
		}
		if close == "" {
			close = ")"
		}
		sep := n.SepChar
		if sep == "" {
			sep = ", "
		}
		parts := make([]string, 0, len(n.Children))
		for _, c := range n.Children {
			if c.Kind == "e" {
				parts = append(parts, c.render())
			}
		}
		if len(parts) == 0 {
			parts = append(parts, n.joinChildren())
		}
		return open + strings.Join(parts, sep) + close
	case "func":
		return n.Arg.render() + "(" + n.Base.render() + ")"
	case "limLow":
		return n.Base.render() + subScript(n.LimLo.render())
	case "limUpp":
		return n.Base.render() + supScript(n.LimUp.render())
	case "acc":
		// Accent: stack the accent char after the base (Unicode combining
		// behaviour). If the accent char is empty fall back to U+0302
		// (combining circumflex).
		ch := n.AccChar
		if ch == "" {
			ch = "̂"
		}
		return n.Base.render() + ch
	case "bar":
		// w:barPr w:pos=top → overline; pos=bot → underline. AccUnder
		// captures the under variant.
		if n.AccUnder {
			return n.Base.render() + "̲"
		}
		return "‾" + n.Base.render()
	case "box":
		// m:box is a logical grouping element — Word uses it to prevent
		// breaks across a sub-expression. It is NOT a visible box; the
		// rendered text should be just the contents.
		return n.Base.render()
	case "borderBox":
		// m:borderBox draws a visible rectangle around the contents.
		// Plain-text fallback uses square brackets — the 2D layout
		// actually strokes the box.
		return "[" + n.Base.render() + "]"
	case "groupChr":
		// m:groupChr places a (typically) stretchy character above or
		// below the base. AccUnder selects the "below" placement.
		ch := n.AccChar
		if n.AccUnder {
			if ch == "" {
				ch = "⏟" // bottom curly bracket
			}
			return n.Base.render() + ch
		}
		if ch == "" {
			ch = "⏞" // top curly bracket
		}
		return ch + n.Base.render()
	case "m", "matrix":
		// Matrix: rows separated by "; ", cells in a row by " ".
		out := make([]string, 0, len(n.Rows))
		for _, row := range n.Rows {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = c.render()
			}
			out = append(out, strings.Join(cells, " "))
		}
		return "[" + strings.Join(out, "; ") + "]"
	case "eqArr":
		// Equation array: stack of equations separated by "; ".
		parts := make([]string, 0, len(n.Children))
		for _, c := range n.Children {
			if c.Kind == "e" {
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
	b.WriteString(n.Text)
	for _, c := range n.Children {
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

// mathRun returns a Run carrying the given text styled in italic,
// inheriting the surrounding paragraph's run properties.
func mathRun(text string, paraRPr RunProps) Run {
	rp := paraRPr
	rp.Italic = true
	return Run{Text: text, Props: rp}
}

// mathRunVert returns a Run styled in italic with the given VertAlign
// ("superscript" or "subscript"). The renderer scales the font and
// shifts the baseline; size unchanged otherwise.
func mathRunVert(text, vert string, paraRPr RunProps) Run {
	r := mathRun(text, paraRPr)
	r.Props.VertAlign = vert
	return r
}

// renderMath is the structure-preserving entry point. It walks the
// subtree starting at `start` and emits a slice of Runs with proper
// VertAlign markers where OMML carried structural cues. Falls back
// to extractMathText behavior for unrecognized elements so the
// textual content survives even when the structure can't be modeled.
func renderMath(dec *xml.Decoder, start xml.StartElement, paraRPr RunProps) ([]Run, error) {
	mc := &mathCtx{paraRPr: paraRPr}
	if err := mc.walkChildren(dec, start.Name.Local); err != nil {
		return mc.runs, err
	}
	return mc.runs, nil
}

// mathCtx holds the running list of runs as the walker descends. The
// paragraph rPr seed is reapplied to every emitted run so styling
// (font color, theme) flows through math content.
type mathCtx struct {
	runs    []Run
	paraRPr RunProps
}

func (mc *mathCtx) emit(text string) {
	if text == "" {
		return
	}
	mc.runs = append(mc.runs, mathRun(text, mc.paraRPr))
}

func (mc *mathCtx) emitVert(text, vert string) {
	if text == "" {
		return
	}
	mc.runs = append(mc.runs, mathRunVert(text, vert, mc.paraRPr))
}

// walkChildren drives the recursive descent. It consumes tokens
// until the closing EndElement for `parent` is seen. Dispatch is on
// the local element name (OMML uses the `m:` prefix throughout).
func (mc *mathCtx) walkChildren(dec *xml.Decoder, parent string) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == parent {
				return nil
			}
		case xml.StartElement:
			if err := mc.handle(dec, t); err != nil {
				return err
			}
		}
	}
}

// handle dispatches one StartElement. The mapping below covers the
// OMML elements named in the file comment; unknown elements fall
// through to the generic text walk so their CharData survives.
func (mc *mathCtx) handle(dec *xml.Decoder, t xml.StartElement) error {
	switch t.Name.Local {
	case "r":
		return mc.handleRun(dec, t)
	case "t":
		return mc.handleText(dec, t, "")
	case "f":
		return mc.handleFraction(dec, t)
	case "sSup":
		return mc.handleScript(dec, t, "superscript")
	case "sSub":
		return mc.handleScript(dec, t, "subscript")
	case "sSubSup":
		return mc.handleSubSup(dec, t)
	case "rad":
		return mc.handleRadical(dec, t)
	case "nary":
		return mc.handleNary(dec, t)
	case "d":
		return mc.handleDelim(dec, t)
	case "func":
		return mc.handleFunc(dec, t)
	case "limLow":
		return mc.handleLim(dec, t, "subscript")
	case "limUpp":
		return mc.handleLim(dec, t, "superscript")
	case "bar":
		return mc.handleBar(dec, t)
	case "m":
		return mc.handleMatrix(dec, t)
	case "phant", "acc", "groupChr", "box":
		// Visually-passive wrappers — just emit the child element's
		// text. Accents/groups would need glyph positioning we don't
		// have, so we drop the accent and keep the base.
		return mc.walkChildren(dec, t.Name.Local)
	case "eqArr":
		return mc.handleEqArr(dec, t)
	case "oMath", "oMathPara", "e", "fName", "deg", "ePr", "rPr", "ctrlPr",
		"naryPr", "fPr", "radPr", "dPr", "funcPr", "limLowPr", "limUppPr",
		"barPr", "mPr", "phantPr", "accPr", "groupChrPr", "boxPr",
		"sSubPr", "sSupPr", "sSubSupPr", "eqArrPr", "mcs", "mc", "mcPr", "mcJc":
		// Property containers + "e" (argument bodies) — recurse so
		// nested elements get processed.
		return mc.walkChildren(dec, t.Name.Local)
	default:
		return dec.Skip()
	}
}

// handleRun handles m:r (a math run) — the equivalent of w:r but
// inside OMML. Its m:t children are emitted as math text.
func (mc *mathCtx) handleRun(dec *xml.Decoder, t xml.StartElement) error {
	return mc.walkChildren(dec, t.Name.Local)
}

// handleText reads a single m:t element's CharData and emits it,
// optionally with VertAlign.
func (mc *mathCtx) handleText(dec *xml.Decoder, t xml.StartElement, vert string) error {
	var s string
	if err := dec.DecodeElement(&s, &t); err != nil {
		return err
	}
	if vert != "" {
		mc.emitVert(s, vert)
	} else {
		mc.emit(s)
	}
	return nil
}

// handleFraction emits "(num)/(den)" with parentheses so the slash
// can't be mistaken for division of arbitrary inline terms.
func (mc *mathCtx) handleFraction(dec *xml.Decoder, t xml.StartElement) error {
	num, den, err := mc.readNumDen(dec, t.Name.Local)
	if err != nil {
		return err
	}
	mc.emit("(")
	mc.runs = append(mc.runs, num...)
	mc.emit(")/(")
	mc.runs = append(mc.runs, den...)
	mc.emit(")")
	return nil
}

// readNumDen reads an m:f subtree consuming m:num and m:den into
// separate Run slices.
func (mc *mathCtx) readNumDen(dec *xml.Decoder, parent string) (num, den []Run, err error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return num, den, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == parent {
				return num, den, nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "num":
				rs, err := mc.collect(dec, t.Name.Local)
				if err != nil {
					return num, den, err
				}
				num = rs
			case "den":
				rs, err := mc.collect(dec, t.Name.Local)
				if err != nil {
					return num, den, err
				}
				den = rs
			default:
				if err := dec.Skip(); err != nil {
					return num, den, err
				}
			}
		}
	}
}

// collect runs walkChildren in a temporary mathCtx and returns just
// the runs it produced — used when a parent (m:num, m:e, m:sup, ...)
// owns a self-contained subexpression that should not yet be flushed
// to the outer paragraph.
func (mc *mathCtx) collect(dec *xml.Decoder, parent string) ([]Run, error) {
	inner := &mathCtx{paraRPr: mc.paraRPr}
	if err := inner.walkChildren(dec, parent); err != nil {
		return inner.runs, err
	}
	return inner.runs, nil
}

// handleScript handles m:sSup or m:sSub. The base m:e is emitted at
// normal baseline; the m:sup or m:sub child is emitted as a
// VertAlign'd run sequence.
func (mc *mathCtx) handleScript(dec *xml.Decoder, t xml.StartElement, vert string) error {
	scriptElem := "sup"
	if vert == "subscript" {
		scriptElem = "sub"
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "e":
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
			case scriptElem:
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				for _, r := range rs {
					r.Props.VertAlign = vert
					mc.runs = append(mc.runs, r)
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleSubSup handles m:sSubSup — base + sub + sup all on one
// element. We emit base, then super, then sub (Word's convention).
func (mc *mathCtx) handleSubSup(dec *xml.Decoder, t xml.StartElement) error {
	var base, sub, sup []Run
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				mc.runs = append(mc.runs, base...)
				for _, r := range sup {
					r.Props.VertAlign = "superscript"
					mc.runs = append(mc.runs, r)
				}
				for _, r := range sub {
					r.Props.VertAlign = "subscript"
					mc.runs = append(mc.runs, r)
				}
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "e":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				base = rs
			case "sub":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				sub = rs
			case "sup":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				sup = rs
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleRadical emits "√(arg)" or "ⁿ√(arg)" when an explicit degree
// is set. We use Unicode SQUARE ROOT U+221A as the radical sign.
func (mc *mathCtx) handleRadical(dec *xml.Decoder, t xml.StartElement) error {
	var arg, deg []Run
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				for _, r := range deg {
					r.Props.VertAlign = "superscript"
					mc.runs = append(mc.runs, r)
				}
				mc.emit("√(")
				mc.runs = append(mc.runs, arg...)
				mc.emit(")")
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "e":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				arg = rs
			case "deg":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				deg = rs
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleNary emits an n-ary operator with attached limits. The
// operator character comes from m:naryPr/m:chr (defaulting to "∑").
// Sub becomes inferior limit, sup becomes superior limit, e is the
// body.
func (mc *mathCtx) handleNary(dec *xml.Decoder, t xml.StartElement) error {
	op := "∑"
	var sub, sup, body []Run
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				mc.emit(op)
				for _, r := range sub {
					r.Props.VertAlign = "subscript"
					mc.runs = append(mc.runs, r)
				}
				for _, r := range sup {
					r.Props.VertAlign = "superscript"
					mc.runs = append(mc.runs, r)
				}
				mc.runs = append(mc.runs, body...)
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "naryPr":
				if c, err := readNaryChr(dec, tt); err == nil && c != "" {
					op = c
				}
			case "e":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				body = rs
			case "sub":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				sub = rs
			case "sup":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				sup = rs
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// readNaryChr reads the m:naryPr block looking for m:chr — the
// custom operator character. Returns "" if not specified.
func readNaryChr(dec *xml.Decoder, start xml.StartElement) (string, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return "", nil
			}
		case xml.StartElement:
			if t.Name.Local == "chr" {
				if v := attr(t, "val"); v != "" {
					_ = dec.Skip()
					// drain to parent's end
					for {
						tok2, err := dec.Token()
						if err != nil {
							return v, err
						}
						if e, ok := tok2.(xml.EndElement); ok && e.Name.Local == start.Name.Local {
							return v, nil
						}
					}
				}
			}
			if err := dec.Skip(); err != nil {
				return "", err
			}
		}
	}
}

// handleDelim emits the argument wrapped in delimiters. m:dPr can
// override the open/close characters; default to parentheses.
func (mc *mathCtx) handleDelim(dec *xml.Decoder, t xml.StartElement) error {
	beg, end := "(", ")"
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "dPr":
				b, e, err := readDPrDelims(dec, tt)
				if err != nil {
					return err
				}
				if b != "" {
					beg = b
				}
				if e != "" {
					end = e
				}
			case "e":
				mc.emit(beg)
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
				mc.emit(end)
				// reset to default for subsequent siblings in m:d
				// (rare — multiple m:e inside one m:d uses a separator)
				beg, end = "", ""
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

func readDPrDelims(dec *xml.Decoder, start xml.StartElement) (beg, end string, err error) {
	for {
		tok, terr := dec.Token()
		if terr != nil {
			return beg, end, terr
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return beg, end, nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "begChr":
				beg = attr(t, "val")
				_ = dec.Skip()
			case "endChr":
				end = attr(t, "val")
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		}
	}
}

// handleFunc emits "fName(arg)" — m:fName children are the function
// label, m:e is the argument.
func (mc *mathCtx) handleFunc(dec *xml.Decoder, t xml.StartElement) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "fName":
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
				mc.emit("(")
			case "e":
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
				mc.emit(")")
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleLim emits the base with the limit script attached as a
// VertAlign'd run. m:limLow/m:limUpp differ only in whether the
// limit is sub or super.
func (mc *mathCtx) handleLim(dec *xml.Decoder, t xml.StartElement, vert string) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "e":
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
			case "lim":
				rs, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				for _, r := range rs {
					r.Props.VertAlign = vert
					mc.runs = append(mc.runs, r)
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleBar emits the argument followed by U+0305 (combining overline)
// for an overbar or U+0332 (combining underline) for an underbar.
// Many fonts position these poorly, but the structural intent
// survives.
func (mc *mathCtx) handleBar(dec *xml.Decoder, t xml.StartElement) error {
	mark := "̅" // combining overline (default — m:pos="top")
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch tt.Name.Local {
			case "barPr":
				if v := readPropVal(dec, tt, "pos"); v == "bot" {
					mark = "̲"
				}
			case "e":
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
				mc.emit(mark)
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// handleMatrix emits matrix rows as "[ a, b ; c, d ]" — bracketed
// with row separators. Each m:mr is one row of m:e cells.
func (mc *mathCtx) handleMatrix(dec *xml.Decoder, t xml.StartElement) error {
	rows := [][]Run{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				mc.emit("[ ")
				for i, row := range rows {
					if i > 0 {
						mc.emit(" ; ")
					}
					mc.runs = append(mc.runs, row...)
				}
				mc.emit(" ]")
				return nil
			}
		case xml.StartElement:
			if tt.Name.Local == "mr" {
				row, err := mc.collectRow(dec, tt.Name.Local)
				if err != nil {
					return err
				}
				rows = append(rows, row)
			} else {
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// collectRow collects cells (m:e) of one matrix row into a single
// Run slice, comma-separated.
func (mc *mathCtx) collectRow(dec *xml.Decoder, parent string) ([]Run, error) {
	var out []Run
	first := true
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == parent {
				return out, nil
			}
		case xml.StartElement:
			if tt.Name.Local == "e" {
				cell, err := mc.collect(dec, tt.Name.Local)
				if err != nil {
					return out, err
				}
				if !first {
					out = append(out, mathRun(", ", mc.paraRPr))
				}
				out = append(out, cell...)
				first = false
			} else {
				if err := dec.Skip(); err != nil {
					return out, err
				}
			}
		}
	}
}

// handleEqArr emits an equation array with rows separated by newlines.
func (mc *mathCtx) handleEqArr(dec *xml.Decoder, t xml.StartElement) error {
	first := true
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tt := tok.(type) {
		case xml.EndElement:
			if tt.Name.Local == t.Name.Local {
				return nil
			}
		case xml.StartElement:
			if tt.Name.Local == "e" {
				if !first {
					mc.emit("\n")
				}
				if err := mc.walkChildren(dec, tt.Name.Local); err != nil {
					return err
				}
				first = false
			} else {
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// readPropVal scans inside a property element for a child element
// named `name` and returns its `val` attribute. Used for one-shot
// property lookups like m:barPr/m:pos.
func readPropVal(dec *xml.Decoder, start xml.StartElement, name string) string {
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return ""
			}
		case xml.StartElement:
			if t.Name.Local == name {
				v := attr(t, "val")
				_ = dec.Skip()
				// drain to parent end
				for {
					tok2, err := dec.Token()
					if err != nil {
						return v
					}
					if e, ok := tok2.(xml.EndElement); ok && e.Name.Local == start.Name.Local {
						return v
					}
				}
			}
			_ = dec.Skip()
		}
	}
}
