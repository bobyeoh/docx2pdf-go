package docx

import "encoding/xml"

// w:sdt (Structured Document Tag) is a transparent wrapper used heavily
// by modern Word templates for form controls, content controls, dropdowns,
// rich text containers, and repeating sections. The wrapper itself has no
// rendering effect — its <w:sdtContent> child holds the actual content.
//
// SDTs appear at two structural levels:
//
//   Block level (in body / cell / header / footer / note):
//     <w:sdt>
//       <w:sdtPr>…</w:sdtPr>
//       <w:sdtEndPr>…</w:sdtEndPr>
//       <w:sdtContent>
//         <w:p>…</w:p>     ← paragraphs and tables go here
//         <w:tbl>…</w:tbl>
//       </w:sdtContent>
//     </w:sdt>
//
//   Inline level (inside a paragraph):
//     <w:sdt>
//       <w:sdtPr>…</w:sdtPr>
//       <w:sdtContent>
//         <w:r>…</w:r>     ← runs (and nested wrappers) go here
//       </w:sdtContent>
//     </w:sdt>
//
// We treat the wrapper as transparent: its child elements are decoded
// in place and appended to the parent collection. sdtPr / sdtEndPr are
// ignored — we don't currently honor field validation, dropdown lists,
// or date pickers.

// decodeBlockSdt walks a block-level <w:sdt> subtree and returns the
// paragraphs and tables found inside its <w:sdtContent>. Nested SDTs are
// flattened. Used by parseDocument, parseTableCellContents,
// parseHeaderFooter, and parseNoteBody.
func decodeBlockSdt(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, error) {
	var out []Block
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sdtContent":
				if err := decodeSdtContentBlocks(dec, t, pctx, &out); err != nil {
					return nil, err
				}
			default:
				// sdtPr, sdtEndPr, namespaced extensions — drop.
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out, nil
			}
		}
	}
}

// decodeSdtContentBlocks dispatches block-level children of <w:sdtContent>
// through the same paragraph/table decoders the surrounding context would
// use. Nested w:sdt elements are flattened.
func decodeSdtContentBlocks(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext, out *[]Block) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, p)
			case "tbl":
				tbl, err := decodeTable(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, tbl)
			case "sdt":
				nested, err := decodeBlockSdt(dec, t, pctx)
				if err != nil {
					return err
				}
				*out = append(*out, nested...)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

// decodeInlineSdt walks an inline <w:sdt> appearing inside a paragraph.
// Finds <w:sdtContent>, then dispatches its child runs (and nested
// wrappers) through decodeWrapper so the contained text reaches the
// surrounding paragraph. `drop` propagates to nested wrappers — an SDT
// inside a w:del still drops.
func decodeInlineSdt(dec *xml.Decoder, start xml.StartElement, p *Paragraph, paraRPr RunProps, pctx *parseDocContext, drop bool) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "sdtContent" {
				if err := decodeWrapper(dec, t, p, paraRPr, pctx, drop); err != nil {
					return err
				}
			} else {
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}
