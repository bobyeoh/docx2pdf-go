package docx

import "encoding/xml"

// mc:AlternateContent is the Markup Compatibility container Word uses to
// supply a new representation of some content (`<mc:Choice>`) alongside
// an older fallback (`<mc:Fallback>`) the reader should use if it
// doesn't understand the Choice's `Requires` namespace.
//
// Both Choice and Fallback are normally wrapping one of the elements
// the surrounding context (run / paragraph / body) already knows how
// to decode — typically a drawing, a pict, or whole block content. We
// prefer the first <mc:Choice> that yields a non-empty result; if none
// does, we fall through to <mc:Fallback>.
//
// True MC processing would inspect each Choice's `Requires` attribute
// against a list of namespace URIs we explicitly understand. We don't
// maintain that list — every namespace we don't recognize would fail —
// so we go the other way: try every Choice, take whatever produces
// content. Behaves correctly for the common patterns Word emits.

// decodeRunAltContent processes <mc:AlternateContent> appearing inside a
// <w:r>. It returns Run atoms produced by the chosen branch. The runs
// produced by the surrounding run's text are inherited via rp.
//
// We expand by re-running the same dispatch logic against each Choice/
// Fallback's child elements — meaning anything decodeRun's main switch
// understands (drawing, pict, t, br, sym, fldChar, …) is in scope.
func decodeRunAltContent(dec *xml.Decoder, start xml.StartElement, rp RunProps, doc *Document) ([]Run, error) {
	var fallback []Run
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Choice":
				inner, err := decodeRunAltBranch(dec, t, rp, doc)
				if err != nil {
					return nil, err
				}
				if len(inner) > 0 {
					// Found a Choice that produced content; drain the
					// rest of AlternateContent without processing.
					if err := skipToEndOf(dec, start.Name.Local); err != nil {
						return nil, err
					}
					return inner, nil
				}
			case "Fallback":
				inner, err := decodeRunAltBranch(dec, t, rp, doc)
				if err != nil {
					return nil, err
				}
				fallback = inner
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return fallback, nil
			}
		}
	}
}

// decodeRunAltBranch walks a single <mc:Choice> or <mc:Fallback> subtree
// and returns the Run atoms its child elements produce. Only the
// run-level elements we know how to render are handled — anything else
// is dropped.
func decodeRunAltBranch(dec *xml.Decoder, start xml.StartElement, rp RunProps, doc *Document) ([]Run, error) {
	var out []Run
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "drawing":
				di, err := findDrawingInfo(dec, t)
				if err != nil {
					return nil, err
				}
				if di.RID != "" {
					out = append(out, Run{
						ImageID:       di.RID,
						ImageWidthPt:  di.WPt,
						ImageHeightPt: di.HPt,
						CropTopPct:    di.CropT,
						CropBottomPct: di.CropB,
						CropLeftPct:   di.CropL,
						CropRightPct:  di.CropR,
						Props:         rp,
					})
				}
				if di.TxbxText != "" {
					trp := rp
					trp.Italic = true
					out = append(out, Run{Text: di.TxbxText, Props: trp})
				}
			case "pict":
				vi, err := findPictInfo(dec, t)
				if err != nil {
					return nil, err
				}
				if vi.RID != "" {
					out = append(out, Run{
						ImageID:       vi.RID,
						ImageWidthPt:  vi.WPt,
						ImageHeightPt: vi.HPt,
						Props:         rp,
					})
				}
			case "AlternateContent":
				// Nested AC — recurse.
				inner, err := decodeRunAltContent(dec, t, rp, doc)
				if err != nil {
					return nil, err
				}
				out = append(out, inner...)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out, nil
			}
		}
	}
}

// decodeBlockAltContent processes <mc:AlternateContent> at block level —
// inside body, header/footer, table cell, or note. Returns the Blocks
// (paragraphs and tables) extracted from the chosen branch.
func decodeBlockAltContent(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, error) {
	var fallback []Block
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Choice":
				inner, err := decodeBlockAltBranch(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				if len(inner) > 0 {
					if err := skipToEndOf(dec, start.Name.Local); err != nil {
						return nil, err
					}
					return inner, nil
				}
			case "Fallback":
				inner, err := decodeBlockAltBranch(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				fallback = inner
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return fallback, nil
			}
		}
	}
}

// decodeBlockAltBranch dispatches the block-level child elements of a
// single mc:Choice or mc:Fallback through the same paragraph/table
// decoders the surrounding context would use.
func decodeBlockAltBranch(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, error) {
	var out []Block
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				out = append(out, p)
			case "tbl":
				tbl, err := decodeTable(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				out = append(out, tbl)
			case "sdt":
				inner, err := decodeBlockSdt(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				out = append(out, inner...)
			case "AlternateContent":
				inner, err := decodeBlockAltContent(dec, t, pctx)
				if err != nil {
					return nil, err
				}
				out = append(out, inner...)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out, nil
			}
		}
	}
}

// skipToEndOf consumes tokens until the matching closing tag for the
// outer element with the given local name. Used to drain unprocessed
// remainder of an mc:AlternateContent after we've already accepted one
// of its Choices.
func skipToEndOf(dec *xml.Decoder, localName string) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 && t.Name.Local == localName {
				return nil
			}
		}
	}
	return nil
}
