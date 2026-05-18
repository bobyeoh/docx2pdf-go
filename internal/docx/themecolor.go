package docx

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// ColorMods captures the DrawingML color modifier chain that can sit under
// any color element (a:srgbClr, a:schemeClr, a:sysClr, a:prstClr).
//
// All transforms are applied in the order: tint/shade → lumMod/lumOff →
// satMod/satOff → alpha. Word's authoring tools rarely combine more than
// two of these on one stop, so order rarely matters in practice.
type ColorMods struct {
	LumMod float64 // [0,1] — scale L toward 0
	LumOff float64 // [0,1] — add to L (clamped at 1)
	SatMod float64 // [0,1] — scale S
	SatOff float64 // [0,1] — add to S
	Tint   float64 // [0,1] — mix toward white
	Shade  float64 // [0,1] — mix toward black
	Alpha  float64 // [0,1] — opacity (1 = opaque). Caller may ignore.
}

// hasAny reports whether any transform is set.
func (m ColorMods) hasAny() bool {
	return m.LumMod != 0 || m.LumOff != 0 ||
		m.SatMod != 0 || m.SatOff != 0 ||
		m.Tint != 0 || m.Shade != 0
}

// ScanColor walks a DrawingML color subtree and returns the resolved 6-hex
// RGB string. start must be a color-bearing element: a:solidFill, a:fgClr,
// a:bgClr, gs (gradient stop), or directly a:srgbClr / a:schemeClr / a:sysClr.
//
// Inside that subtree we accept exactly one color leaf (srgbClr/schemeClr/
// sysClr/prstClr) plus any number of modifier children (lumMod, lumOff,
// satMod, satOff, tint, shade, alpha). Theme color slot resolution uses
// theme.Colors; if the slot is missing we return "" and the caller can
// fall back.
//
// We DO NOT recurse past the first leaf — that matches Word's authoring
// model (each fill carries one color).
func ScanColor(dec *xml.Decoder, start xml.StartElement, theme Theme) string {
	val, mods, ok := scanColorRaw(dec, start, theme)
	if !ok {
		return ""
	}
	return ApplyColorMods(val, mods)
}

// scanColorRaw returns the unmodified base color + modifiers + ok. On
// return the decoder cursor is positioned past the matching EndElement
// of start (matching the original scanSolidFillColor contract).
func scanColorRaw(dec *xml.Decoder, start xml.StartElement, theme Theme) (string, ColorMods, bool) {
	// Some callers hand us the color leaf directly (a:srgbClr / a:schemeClr).
	// Handle that fast-path before descending.
	if base, ok := colorLeafValue(start, theme); ok {
		mods := readColorMods(dec, start)
		return base, mods, true
	}
	var base string
	var mods ColorMods
	found := false
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return base, mods, found
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !found {
				if v, ok := colorLeafValue(t, theme); ok {
					base = v
					found = true
					mods = readColorMods(dec, t)
					// readColorMods consumed the leaf's end — we are still
					// inside the wrapper, keep iterating until its end.
					continue
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return base, mods, found
}

// colorLeafValue returns the base hex for a single color element (srgbClr,
// schemeClr, sysClr, prstClr) — without modifiers. ok=false means se is
// not a color leaf.
func colorLeafValue(se xml.StartElement, theme Theme) (string, bool) {
	switch se.Name.Local {
	case "srgbClr":
		v := strings.TrimPrefix(attr(se, "val"), "#")
		if len(v) == 6 {
			return strings.ToUpper(v), true
		}
	case "schemeClr":
		name := attr(se, "val")
		if v, ok := theme.Colors[mapSchemeName(name)]; ok && len(v) == 6 {
			return strings.ToUpper(v), true
		}
		// Unknown scheme name: caller falls back gracefully.
		return "", true
	case "sysClr":
		// sysClr carries lastClr — the cached resolved value at save time.
		if v := attr(se, "lastClr"); len(v) == 6 {
			return strings.ToUpper(v), true
		}
		return "000000", true
	case "prstClr":
		if v, ok := prstColorRGB(attr(se, "val")); ok {
			return v, true
		}
	}
	return "", false
}

// readColorMods consumes the children of a color leaf element, picking up
// transform attributes (val is in 1000ths of a percent so 50000 = 50%).
// On exit the decoder cursor is past the leaf's EndElement.
func readColorMods(dec *xml.Decoder, start xml.StartElement) ColorMods {
	var m ColorMods
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return m
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			v := parsePercentMod(attr(t, "val"))
			switch t.Name.Local {
			case "lumMod":
				m.LumMod = v
			case "lumOff":
				m.LumOff = v
			case "satMod":
				m.SatMod = v
			case "satOff":
				m.SatOff = v
			case "tint":
				m.Tint = v
			case "shade":
				m.Shade = v
			case "alpha":
				m.Alpha = v
			}
		case xml.EndElement:
			depth--
		}
	}
	return m
}

