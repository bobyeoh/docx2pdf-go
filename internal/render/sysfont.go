package render

import (
	"os"
	"strings"
)

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
// or "" if none of them exist on the host. Pure I/O — no caching, since
// font installation between Convert calls is implausible and a cache
// would just hide test fixtures.
func findSystemFont() string {
	for _, p := range systemFontCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// formatFontCandidates is the joined-with-commas form used in error
// messages so the caller sees what we tried.
func formatFontCandidates() string {
	return strings.Join(systemFontCandidates(), ", ")
}
