package render

import (
	"os"
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

// TestFindSystemFont confirms that on a typical dev host (macOS or
// Linux with default font packages), findSystemFont returns a real
// path. Skipped on hosts where none of the candidates exist so the
// test still passes inside a stripped container.
func TestFindSystemFont(t *testing.T) {
	got := findSystemFont()
	if got == "" {
		t.Skip("no system font found on this host — skipping (this is OK on minimal containers)")
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("findSystemFont returned %q but the file doesn't exist: %v", got, err)
	}
}