// parsePercentMod converts a Word percent-mod attribute (50000 = 50%) to
// the float in [0,1]. Returns 0 if v is empty or malformed.
func parsePercentMod(v string) float64 {
	if v == "" {
		return 0
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return x / 100000.0
}

// mapSchemeName normalizes a:schemeClr w:val attribute names to the slot
// names actually stored in Theme.Colors. The OOXML spec uses both bg1/tx1
// and lt1/dk1 — Word writes the lt/dk variants in theme1.xml and may use
// the bg/tx names in body content.
func mapSchemeName(name string) string {
	switch name {
	case "bg1":
		return "lt1"
	case "bg2":
		return "lt2"
	case "tx1":
		return "dk1"
	case "tx2":
		return "dk2"
	}
	return name
}

// ApplyColorMods applies tint/shade/lumMod/lumOff/satMod/satOff in order
// to a 6-hex RGB color. Returns the transformed hex (or the input if all
// mods are zero / on parse failure).
func ApplyColorMods(hex string, m ColorMods) string {
	if !m.hasAny() {
		return strings.ToUpper(strings.TrimPrefix(hex, "#"))
	}
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return hex
	}
	x, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return hex
	}
	r := float64((x>>16)&0xff) / 255.0
	g := float64((x>>8)&0xff) / 255.0
	b := float64(x&0xff) / 255.0

	// Tint: mix toward white by amount (1 - tint isn't applied; OOXML
	// defines tint val=50% as "luminance bumped by half toward white").
	if m.Tint > 0 {
		r += (1 - r) * m.Tint
		g += (1 - g) * m.Tint
		b += (1 - b) * m.Tint
	}
	if m.Shade > 0 {
		// Shade val=50% means "luminance scaled by 50%" — drift toward black.
		r *= (1 - m.Shade)
		g *= (1 - m.Shade)
		b *= (1 - m.Shade)
	}

	if m.LumMod != 0 || m.LumOff != 0 || m.SatMod != 0 || m.SatOff != 0 {
		h, s, l := rgb2hsl(r, g, b)
		if m.LumMod > 0 {
			l *= m.LumMod
		}
		if m.LumOff > 0 {
			l += m.LumOff
		}
		if m.SatMod > 0 {
			s *= m.SatMod
		}
		if m.SatOff > 0 {
			s += m.SatOff
		}
		if l > 1 {
			l = 1
		}
		if l < 0 {
			l = 0
		}
		if s > 1 {
			s = 1
		}
		if s < 0 {
			s = 0
		}
		r, g, b = hsl2rgb(h, s, l)
	}

	return fmt.Sprintf("%02X%02X%02X", clamp255(r), clamp255(g), clamp255(b))
}

func clamp255(v float64) int {
	v *= 255
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v + 0.5)
}

// rgb2hsl converts 0..1 RGB to HSL. Standard formula.
func rgb2hsl(r, g, b float64) (h, s, l float64) {
	max, min := r, r
	if g > max {
		max = g
	}
	if b > max {
		max = b
	}
	if g < min {
		min = g
	}
	if b < min {
		min = b
	}
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h /= 6
	return
}

func hsl2rgb(h, s, l float64) (r, g, b float64) {
	if s == 0 {
		return l, l, l
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return hue2rgb(p, q, h+1.0/3.0), hue2rgb(p, q, h), hue2rgb(p, q, h-1.0/3.0)
}

func hue2rgb(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

// prstColorRGB returns the RGB for a:prstClr val="black|white|red|…".
// Only the most common names are mapped; unknown names → (?, false).
func prstColorRGB(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "black":
		return "000000", true
	case "white":
		return "FFFFFF", true
	case "red":
		return "FF0000", true
	case "green":
		return "00FF00", true
	case "blue":
		return "0000FF", true
	case "yellow":
		return "FFFF00", true
	case "cyan":
		return "00FFFF", true
	case "magenta":
		return "FF00FF", true
	case "gray", "grey":
		return "808080", true
	case "lightgray", "lightgrey":
		return "D3D3D3", true
	case "darkgray", "darkgrey":
		return "404040", true
	case "orange":
		return "FFA500", true
	case "purple":
		return "800080", true
	case "brown":
		return "A52A2A", true
	case "pink":
		return "FFC0CB", true
	}
	return "", false
}
