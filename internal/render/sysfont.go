package render

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font/gofont/goregular"
)

// Env-var names honored when Options.FontRegular / FontFallback are
// empty. Set by the Docker image so containerized callers get sensible
// defaults without having to pass -font flags.
const (
	envFontRegular  = "DOCX2PDF_FONT"
	envFontFallback = "DOCX2PDF_FONT_CJK"
	envFontSymbol   = "DOCX2PDF_FONT_SYMBOL"
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
		"/System/Library/Fonts/Supplemental/Verdana.ttf",
		"/System/Library/Fonts/Supplemental/Tahoma.ttf",
		"/System/Library/Fonts/Helvetica.ttc",
		"/System/Library/Fonts/Geneva.ttf",
		"/System/Library/Fonts/HelveticaNeue.ttc",

		// Linux — major distros' default sans-serif locations. Cover
		// Debian/Ubuntu (truetype/<family>/), Fedora/RHEL (<family>/),
		// Arch (TTF/), and the gnu-freefont package present on minimal
		// images.
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/liberation/LiberationSans-Regular.ttf",
		"/usr/share/fonts/liberation-sans/LiberationSans-Regular.ttf",
		"/usr/share/fonts/TTF/LiberationSans-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/freefont/FreeSans.ttf",
		"/usr/share/fonts/gnu-free/FreeSans.ttf",
		"/usr/share/fonts/truetype/ubuntu/Ubuntu-R.ttf",
		"/usr/share/fonts/ubuntu/Ubuntu-R.ttf",
		// Homebrew / MacPorts on macOS, common /usr/local mirror on Linux.
		"/usr/local/share/fonts/DejaVuSans.ttf",
		"/opt/homebrew/share/fonts/DejaVuSans.ttf",

		// Windows — useful when cross-compiling or running under WSL
		// with the host /mnt/c mount.
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
		`C:\Windows\Fonts\tahoma.ttf`,
		`C:\Windows\Fonts\verdana.ttf`,
		`C:\Windows\Fonts\times.ttf`,
	}
}

// findSystemFont returns the first existing path from systemFontCandidates,
// then falls back to scanning user-level font dirs (~/Library/Fonts,
// ~/.fonts, ~/.local/share/fonts) for any TrueType file, and finally
// to the embeddedFontSentinel. Never returns "" — the embedded Go font
// is always available, so the renderer always has *something* to draw
// Latin glyphs with even in scratch / distroless / fontless containers.
//
// User-dir scan is Latin-only: any .ttf/.ttc matches without coverage
// inspection. That's safe here because the worst-case is "user dropped
// a CJK-only TTF into ~/.fonts/ and we pick it for Latin text" — the
// font still renders Latin glyphs, just not aesthetically what the
// caller might expect. For CJK we keep the curated list (see
// findSystemCJKFont) to avoid picking a font that gopdf can't decode.
func findSystemFont() string {
	for _, p := range systemFontCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p := findFontInDirs(userFontDirs()); p != "" {
		return p
	}
	return embeddedFontSentinel
}

