package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSystemFontCandidates exercises the candidate list shape: it must
// be non-empty, each entry must be an absolute path, and the formatter
// must produce a comma-separated representation that contains every
// candidate.
func TestSystemFontCandidates(t *testing.T) {
	candidates := systemFontCandidates()
	if len(candidates) == 0 {
		t.Fatal("systemFontCandidates returned empty list")
	}
	for _, p := range candidates {
		// Accept either Unix-style /usr/... or Windows-style C:\… paths.
		if !strings.HasPrefix(p, "/") && !(len(p) >= 3 && p[1] == ':') {
			t.Errorf("candidate %q is not an absolute path", p)
		}
	}
	formatted := formatFontCandidates()
	for _, p := range candidates {
		if !strings.Contains(formatted, p) {
			t.Errorf("formatFontCandidates() = %q, missing candidate %q",
				formatted, p)
		}
	}
}

// TestFindSystemFont confirms findSystemFont always returns something
// usable — either a real path on a host with fonts installed, or the
// embedded-font sentinel on a stripped container. Empty result would
// mean the embedded fallback failed to wire up.
func TestFindSystemFont(t *testing.T) {
	got := findSystemFont()
	if got == "" {
		t.Fatal("findSystemFont returned empty — embedded fallback not wired up")
	}
	if got == embeddedFontSentinel {
		// On a fontless host, the sentinel is the correct answer.
		// embeddedRegularFont must be non-empty for the sentinel path
		// to produce a working font in loadFont.
		if len(embeddedRegularFont) == 0 {
			t.Error("embeddedRegularFont is empty — sentinel would fail at load time")
		}
		return
	}
	// Otherwise findSystemFont must have returned a real file.
	if _, err := os.Stat(got); err != nil {
		t.Errorf("findSystemFont returned %q but the file doesn't exist: %v", got, err)
	}
}

// TestEmbeddedFontRenders verifies the embedded font actually works
// end-to-end through gopdf — protects against the goregular.TTF
// dependency drifting in a future Go release.
func TestEmbeddedFontRenders(t *testing.T) {
	if len(embeddedRegularFont) == 0 {
		t.Fatal("embeddedRegularFont is empty")
	}
	// Reasonable size check: Go font is ~150KB, never zero, never
	// huge.
	if n := len(embeddedRegularFont); n < 10_000 || n > 5_000_000 {
		t.Errorf("embeddedRegularFont size = %d, sanity range [10KB, 5MB] failed", n)
	}
}

// TestResolveFontFromEnv covers three cases: unset env var → empty,
// env var set to a non-existent path → empty (stale env var must NOT
// propagate to gopdf as a broken path), env var set to an actual file
// → that file. We use t.Setenv so the change is auto-reverted.
func TestResolveFontFromEnv(t *testing.T) {
	const name = "TEST_DOCX2PDF_FONT_RESOLVE"

	// 1. unset
	t.Setenv(name, "")
	if got := resolveFontFromEnv(name); got != "" {
		t.Errorf("unset env: got %q, want empty", got)
	}

	// 2. set to a missing path
	t.Setenv(name, "/clearly/missing/path/font.ttf")
	if got := resolveFontFromEnv(name); got != "" {
		t.Errorf("stale env (missing file): got %q, want empty", got)
	}

	// 3. set to a real file (use the test binary itself — exists, any
	// content is fine since resolveFontFromEnv only checks existence).
	tmp, err := os.CreateTemp(t.TempDir(), "fontlike-*.ttf")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	t.Setenv(name, tmp.Name())
	if got := resolveFontFromEnv(name); got != tmp.Name() {
		t.Errorf("existing env: got %q, want %q", got, tmp.Name())
	}
}

// TestSystemCJKFontCandidates mirrors TestSystemFontCandidates: the list
// must be non-empty and every entry must be an absolute path. This is a
// regression guard against an accidental edit dropping all platforms'
// CJK paths — without at least one valid entry, findSystemCJKFont
// quietly returns "" and CJK documents render as boxes.
func TestSystemCJKFontCandidates(t *testing.T) {
	candidates := systemCJKFontCandidates()
	if len(candidates) == 0 {
		t.Fatal("systemCJKFontCandidates returned empty list")
	}
	for _, p := range candidates {
		if !strings.HasPrefix(p, "/") && !(len(p) >= 3 && p[1] == ':') {
			t.Errorf("candidate %q is not an absolute path", p)
		}
	}
}

// TestFindFontInDirs confirms the user-dir Latin scan picks up any
// .ttf or .ttc file. We populate a temp dir with a couple of dummies —
// findFontInDirs doesn't open the files, only inspects names, so empty
// "fonts" are fine.
func TestFindFontInDirs(t *testing.T) {
	dir := t.TempDir()
	// Drop a non-font and a font so we exercise the suffix filter.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "MyFont.ttf")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := findFontInDirs([]string{dir})
	if got != target {
		t.Errorf("findFontInDirs = %q, want %q", got, target)
	}

	// Empty dir → "".
	empty := t.TempDir()
	if got := findFontInDirs([]string{empty}); got != "" {
		t.Errorf("findFontInDirs(empty dir) = %q, want empty", got)
	}

	// Non-existent dir → "".
	if got := findFontInDirs([]string{filepath.Join(dir, "no-such")}); got != "" {
		t.Errorf("findFontInDirs(missing dir) = %q, want empty", got)
	}
}

// TestFindCJKFontInDirs confirms the allowlist scan picks up known CJK
// filenames and ignores unrelated TTFs. Important because the Latin
// variant would happily pick "RandomFont.ttf" — that's safe for Latin
// but would be a wrong CJK pick.
func TestFindCJKFontInDirs(t *testing.T) {
	dir := t.TempDir()
	// A non-CJK TTF that should NOT be returned.
	if err := os.WriteFile(filepath.Join(dir, "RandomFont.ttf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A known CJK filename that SHOULD be returned. Case-insensitive
	// match so we exercise the lowercase normalization too.
	target := filepath.Join(dir, "WQY-Zenhei.ttc")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := findCJKFontInDirs([]string{dir})
	if got != target {
		t.Errorf("findCJKFontInDirs = %q, want %q", got, target)
	}

	// Dir with only the non-CJK font → "".
	onlyLatin := t.TempDir()
	if err := os.WriteFile(filepath.Join(onlyLatin, "RandomFont.ttf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findCJKFontInDirs([]string{onlyLatin}); got != "" {
		t.Errorf("findCJKFontInDirs(no CJK) = %q, want empty", got)
	}
}

// TestUserFontDirs is mostly a smoke test: we can't assert what's
// installed on the test host, but the function must never panic and
// must only return existing directories.
func TestUserFontDirs(t *testing.T) {
	for _, d := range userFontDirs() {
		fi, err := os.Stat(d)
		if err != nil {
			t.Errorf("userFontDirs returned %q which doesn't stat: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("userFontDirs returned %q which is not a directory", d)
		}
	}
}
