package docx

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// themeFor returns doc.Theme if doc is non-nil, else an empty Theme. Used
// by lower-level parsers that may be invoked outside a Document context.
func themeFor(doc *Document) Theme {
	if doc == nil {
		return Theme{}
	}
	return doc.Theme
}

// PatternFill records a DrawingML a:pattFill so the renderer can repeat a
// real tile rather than a flat average color. Preset names follow the
// OOXML enumeration (e.g. "dkHorz", "ltDnDiag", "pct25", "diagBrick").
type PatternFill struct {
	Preset string // a:pattFill@prst — empty if none
	FgHex  string // 6-hex foreground color (post-modifier)
	BgHex  string // 6-hex background color (post-modifier); empty = white
}

// effectBag is the bundle parseEffectListExt returns.
type effectBag struct {
	Shadow      *ShadowEffect
	InnerShadow *ShadowEffect // visually approximated as a darker outerShdw
	Glow        *GlowEffect
	Reflection  *ReflectionEffect
	SoftEdgePt  float64
}

// parseGradFillTheme is the theme-aware variant of parseGradFill — each
// gradient stop's color is resolved via ScanColor against the supplied
// Theme so schemeClr references / modifier chains work.
func parseGradFillTheme(dec *xml.Decoder, start xml.StartElement, theme Theme) (stops []GradientStop, angleDeg float64, kind string, err error) {
	kind = "linear"
	for {
		tok, e := dec.Token()
		if e != nil {
			return stops, angleDeg, kind, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "gsLst":
				if err := parseGsLstTheme(dec, t, &stops, theme); err != nil {
					return stops, angleDeg, kind, err
				}
			case "lin":
				kind = "linear"
				if v := attr(t, "ang"); v != "" {
					if a, e := strconv.ParseFloat(v, 64); e == nil {
						angleDeg = a / 60000.0
					}
				}
				_ = dec.Skip()
			case "path":
				kind = "radial"
				_ = dec.Skip()
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return stops, angleDeg, kind, nil
			}
		}
	}
}

// parseGsLstTheme is the theme-aware twin of parseGsLst.
func parseGsLstTheme(dec *xml.Decoder, start xml.StartElement, stops *[]GradientStop, theme Theme) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "gs" {
				stop := GradientStop{Alpha: 1}
				if v := attr(t, "pos"); v != "" {
					if x, err := strconv.ParseFloat(v, 64); err == nil {
						stop.Pos = x / 100000.0
					}
				}
				if c := ScanColor(dec, t, theme); c != "" {
					stop.Color = c
				}
				*stops = append(*stops, stop)
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

// pattFillSpec is the richer return from parsePattFillSpec.
type pattFillSpec struct {
	Preset   string // raw OOXML preset name (e.g. "dkHorz")
	FgHex    string
	BgHex    string
	AvgColor string // legacy fallback color (per-channel average)
}

// parsePattFillSpec parses <a:pattFill prst="…"> capturing the preset name
// and both fg/bg colors (theme-resolved). Backwards-compatible result
// AvgColor preserves the old "average tone" behaviour for renderers that
// don't know the preset.
func parsePattFillSpec(dec *xml.Decoder, start xml.StartElement, theme Theme) (pattFillSpec, error) {
	out := pattFillSpec{Preset: attr(start, "prst")}
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "fgClr":
				if c := ScanColor(dec, t, theme); c != "" {
					out.FgHex = c
				}
			case "bgClr":
				if c := ScanColor(dec, t, theme); c != "" {
					out.BgHex = c
				}
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				switch {
				case out.FgHex != "" && out.BgHex != "":
					out.AvgColor = averageHexColor(out.FgHex, out.BgHex)
				case out.FgHex != "":
					out.AvgColor = out.FgHex
				case out.BgHex != "":
					out.AvgColor = out.BgHex
				}
				return out, nil
			}
		}
	}
}