// systemCJKFontCandidates returns paths to TTF/TTC fonts that cover
// CJK glyph blocks AND the common symbol/dingbat ranges (✓ ✗ → etc.)
// that Latin fonts often omit. Used as the auto-fallback when
// Options.FontFallback is empty.
//
// Priority order favors TrueType-outline fonts (which gopdf can render
// directly). CFF/OpenType-outline fonts like PingFang, Noto CJK, and
// Hiragino are listed last and only work if the runtime's TTC face-0
// extractor finds a TrueType face in the collection.
func systemCJKFontCandidates() []string {
	return []string{
		// macOS — Arial Unicode is TrueType and covers basically every
		// BMP glyph including CJK + Dingbats. 23 MB but already on
		// every macOS install.
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/Library/Fonts/Arial Unicode.ttf",
		// Simplified Chinese (TrueType, ship with every modern macOS).
		"/System/Library/Fonts/STHeiti Light.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
		"/System/Library/Fonts/Supplemental/Songti.ttc",
		"/Library/Fonts/Songti.ttc",
		// Korean (Hangul) — TrueType.
		"/System/Library/Fonts/Supplemental/AppleGothic.ttf",
		"/System/Library/Fonts/Supplemental/AppleMyungjo.ttf",

		// Linux — WQY Zen Hei / Micro Hei are TrueType and broadly
		// packaged. ArPhic uming/ukai cover Traditional + Simplified.
		// Droid Sans Fallback ships TrueType and covers all CJK ranges.
		"/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/wqy-microhei/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/arphic/uming.ttc",
		"/usr/share/fonts/truetype/arphic/uming.ttc",
		"/usr/share/fonts/arphic/ukai.ttc",
		"/usr/share/fonts/truetype/arphic/ukai.ttc",
		"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		"/usr/share/fonts/google-droid/DroidSansFallbackFull.ttf",
		// Japanese — Takao / Sazanami are TrueType forks of IPA.
		"/usr/share/fonts/truetype/takao-gothic/TakaoGothic.ttf",
		"/usr/share/fonts/takao-gothic/TakaoGothic.ttf",
		"/usr/share/fonts/truetype/sazanami/sazanami-gothic.ttf",
		"/usr/share/fonts/sazanami/sazanami-gothic.ttf",
		// Korean — UnDotum is TrueType, packaged as un-fonts-core.
		"/usr/share/fonts/truetype/unfonts-core/UnDotum.ttf",
		"/usr/share/fonts/un-core/UnDotum.ttf",

		// Windows — TrueType faces, listed in priority order:
		// Simplified Chinese first (msyh / SimSun / SimHei), then
		// Traditional (msjh / mingliu), then Japanese (msgothic /
		// meiryo / Yu Gothic), then Korean (malgun / gulim / batang).
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simsun.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\simkai.ttf`,
		`C:\Windows\Fonts\simfang.ttf`,
		`C:\Windows\Fonts\msjh.ttc`,
		`C:\Windows\Fonts\mingliu.ttc`,
		`C:\Windows\Fonts\msgothic.ttc`,
		`C:\Windows\Fonts\meiryo.ttc`,
		`C:\Windows\Fonts\YuGothR.ttc`,
		`C:\Windows\Fonts\malgun.ttf`,
		`C:\Windows\Fonts\gulim.ttc`,
		`C:\Windows\Fonts\batang.ttc`,

		// CFF-outline fallbacks (low priority; only work if the TTC
		// has a TrueType face somewhere we can extract — currently
		// extractTTCFace0 only reads face 0, so most of these fail).
		"/System/Library/Fonts/PingFang.ttc",
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	}
}

// systemSymbolFontCandidates returns paths to TTF fonts that specifically
// cover the Unicode symbol blocks Word's checkbox / arrow / dingbat
// content controls emit (☐ ☒ ☑ ✓ ✗ → ←). Many fonts present in
// distros that include "CJK fallback" coverage skip these blocks
// (WQY Zen Hei is the canonical example), so we look for a dedicated
// symbol face first.
func systemSymbolFontCandidates() []string {
	return []string{
		// Noto Sans Symbols 2 — explicit symbol font, ~600KB, broadly
		// packaged via font-noto on Alpine / RHEL / Debian.
		"/usr/share/fonts/noto/NotoSansSymbols2-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSansSymbols2-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf",
		"/usr/share/fonts/noto/NotoSansSymbols-Regular.ttf",
		"/usr/share/fonts/google-noto/NotoSansSymbols-Regular.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansSymbols-Regular.ttf",
		// DejaVu Sans is widely-installed; it covers ☐ ☒ ☑ at GID
		// positions matching the standard Unicode mapping.
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf",
		// macOS — Apple Symbols covers ballot box and dingbat ranges.
		"/System/Library/Fonts/Apple Symbols.ttf",
		"/Library/Fonts/Apple Symbols.ttf",
		// Windows — Segoe UI Symbol carries the modern symbol set.
		`C:\Windows\Fonts\seguisym.ttf`,
		`C:\Windows\Fonts\segoeuisl.ttf`,
	}
}

// findSystemSymbolFont returns the first existing path from
// systemSymbolFontCandidates, then "". Used when Options.FontSymbol is
// empty and $DOCX2PDF_FONT_SYMBOL is unset.
func findSystemSymbolFont() string {
	for _, p := range systemSymbolFontCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findSystemCJKFont returns the first existing path from
// systemCJKFontCandidates, then falls back to scanning user-level
// font dirs for a filename that matches a known CJK font release.
// Returns "" if nothing matches.
//
// Unlike findSystemFont this does NOT fall back to the embedded Go
// font — the Go font is Latin-only and would not actually serve as a
// CJK fallback. Callers treat "" as "no fallback available".
//
// The user-dir scan uses an allowlist of well-known filenames
// (cjkUserFontFilenames) rather than substring matching — fonts like
// "kaitilike.ttf" or "hei-sans.ttf" could false-match a keyword scan,
// and a wrong pick is silent: the file loads, gopdf renders boxes
// instead of glyphs, and the caller doesn't know why.
func findSystemCJKFont() string {
	for _, p := range systemCJKFontCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p := findCJKFontInDirs(userFontDirs()); p != "" {
		return p
	}
	return ""
}

// userFontDirs returns user-level font directories that exist on the
// current host. All three common paths are probed regardless of
// platform (macOS: ~/Library/Fonts, Linux/BSD: ~/.fonts and
// ~/.local/share/fonts) — non-existent ones are filtered out, so the
// same code works under WSL, Docker mounts, etc. Empty if $HOME
// cannot be resolved.
func userFontDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Library", "Fonts"),
		filepath.Join(home, ".fonts"),
		filepath.Join(home, ".local", "share", "fonts"),
	}
	var out []string
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// findFontInDirs returns the first .ttf or .ttc file found by a
// one-level scan of dirs, or "" if none found. Used as a last-resort
// Latin fallback so user-installed fonts in ~/Library/Fonts / ~/.fonts /
// ~/.local/share/fonts get picked up without explicit configuration.
//
// Any TrueType filename matches — fine for Latin (worst case: a
// user-dropped CJK-only TTF gets selected and still renders Latin
// glyphs), unsafe for CJK (see findCJKFontInDirs for the allowlist
// variant).
func findFontInDirs(dirs []string) string {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if strings.HasSuffix(name, ".ttf") || strings.HasSuffix(name, ".ttc") {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

// cjkUserFontFilenames is the allowlist used when scanning user-level
// font dirs for a CJK fallback. Lower-cased basenames; we only add
// fonts known to be TrueType-outline (so gopdf can load them) and
// known to cover CJK ranges. Substring/keyword matching is avoided —
// "kai" / "hei" / "song" / "ming" are common enough Latin font name
// fragments to produce wrong picks.
var cjkUserFontFilenames = map[string]bool{
	// WenQuanYi (TrueType, MIT)
	"wqy-zenhei.ttc":   true,
	"wqy-microhei.ttc": true,
	"wqy-microhei.ttf": true,
	"wqy-zenhei.ttf":   true,
	// ArPhic (TrueType, Arphic Public License)
	"uming.ttc": true,
	"ukai.ttc":  true,
	// Google/Android (TrueType)
	"droidsansfallback.ttf":     true,
	"droidsansfallbackfull.ttf": true,
	// Microsoft TrueType faces (when user copied them in)
	"msyh.ttc":     true,
	"msyhbd.ttc":   true,
	"msjh.ttc":     true,
	"msgothic.ttc": true,
	"meiryo.ttc":   true,
	"simsun.ttc":   true,
	"simhei.ttf":   true,
	"simkai.ttf":   true,
	"simfang.ttf":  true,
	"mingliu.ttc":  true,
	"yugothr.ttc":  true,
	"malgun.ttf":   true,
	"gulim.ttc":    true,
	"batang.ttc":   true,
	// macOS TrueType faces (when user copied them in)
	"arial unicode.ttf":  true,
	"stheiti light.ttc":  true,
	"stheiti medium.ttc": true,
	"songti.ttc":         true,
	"applegothic.ttf":    true,
	"applemyungjo.ttf":   true,
	// Japanese TrueType
	"takaogothic.ttf":     true,
	"sazanami-gothic.ttf": true,
	// Korean TrueType
	"undotum.ttf": true,
}

// findCJKFontInDirs scans dirs for a file whose lower-cased basename
// is in cjkUserFontFilenames. Returns the first match or "". One-level
// scan only — users typically drop fonts flat into ~/.fonts/.
func findCJKFontInDirs(dirs []string) string {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if cjkUserFontFilenames[strings.ToLower(e.Name())] {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
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

// embeddedDocFontSentinel returns the sentinel string loadFont
// recognizes for a doc-embedded font face. variant is "Regular" /
// "Bold" / "Italic" / "BoldItalic". fontname is matched against the
// (lower-cased) key in doc.EmbeddedFonts.
func embeddedDocFontSentinel(fontname, variant string) string {
	return "<embedded-doc:" + strings.ToLower(fontname) + ":" + variant + ">"
}
