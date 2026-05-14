package docx

import "encoding/xml"

// Math support is intentionally minimal: we extract the visible text from
// an m:oMath / m:oMathPara subtree and emit it as a single italic run. The
// structural information (fractions, sub/superscripts, matrices, ...) is
// lost, but the textual content survives — which is far better than the
// silent drop that the default `dec.Skip()` branch produced before.
//
// Real math typesetting needs a parallel renderer (think MathML → glyph
// positioning); docx4j routes this through MathML + a TeX engine. That is
// out of scope for a pure-Go pipeline.

// extractMathText walks any subtree starting at `start` and returns the
// concatenated character-data (text) content. It stops when the matching
// EndElement is encountered. Whitespace is preserved as it appears in the
// source — for typical inline equations this yields a readable approximation
// like "x² + 2x + 1".
func extractMathText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb []byte
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return string(sb), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			sb = append(sb, t...)
		}
	}
	return string(sb), nil
}

// mathRun returns a Run carrying the extracted math text styled in italic,
// inheriting the surrounding paragraph's run properties.
func mathRun(text string, paraRPr RunProps) Run {
	rp := paraRPr
	rp.Italic = true
	return Run{Text: text, Props: rp}
}
