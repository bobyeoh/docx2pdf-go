package render

import (
	"fmt"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

const (
	defaultFamily  = "doc"
	boldFamily     = "doc-b"
	italicFamily   = "doc-i"
	headingFamily  = "doc-h"
	fallbackFamily = "doc-fb"
)

func (r *renderer) registerFonts() error {
	if err := r.pdf.AddTTFFont(defaultFamily, r.opts.FontRegular); err != nil {
		return fmt.Errorf("load font %s: %w", r.opts.FontRegular, err)
	}
	r.fonts[defaultFamily] = true
	if r.opts.FontBold != "" {
		if err := r.pdf.AddTTFFont(boldFamily, r.opts.FontBold); err == nil {
			r.fonts[boldFamily] = true
		}
	}
	if r.opts.FontItalic != "" {
		if err := r.pdf.AddTTFFont(italicFamily, r.opts.FontItalic); err == nil {
			r.fonts[italicFamily] = true
		}
	}
	if r.opts.FontHeading != "" {
		if err := r.pdf.AddTTFFont(headingFamily, r.opts.FontHeading); err == nil {
			r.fonts[headingFamily] = true
		}
	}
	if r.opts.FontFallback != "" {
		if err := r.pdf.AddTTFFont(fallbackFamily, r.opts.FontFallback); err == nil {
			r.fonts[fallbackFamily] = true
		}
	}
	return nil
}

// isMajorThemeRole reports whether the role names a major (i.e. heading)
// theme font slot. Major roles map to FontHeading when available; minor
// roles always fall through to the default body font.
func isMajorThemeRole(role string) bool {
	switch role {
	case "majorAscii", "majorEastAsia":
		return true
	}
	return false
}

// selectFont picks the registered family that should render `p`, honoring
// bold/italic variants when available. A run whose theme font role is
// "major*" (heading) routes to the heading family if FontHeading was
// registered; otherwise it falls through to the regular face — keeping
// behavior unchanged for callers that don't opt into a heading font.
func (r *renderer) selectFont(p docx.RunProps) string {
	if isMajorThemeRole(p.ThemeFontRole) && r.fonts[headingFamily] {
		return headingFamily
	}
	switch {
	case p.Bold && r.fonts[boldFamily]:
		return boldFamily
	case p.Italic && r.fonts[italicFamily]:
		return italicFamily
	}
	return defaultFamily
}

// applyFontFamily activates a specific registered family at the run's size.
// Atoms carry an explicit family when CJK fallback applies, so this lets us
// switch fonts mid-line without consulting selectFont again.
//
// Also installs the run's effective character spacing (w:spacing → letter
// spacing, plus w:w → approximate horizontal scale). Done here so both
// MeasureTextWidth and the subsequent Cell draw see the same value — they
// share the gopdf "current" CharSpacing state, so the measured atom width
// matches what the renderer eventually paints.
func (r *renderer) applyFontFamily(p docx.RunProps, family string) error {
	if family == "" {
		family = r.selectFont(p)
	}
	size := p.FontSize
	if size == 0 {
		size = r.opts.DefaultFontSize
	}
	if p.VertAlign == "superscript" || p.VertAlign == "subscript" {
		size *= 0.6
	}
	if err := r.pdf.SetFont(family, "", size); err != nil {
		return err
	}
	_ = r.pdf.SetCharSpacing(charSpacingFor(p, size))
	color := p.Color
	if color == "" && p.ThemeColor != "" {
		if hex, ok := r.doc.Theme.Colors[p.ThemeColor]; ok {
			color = applyLumModOff(hex, p.LumMod, p.LumOff)
		}
	}
	if color != "" {
		rR, gR, bR := parseHexColor(color)
		r.pdf.SetTextColor(rR, gR, bR)
	} else {
		r.pdf.SetTextColor(0, 0, 0)
	}
	return nil
}

// charSpacingFor returns the effective inter-character spacing in points
// for a run. LetterSpacingPt (w:spacing) adds directly; CharacterScale
// (w:w, 1.0 = 100%) is approximated by spreading the per-glyph delta
// across the run — true Tz horizontal scaling isn't available in gopdf.
// Glyph half-em is a rough enough proxy for "advance per char" without
// pulling font metrics; result reads as condensed/expanded text.
func charSpacingFor(p docx.RunProps, fontSize float64) float64 {
	spacing := p.LetterSpacingPt
	if p.CharacterScale > 0 && p.CharacterScale != 1.0 {
		spacing += (p.CharacterScale - 1) * fontSize * 0.5
	}
	return spacing
}

// applyLumModOff approximates Word's HSL luminance adjustments. lumMod
// scales luminance (we accept 0..1); lumOff brightens toward white.
func applyLumModOff(hex string, lumMod, lumOff float64) string {
	if lumMod == 0 && lumOff == 0 {
		return hex
	}
	r, g, b := parseHexColor(hex)
	rf, gf, bf := float64(r), float64(g), float64(b)
	if lumMod > 0 && lumMod < 1 {
		rf *= lumMod
		gf *= lumMod
		bf *= lumMod
	}
	if lumOff > 0 && lumOff < 1 {
		rf += (255 - rf) * lumOff
		gf += (255 - gf) * lumOff
		bf += (255 - bf) * lumOff
	}
	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return fmt.Sprintf("%02X%02X%02X", clamp(rf), clamp(gf), clamp(bf))
}

// highlightRGB resolves Word's predefined w:highlight names to RGB.
func highlightRGB(name string) (uint8, uint8, uint8, bool) {
	switch name {
	case "yellow":
		return 0xFF, 0xFF, 0x00, true
	case "green":
		return 0x00, 0xFF, 0x00, true
	case "cyan":
		return 0x00, 0xFF, 0xFF, true
	case "magenta":
		return 0xFF, 0x00, 0xFF, true
	case "blue":
		return 0x00, 0x00, 0xFF, true
	case "red":
		return 0xFF, 0x00, 0x00, true
	case "darkBlue":
		return 0x00, 0x00, 0x80, true
	case "darkCyan":
		return 0x00, 0x80, 0x80, true
	case "darkGreen":
		return 0x00, 0x80, 0x00, true
	case "darkMagenta":
		return 0x80, 0x00, 0x80, true
	case "darkRed":
		return 0x80, 0x00, 0x00, true
	case "darkYellow":
		return 0x80, 0x80, 0x00, true
	case "darkGray":
		return 0x80, 0x80, 0x80, true
	case "lightGray":
		return 0xC0, 0xC0, 0xC0, true
	case "black":
		return 0x00, 0x00, 0x00, true
	case "white":
		return 0xFF, 0xFF, 0xFF, true
	}
	return 0, 0, 0, false
}

// runBackgroundRGB returns the background fill color for a run, taking
// highlight first and shading second.
func runBackgroundRGB(p docx.RunProps) (uint8, uint8, uint8, bool) {
	if p.Highlight != "" {
		if r, g, b, ok := highlightRGB(p.Highlight); ok {
			return r, g, b, true
		}
	}
	if p.Shading != "" {
		r, g, b := parseHexColor(p.Shading)
		return r, g, b, true
	}
	return 0, 0, 0, false
}

// applyRunFont is the no-fallback variant used by code paths that don't yet
// know which family an atom needs (e.g. list marker rendering).
func (r *renderer) applyRunFont(p docx.RunProps) error {
	return r.applyFontFamily(p, r.selectFont(p))
}

// isCJK returns true for runes in the main CJK blocks plus common CJK
// punctuation/full-width forms. Used both as a break opportunity and to
// route to the fallback font.
func isCJK(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F:
		return true
	case r >= 0x3040 && r <= 0x309F:
		return true
	case r >= 0x30A0 && r <= 0x30FF:
		return true
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0xFF00 && r <= 0xFFEF:
		return true
	}
	return false
}

// chooseFamily returns the registered family that should render this rune,
// taking CJK fallback into account.
func (r *renderer) chooseFamily(rn rune, p docx.RunProps) string {
	if isCJK(rn) && r.fonts[fallbackFamily] {
		return fallbackFamily
	}
	return r.selectFont(p)
}
