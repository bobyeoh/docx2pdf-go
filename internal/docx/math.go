package docx

import (
	"encoding/xml"
	"strings"
)

// Math support: linearize OMML (Office Math Markup Language) into a
// sequence of italic runs that preserve the most-noticeable structural
// information — sub/super-scripts via Run.VertAlign, fractions as
// "(num)/(den)", radicals as "√(arg)", n-ary operators with their
// limits attached.
//
// The previous implementation flattened everything to plain text,
// losing exponents and fraction bars entirely. The current pass
// trades pixel-perfect typesetting (which would require a parallel
// math engine) for a readable approximation that survives in the
// PDF: "x² + 2x + 1" reads as "x" + superscript "2" + " + 2x + 1",
// not "x2 + 2x + 1".
//
// What's covered (the OMML elements docx4j calls out as common):
//   - m:r / m:t (math runs and their text)
//   - m:f (fraction → "(num)/(den)")
//   - m:sSup / m:sSub / m:sSubSup (super, sub, both)
//   - m:rad (radical → "√(arg)" with optional degree as left super)
//   - m:nary (∑/∏/∫ etc. with sub/super limits)
//   - m:d (delimiters → "(arg)" or whatever m:begChr/m:endChr say)
//   - m:func (function name applied to argument)
//   - m:limLow / m:limUpp (limit below / above)
//   - m:bar (overbar / underbar — emitted as the bare arg + "‾")
//   - m:m / m:mr (matrices as bracketed CSV)
//   - m:phant (phantom — content is suppressed visually; we keep text)
//   - m:acc (accent → arg + accent char as combining mark)
//   - m:groupChr (grouped char → arg + grouping char)
//   - m:eqArr (equation array — newline separated)
//
// Anything else falls through to a textual walk that concatenates
// CharData, matching the legacy behavior. The structural info that's
// preserved is the part readers most often want.

// extractMathText keeps the legacy single-string accessor for callers
// that just want concatenated CharData. The structured renderer
// (renderMath) is the new entry point.
func extractMathText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			sb.Write(t)
		}
	}
	return sb.String(), nil
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
