package render

import (
	"os"
	"strings"

	"golang.org/x/image/font/gofont/goregular"
)

// Env-var names honored when Options.FontRegular / FontFallback are
// empty. Set by the Docker image so containerized callers get sensible
// defaults without having to pass -font flags.
const (
	envFontRegular  = "DOCX2PDF_FONT"
	envFontFallback = "DOCX2PDF_FONT_CJK"
)

// embeddedRegularFont is the MIT-licensed Go font (~150KB, Latin only).
// It is the final fallback when no env var, system font, or caller-
// supplied TTF is available — guarantees the binary can always render
// Latin text even in scratch / distroless / fontless containers.
//
// Exported via a private sentinel rather than a path: the caller's
// loadFont path checks for `embeddedFontSentinel` and routes the bytes
// through AddTTFFontData instead of stat'ing the filesystem.
var embeddedRegularFont = goregular.TTF

// embeddedFontSentinel is the magic path string returned by
// findSystemFont when nothing else matches. loadFont recognizes it and
// uses embeddedRegularFont instead of touching the filesystem.
const embeddedFontSentinel = "<embedded:goregular>"

// systemFontCandidates returns paths to TTF/TTC fonts that commonly exist
// on the major platforms. Used as the fallback when a caller doesn't
// supply Options.FontRegular. The list is intentionally biased toward
// sans-serif Latin faces — gopdf needs a real TTF/TTC, and these are
// the ones most likely to be present without extra installation.
//
// Listed in priority order: the first existing path wins. Exposed
// (lowercase only — package-internal) so the unit test can render its
// expected message when nothing is found.
func systemFontCandidates() []string {
	return []string{
		// macOS — supplemental dir holds the Office-bundled face on
		// newer releases; older releases keep them in /Library/Fonts.
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/Library/Fonts/Arial.ttf",
		"/System/Library/Fonts/Helvetica.ttc",
		"/System/Library/Fonts/Geneva.ttf",

		// Linux — major distros' default sans-serif locations.
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/noto/NotoSans-Regular.ttf",

		// Windows — useful when cross-compiling or running under WSL
		// with the host /mnt/c mount.
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
	}
}

// findSystemFont returns the first existing path from systemFontCandidates,
// or the embeddedFontSentinel as a final fallback. Never returns "" —
// the embedded Go font is always available, so the renderer will always
// have *something* to draw Latin glyphs with even in scratch /
// distroless / fontless containers. Pure I/O — no caching, since font
// installation between Convert calls is implausible and a cache would
// just hide test fixtures.
func findSystemFont() string {
	for _, p := range systemFontCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return embeddedFontSentinel
}

// resolveFontFromEnv reads the named environment variable; returns the
// path only when the file actually exists. A stale env var pointing at
// a missing file is treated as unset rather than letting AddTTFFont
// fail with a less informative error later.
func resolveFontFromEnv(name string) string {
	p := strings.TrimSpace(os.Getenv(name))
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// formatFontCandidates is the joined-with-commas form used in error
// messages so the caller sees what we tried.
func formatFontCandidates() string {
	return strings.Join(systemFontCandidates(), ", ")
}