// parseEffectListExt scans <a:effectLst> picking up the four effects the
// renderer can approximate: outerShdw, innerShdw, glow, reflection,
// softEdge. Order in the XML doesn't matter — the renderer composites
// them in z-order shadow → fill → innerShadow → glow → reflection.
func parseEffectListExt(dec *xml.Decoder, start xml.StartElement, theme Theme) (effectBag, error) {
	var out effectBag
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "outerShdw":
				if out.Shadow == nil {
					out.Shadow = parseShadowElem(dec, t, theme)
				} else {
					_ = dec.Skip()
				}
			case "innerShdw":
				if out.InnerShadow == nil {
					out.InnerShadow = parseShadowElem(dec, t, theme)
				} else {
					_ = dec.Skip()
				}
			case "prstShdw":
				// Preset shadow: distill to outer-shdw default behaviour.
				if out.Shadow == nil {
					out.Shadow = parseShadowElem(dec, t, theme)
				} else {
					_ = dec.Skip()
				}
			case "glow":
				if out.Glow == nil {
					eff := &GlowEffect{Alpha: 1, Color: "FFFF00"}
					if v := attr(t, "rad"); v != "" {
						if x, e := strconv.ParseFloat(v, 64); e == nil {
							eff.RadiusPt = x / emuPerPt
						}
					}
					if c := ScanColor(dec, t, theme); c != "" {
						eff.Color = c
					}
					out.Glow = eff
				} else {
					_ = dec.Skip()
				}
			case "reflection":
				if out.Reflection == nil {
					eff := &ReflectionEffect{StartA: 0.5, EndA: 0.0}
					if v := attr(t, "blurRad"); v != "" {
						if x, e := strconv.ParseFloat(v, 64); e == nil {
							eff.BlurPt = x / emuPerPt
						}
					}
					if v := attr(t, "stA"); v != "" {
						eff.StartA = parsePercentMod(v)
					}
					if v := attr(t, "endA"); v != "" {
						eff.EndA = parsePercentMod(v)
					}
					if v := attr(t, "dist"); v != "" {
						if x, e := strconv.ParseFloat(v, 64); e == nil {
							eff.DistPt = x / emuPerPt
						}
					}
					if v := attr(t, "fadeDir"); v != "" {
						if x, e := strconv.ParseFloat(v, 64); e == nil {
							eff.FadeDirDg = x / 60000.0
						}
					}
					out.Reflection = eff
					_ = dec.Skip()
				} else {
					_ = dec.Skip()
				}
			case "softEdge":
				if out.SoftEdgePt == 0 {
					if v := attr(t, "rad"); v != "" {
						if x, e := strconv.ParseFloat(v, 64); e == nil {
							out.SoftEdgePt = x / emuPerPt
						}
					}
				}
				_ = dec.Skip()
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

// parseShadowElem reads a single <a:outerShdw>/<a:innerShdw>/<a:prstShdw>
// element including its color subtree.
func parseShadowElem(dec *xml.Decoder, start xml.StartElement, theme Theme) *ShadowEffect {
	eff := &ShadowEffect{Alpha: 1, Color: "000000"}
	if v := attr(start, "blurRad"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			eff.BlurPt = x / emuPerPt
		}
	}
	dist := 0.0
	dirDeg := 0.0
	if v := attr(start, "dist"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			dist = x / emuPerPt
		}
	}
	if v := attr(start, "dir"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			dirDeg = x / 60000.0
		}
	}
	rad := dirDeg * pi180
	eff.OffsetXPt = dist * cosF(rad)
	eff.OffsetYPt = dist * sinF(rad)
	if c := ScanColor(dec, start, theme); c != "" {
		eff.Color = c
	}
	return eff
}

// trimHash returns hex without leading '#'.
func trimHash(s string) string { return strings.TrimPrefix(s, "#") }

