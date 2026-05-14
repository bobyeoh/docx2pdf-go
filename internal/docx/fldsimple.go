package docx

import "encoding/xml"

// decodeFldSimple translates a <w:fldSimple w:instr="…"> element into the
// same marker stream the complex form (fldChar / instrText) emits:
//
//	{FieldBegin} {InstrText: instr} {FieldSep} <child runs> {FieldEnd}
//
// The renderer's flattenFields state machine then handles the substitution
// the same way regardless of which encoding the source used. This is
// important because Word frequently writes the simple form for PAGE in
// headers/footers and the complex form for more elaborate fields.
//
// Child elements of fldSimple are the cached result — typically one or
// more w:r entries — and are passed through verbatim. Nested wrappers
// (smartTag, ins/del, sdt) inside the result region are routed via
// decodeWrapper so they keep behaving like wrappers.
func decodeFldSimple(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext) error {
	instr := attr(start, "instr")

	// Emit begin + instr + separator markers up front. The body that
	// follows is the "result" region.
	p.Runs = append(p.Runs, Run{FieldBegin: true, Props: paraRPr})
	if instr != "" {
		p.Runs = append(p.Runs, Run{InstrText: instr, Props: paraRPr})
	}
	p.Runs = append(p.Runs, Run{FieldSep: true, Props: paraRPr})

	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "r":
				runs, err := decodeRun(dec, t, paraRPr, pctx.doc)
				if err != nil {
					return err
				}
				p.Runs = append(p.Runs, runs...)
				if len(pctx.bmActive) > 0 {
					var sb []byte
					for _, rn := range runs {
						sb = append(sb, rn.Text...)
					}
					if s := string(sb); s != "" {
						for name := range pctx.bmActive {
							pctx.doc.Bookmarks[name] += s
						}
					}
				}
			case "hyperlink":
				if err := decodeHyperlink(dec, t, p, paraRPr, pctx); err != nil {
					return err
				}
			case "ins", "moveTo", "del", "moveFrom", "smartTag", "customXml":
				childDrop := t.Name.Local == "del" || t.Name.Local == "moveFrom"
				if err := decodeWrapper(dec, t, p, paraRPr, pctx, childDrop); err != nil {
					return err
				}
			case "sdt":
				if err := decodeInlineSdt(dec, t, p, paraRPr, pctx, false); err != nil {
					return err
				}
			case "fldSimple":
				// Nested simple field — rare but legal. Just recurse.
				if err := decodeFldSimple(dec, t, p, paraRPr, pctx); err != nil {
					return err
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				p.Runs = append(p.Runs, Run{FieldEnd: true, Props: paraRPr})
				return nil
			}
		}
	}
}
