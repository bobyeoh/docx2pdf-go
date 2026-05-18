package render

import (
	"fmt"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

const (
	defaultFamily   = "doc"
	boldFamily      = "doc-b"
	italicFamily    = "doc-i"
	headingFamily   = "doc-h"
	fallbackFamily  = "doc-fb"
	symbolFamily    = "doc-sym"
	embFamilyPrefix = "emb-"
)

// applyEmbeddedDocFonts substitutes sentinel paths into the relevant
// Options.Font* fields whenever the document ships a matching
// w:embed{Regular,Bold,Italic,BoldItalic} in word/fontTable.xml. The
// returned map populates renderer.embeddedFontData so loadFont can
// resolve the sentinels back to bytes.
//
// Resolution rules (caller-set paths always win — embedded fonts only
// fill empty slots):
//
//  1. The doc's body face (theme minorAscii > Defaults.FontFamily)
//     supplies FontRegular / FontBold / FontItalic from its
//     EmbeddedFontSet.
//  2. The doc's heading face (theme majorAscii) supplies FontHeading.
//
// Per-variant: if the byte slice for that variant is nil (because the
// fontTable didn't include it), the slot is left untouched — so e.g.
// a font that only embeds Regular still gets system-font bold via the
// usual resolution chain.
func applyEmbeddedDocFonts(opts *Options, doc *docx.Document) map[string][]byte {
	if doc == nil || len(doc.EmbeddedFonts) == 0 {
		return nil
	}
	data := map[string][]byte{}

	bind := func(slot *string, name, variant string, bytes []byte) {
		if *slot != "" || len(bytes) == 0 || name == "" {
			return
		}
		sentinel := embeddedDocFontSentinel(name, variant)
		data[sentinel] = bytes
		*slot = sentinel
	}

	bodyName := primaryFontName(doc, "minorAscii")
	if set, ok := doc.EmbeddedFonts[strings.ToLower(bodyName)]; ok && bodyName != "" {
		bind(&opts.FontRegular, bodyName, "Regular", set.Regular)
		bind(&opts.FontBold, bodyName, "Bold", set.Bold)
		bind(&opts.FontItalic, bodyName, "Italic", set.Italic)
	}

	headingName := primaryFontName(doc, "majorAscii")
	if set, ok := doc.EmbeddedFonts[strings.ToLower(headingName)]; ok && headingName != "" {
		bind(&opts.FontHeading, headingName, "Regular", set.Regular)
	}

	if len(data) == 0 {
		return nil
	}
	return data
}

// primaryFontName returns the doc's preferred font name for the given
// theme role ("minorAscii" for body text, "majorAscii" for headings).
// Falls back to doc.Defaults.FontFamily when the theme is missing —
// older docs without theme1.xml still declare a body font via the
// docDefaults block.
func primaryFontName(doc *docx.Document, themeRole string) string {
	if doc == nil {
		return ""
	}
	if doc.Theme.Fonts != nil {
		if name := doc.Theme.Fonts[themeRole]; name != "" {
			return name
		}
	}
	if themeRole == "minorAscii" {
		return doc.Defaults.FontFamily
	}
	return ""
}

func (r *renderer) registerFonts() error {
	// FontRegular is required — its load error is fatal.
	if err := r.loadFont(defaultFamily, r.opts.FontRegular); err != nil {
		return fmt.Errorf("load font %s: %w", r.opts.FontRegular, err)
	}
	r.fonts[defaultFamily] = true
	// Optional faces: failures are reported via Logger / Verbose but
	// don't abort the render. The renderer just falls back to the
	// regular face for the missing variant.
	r.tryLoadOptionalFont(boldFamily, r.opts.FontBold)
	r.tryLoadOptionalFont(italicFamily, r.opts.FontItalic)
	r.tryLoadOptionalFont(headingFamily, r.opts.FontHeading)
	r.tryLoadOptionalFont(fallbackFamily, r.opts.FontFallback)
	r.tryLoadOptionalFont(symbolFamily, r.opts.FontSymbol)
	r.registerEmbeddedFonts()
	return nil
}

// loadFont handles plain TTF files, TrueType Collections, and the
// embedded-Go-font sentinel.
//
//   - embeddedFontSentinel ("<embedded:goregular>"): load the bundled
//     Go font bytes via AddTTFFontData. Used as the final fallback so
//     the renderer always has *something* to draw with.
//   - TTC (file with "ttcf" header): gopdf's AddTTFFont rejects these
//     because it parses the first 4 bytes as an sfnt version. We
//     extract face 0 ourselves and feed the result to AddTTFFontData.
//   - Anything else: plain TTF, hand off to AddTTFFont directly.
//
// Errors are returned wrapped with the path so callers can see exactly
// which face failed.
func (r *renderer) loadFont(family, path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	if path == embeddedFontSentinel {
		return r.pdf.AddTTFFontData(family, embeddedRegularFont)
	}
	// Doc-embedded font (word/fontTable.xml + word/fonts/*). The
	// sentinel format is "<embedded-doc:<fontname>:<variant>>".
	if data, ok := r.embeddedFontData[path]; ok {
		return r.pdf.AddTTFFontData(family, data)
	}
	if looksLikeTTC(path) {
		data, err := extractTTCFace0(path)
		if err != nil {
			return fmt.Errorf("extract ttc face 0: %w", err)
		}
		return r.pdf.AddTTFFontData(family, data)
	}
	return r.pdf.AddTTFFont(family, path)
}

// tryLoadOptionalFont attempts to load an optional face (bold/italic/
// heading/fallback). Empty path → no-op. Real load failures are logged
// via the renderer's logger so users see why their CJK fallback or
// bold variant isn't taking effect, but do NOT abort the render.
func (r *renderer) tryLoadOptionalFont(family, path string) {
	if path == "" {
		return
	}
	if err := r.loadFont(family, path); err != nil {
		// Mirror RenderWriter's logger selection logic.
		log := r.opts.Logger
		if log == nil && r.opts.Verbose {
			log = func(s string) { fmt.Println(s) }
		}
		if log != nil {
			log(fmt.Sprintf("font: skip %s (%s): %v", family, path, err))
		}
		return
	}
	r.fonts[family] = true
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
// bold/italic variants when available. Embedded fonts take priority.
func (r *renderer) selectFont(p docx.RunProps) string {
	if fam := r.resolveEmbedded(p); fam != "" {
		return fam
	}
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

// registerEmbeddedFonts iterates deobfuscated fonts from fontTable.xml
// and registers each variant with gopdf.
func (r *renderer) registerEmbeddedFonts() {
	if len(r.doc.EmbeddedFonts) == 0 {
		return
	}
	if r.embeddedFamilies == nil {
		r.embeddedFamilies = map[string]embeddedFamily{}
	}
	log := r.opts.Logger
	if log == nil && r.opts.Verbose {
		log = func(s string) { fmt.Println(s) }
	}
	if log == nil {
		log = func(string) {}
	}
	idx := 0
	names := make([]string, 0, len(r.doc.EmbeddedFonts))
	for k := range r.doc.EmbeddedFonts {
		names = append(names, k)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	for _, name := range names {
		ef := r.doc.EmbeddedFonts[name]
		fam := embeddedFamily{}
		regular := fmt.Sprintf("%s%d", embFamilyPrefix, idx)
		bold := regular + "-b"
		italic := regular + "-i"
		boldItalic := regular + "-bi"
		idx++
		if ef.Regular != nil {
			if err := r.pdf.AddTTFFontData(regular, ef.Regular); err == nil {
				r.fonts[regular] = true
				fam.regular = regular
			} else {
				log(fmt.Sprintf("font: embedded %s regular load: %v", name, err))
			}
		}
		if ef.Bold != nil {
			if err := r.pdf.AddTTFFontData(bold, ef.Bold); err == nil {
				r.fonts[bold] = true
				fam.bold = bold
			} else {
				log(fmt.Sprintf("font: embedded %s bold load: %v", name, err))
			}
		}
		if ef.Italic != nil {
			if err := r.pdf.AddTTFFontData(italic, ef.Italic); err == nil {
				r.fonts[italic] = true
				fam.italic = italic
			} else {
				log(fmt.Sprintf("font: embedded %s italic load: %v", name, err))
			}
		}
		if ef.BoldItalic != nil {
			if err := r.pdf.AddTTFFontData(boldItalic, ef.BoldItalic); err == nil {
				r.fonts[boldItalic] = true
				fam.boldItalic = boldItalic
			} else {
				log(fmt.Sprintf("font: embedded %s bold-italic load: %v", name, err))
			}
		}
		if fam.regular == "" && fam.bold == "" && fam.italic == "" && fam.boldItalic == "" {
			continue
		}
		r.embeddedFamilies[name] = fam
		r.embeddedFamilies[strings.ToLower(name)] = fam
	}
}

type embeddedFamily struct {
	regular, bold, italic, boldItalic string
}

// resolveEmbedded looks up the family id appropriate for the run's
// bold/italic combination. Returns "" when no embedded match.
func (r *renderer) resolveEmbedded(p docx.RunProps) string {
	if len(r.embeddedFamilies) == 0 || p.FontFamily == "" {
		return ""
	}
	fam, ok := r.embeddedFamilies[p.FontFamily]
	if !ok {
		fam, ok = r.embeddedFamilies[strings.ToLower(p.FontFamily)]
	}
	if !ok {
		return ""
	}
	switch {
	case p.Bold && p.Italic:
		if fam.boldItalic != "" {
			return fam.boldItalic
		}
		if fam.bold != "" {
			return fam.bold
		}
		if fam.italic != "" {
			return fam.italic
		}
	case p.Bold:
		if fam.bold != "" {
			return fam.bold
		}
	case p.Italic:
		if fam.italic != "" {
			return fam.italic
		}
	}
	return fam.regular
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
	// w:fitText horizontal squeeze — pre-computed at cell entry. We shrink
	// the font size uniformly so the entire cell content stays within its
	// column width. gopdf has no Tz (horizontal-scale) operator, so this
	// is an isotropic approximation rather than the asymmetric squash
	// Word performs; readable, just not pixel-faithful.
	if r.fitTextScale > 0 && r.fitTextScale != 1.0 {
		size *= r.fitTextScale
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

// applyLumModOff implements Word's HSL luminance adjustments per the
// OOXML spec: L' = L * lumMod + lumOff. The transform is performed in
// HSL space (not RGB) so saturated accent colors shift correctly.
//
//	lumMod = 0 OR 1 → no luminance scaling
//	lumOff > 0      → brighten by adding to L (clamped to 1)
//	lumOff < 0      → darken (rare; spec allows negative)
//
// Special cases:
//	If both are zero, return the hex unchanged.
//	Pure grayscale colors (S = 0) keep saturation 0; only L moves.
func applyLumModOff(hex string, lumMod, lumOff float64) string {
	return applyColorMods(hex, lumMod, lumOff, 0, 0)
}

// applyColorMods extends applyLumModOff with saturation modulation
// (satMod / satOff), per ECMA-376 §17.18.99. Order is L-then-S so the
// L derivation matches Word; both transforms run in HSL space.
func applyColorMods(hex string, lumMod, lumOff, satMod, satOff float64) string {
	if lumMod == 0 && lumOff == 0 && satMod == 0 && satOff == 0 {
		return hex
	}
	r, g, b := parseHexColor(hex)
	h, s, l := rgbToHSL(r, g, b)
	if lumMod != 0 && lumMod != 1 {
		l *= lumMod
	}
	if lumOff > 0 {
		l = l + (1-l)*lumOff
	} else if lumOff < 0 {
		l = l + l*lumOff
	}
	if satMod != 0 && satMod != 1 {
		s *= satMod
	}
	if satOff > 0 {
		s = s + (1-s)*satOff
	} else if satOff < 0 {
		s = s + s*satOff
	}
	if l < 0 {
		l = 0
	}
	if l > 1 {
		l = 1
	}
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	rr, gg, bb := hslToRGB(h, s, l)
	return fmt.Sprintf("%02X%02X%02X", rr, gg, bb)
}

// rgbToHSL converts 0..255 RGB to HSL in [0,1] for H/S/L.
func rgbToHSL(r, g, b uint8) (h, s, l float64) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0
	maxV := rf
	if gf > maxV {
		maxV = gf
	}
	if bf > maxV {
		maxV = bf
	}
	minV := rf
	if gf < minV {
		minV = gf
	}
	if bf < minV {
		minV = bf
	}
	l = (maxV + minV) / 2
	if maxV == minV {
		return 0, 0, l
	}
	d := maxV - minV
	if l > 0.5 {
		s = d / (2 - maxV - minV)
	} else {
		s = d / (maxV + minV)
	}
	switch maxV {
	case rf:
		h = (gf - bf) / d
		if gf < bf {
			h += 6
		}
	case gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h /= 6
	return h, s, l
}

// hslToRGB inverts rgbToHSL.
func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	if s == 0 {
		v := uint8(l*255 + 0.5)
		return v, v, v
	}
	q := l + s - l*s
	if l < 0.5 {
		q = l * (1 + s)
	}
	p := 2*l - q
	hue2rgb := func(t float64) float64 {
		if t < 0 {
			t += 1
		}
		if t > 1 {
			t -= 1
		}
		if t < 1.0/6.0 {
			return p + (q-p)*6*t
		}
		if t < 0.5 {
			return q
		}
		if t < 2.0/3.0 {
			return p + (q-p)*(2.0/3.0-t)*6
		}
		return p
	}
	r := hue2rgb(h + 1.0/3.0)
	g := hue2rgb(h)
	b := hue2rgb(h - 1.0/3.0)
	clamp := func(v float64) uint8 {
		v = v*255 + 0.5
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return clamp(r), clamp(g), clamp(b)
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

// isRTL reports whether a rune belongs to a right-to-left script
// (Hebrew, Arabic, Syriac, Thaana, N'Ko, Samaritan, Mandaic, plus the
// Arabic Presentation Forms used by some legacy encoders). Digits and
// punctuation are deliberately excluded — they keep their LTR direction
// even inside RTL paragraphs in this MVP.
func isRTL(r rune) bool {
	switch {
	case r >= 0x0590 && r <= 0x05FF: // Hebrew
		return true
	case r >= 0x0600 && r <= 0x06FF: // Arabic
		// Arabic block contains digits at 0x0660-0x0669 and 0x06F0-0x06F9
		// — treat those as neutral so they keep LTR direction.
		if (r >= 0x0660 && r <= 0x0669) || (r >= 0x06F0 && r <= 0x06F9) {
			return false
		}
		return true
	case r >= 0x0700 && r <= 0x074F: // Syriac
		return true
	case r >= 0x0780 && r <= 0x07BF: // Thaana
		return true
	case r >= 0x07C0 && r <= 0x07FF: // N'Ko
		return true
	case r >= 0x0800 && r <= 0x083F: // Samaritan
		return true
	case r >= 0x0840 && r <= 0x085F: // Mandaic
		return true
	case r >= 0xFB1D && r <= 0xFDFF: // Hebrew/Arabic Presentation Forms-A
		return true
	case r >= 0xFE70 && r <= 0xFEFF: // Arabic Presentation Forms-B
		return true
	}
	return false
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
// taking CJK fallback into account. Symbol-block runes (Dingbats,
// Misc Symbols, Geometric Shapes, arrows, math operators) also route
// to the fallback font when registered — Latin TTFs frequently omit
// these (notably Arial on macOS lacks U+2713 CHECK MARK), and any
// real CJK fallback covers them.
func (r *renderer) chooseFamily(rn rune, p docx.RunProps) string {
	// Symbol-block runes (ballot box, arrows, dingbats, ...) prefer the
	// dedicated symbol face when one is registered. CJK fonts often skip
	// these blocks (WQY Zen Hei lacks U+2610 / U+2612, for example), so
	// without a symbol font the glyph would render as a missing-GID box.
	if isSymbolGlyph(rn) {
		if r.fonts[symbolFamily] {
			return symbolFamily
		}
		if r.fonts[fallbackFamily] {
			return fallbackFamily
		}
	}
	if r.fonts[fallbackFamily] && isCJK(rn) {
		return fallbackFamily
	}
	return r.selectFont(p)
}

// isSymbolGlyph reports whether a rune sits in one of the Unicode
// blocks that Latin-only TTF fonts commonly omit but most CJK fonts
// cover: Arrows, Math Operators, Miscellaneous Technical, Box Drawing,
// Geometric Shapes, Miscellaneous Symbols, and Dingbats. Routing these
// to the fallback face avoids "missing checkmark" / "missing arrow"
// rendering when the regular font doesn't cover them.
func isSymbolGlyph(r rune) bool {
	switch {
	case r >= 0x2190 && r <= 0x21FF: // Arrows
		return true
	case r >= 0x2200 && r <= 0x22FF: // Mathematical Operators
		return true
	case r >= 0x2300 && r <= 0x23FF: // Miscellaneous Technical
		return true
	case r >= 0x2500 && r <= 0x257F: // Box Drawing
		return true
	case r >= 0x2580 && r <= 0x259F: // Block Elements
		return true
	case r >= 0x25A0 && r <= 0x25FF: // Geometric Shapes
		return true
	case r >= 0x2600 && r <= 0x26FF: // Miscellaneous Symbols
		return true
	case r >= 0x2700 && r <= 0x27BF: // Dingbats — includes ✓ ✗ ✘ ✦
		return true
	}
	return false
}