// parseBlipEffects walks an <a:blip> subtree collecting per-pixel filter
// children. start is the <a:blip> element token; on return the decoder
// has consumed up through </a:blip>.
//
// Also returns the asvg:svgBlip embed rId when the blip's extLst carries
// an Office 365 SVG extension — callers prefer this rId over the raster
// preview embed so vector graphics render at native resolution.
func parseBlipEffects(dec *xml.Decoder, start xml.StartElement, theme Theme) ([]ImageEffect, string) {
	var out []ImageEffect
	svgRID := ""
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out, svgRID
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "svgBlip":
				// asvg:svgBlip r:embed="rIdN" — the SVG source for the
				// raster preview held by the outer a:blip.
				for _, a := range t.Attr {
					if a.Name.Local == "embed" && svgRID == "" {
						svgRID = a.Value
					}
				}
				_ = dec.Skip()
			case "alphaModFix":
				eff := ImageEffect{Kind: "alphaModFix", Amount: 100}
				if v := attr(t, "amt"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Amount = x / 1000.0 // 1/1000 of percent
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "lum":
				eff := ImageEffect{Kind: "lum"}
				if v := attr(t, "bright"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Bright = x / 100000.0
					}
				}
				if v := attr(t, "contrast"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Contrast = x / 100000.0
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "biLevel":
				eff := ImageEffect{Kind: "biLevel", Threshold: 0.5}
				if v := attr(t, "thresh"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Threshold = x / 100000.0
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "grayscl":
				out = append(out, ImageEffect{Kind: "grayscl"})
				_ = dec.Skip()
			case "duotone":
				eff := ImageEffect{Kind: "duotone"}
				// Two color children — pick the first two leaves.
				cs := drainColorListUntilEnd(dec, t, theme, 2)
				if len(cs) > 0 {
					eff.FgHex = cs[0]
				}
				if len(cs) > 1 {
					eff.BgHex = cs[1]
				} else {
					eff.BgHex = "FFFFFF"
				}
				out = append(out, eff)
				depth-- // drainColorListUntilEnd consumed the EndElement
			case "clrChange":
				eff := ImageEffect{Kind: "clrChange"}
				cs := drainClrChangeFromTo(dec, t, theme)
				eff.FgHex = cs[0]
				eff.BgHex = cs[1]
				out = append(out, eff)
				depth--
			case "blur":
				eff := ImageEffect{Kind: "blur"}
				if v := attr(t, "rad"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.BlurRadiusPx = x / emuPerPt
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "hsl":
				// a:hsl carries hue (degrees * 60000), saturation
				// (1/100000), and lum (1/100000). Signed.
				eff := ImageEffect{Kind: "hsl"}
				if v := attr(t, "hue"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.HueDeg = x / 60000.0
					}
				}
				if v := attr(t, "sat"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Saturation = x / 100000.0
					}
				}
				if v := attr(t, "lum"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Lum = x / 100000.0
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "tint":
				eff := ImageEffect{Kind: "tint"}
				if v := attr(t, "amt"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Amount = x / 1000.0 // 0..100000 → 0..100
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "shade":
				eff := ImageEffect{Kind: "shade"}
				if v := attr(t, "amt"); v != "" {
					if x, e := strconv.ParseFloat(v, 64); e == nil {
						eff.Amount = x / 1000.0
					}
				}
				out = append(out, eff)
				_ = dec.Skip()
			case "alphaInv":
				out = append(out, ImageEffect{Kind: "alphaInv"})
				_ = dec.Skip()
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return out, svgRID
}

// drainColorListUntilEnd reads color leaves inside start until limit
// reached or the matching EndElement. Returns the resolved hex strings
// in document order.
func drainColorListUntilEnd(dec *xml.Decoder, start xml.StartElement, theme Theme, limit int) []string {
	var out []string
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(out) < limit {
				if v, ok := colorLeafValue(t, theme); ok {
					mods := readColorMods(dec, t)
					out = append(out, ApplyColorMods(v, mods))
					continue
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return out
}

// drainClrChangeFromTo reads <a:clrFrom> and <a:clrTo> nested colors.
// Returns [from, to] hex strings.
func drainClrChangeFromTo(dec *xml.Decoder, start xml.StartElement, theme Theme) [2]string {
	var out [2]string
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "clrFrom":
				out[0] = ScanColor(dec, t, theme)
			case "clrTo":
				out[1] = ScanColor(dec, t, theme)
			default:
				depth++
			}
		case xml.EndElement:
			depth--
		}
	}
	return out
}
