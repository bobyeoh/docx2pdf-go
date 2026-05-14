package verify

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
)

// verifyCase is one feature-level test in the harness.
type verifyCase struct {
	// name is used as the subtest name and the output PNG directory.
	name string
	// description is shown in the HTML report next to the case name.
	description string
	// build produces a docx file under dir and returns its path.
	build func(t *testing.T, dir string) string
	// expectText is a list of substrings that must appear in the PDF's
	// extracted text. Use this to assert content is preserved.
	expectText []string
	// expectPages, when > 0, asserts the PDF has exactly this many pages.
	expectPages int
	// pageNumbers toggles the -page-numbers flag.
	pageNumbers bool
	// useCJK toggles the CJK fallback font (needed for any Chinese case).
	useCJK bool
	// custom is an optional extra assertion run after stock checks. Use this
	// when a case needs geometric/per-page checks pdftotext can't express.
	// Call `fail` to record a failure in both the test and the HTML report.
	custom func(t *testing.T, pdf string, fail func(format string, args ...any))
}

// caseResult is captured during a run for the final HTML report.
type caseResult struct {
	name        string
	description string
	pdfPath     string
	pngPaths    []string
	pages       int
	textSample  string
	failures    []string // human-readable failure messages
}

// requireTool skips the test if the named CLI is missing. We need pdftotext
// and pdftoppm from poppler-utils; install via `brew install poppler` on macOS.
func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found in PATH — install poppler-utils to run the verify suite", name)
	}
}

// pdftotext extracts text from the PDF preserving rough layout. Layout mode
// gives more predictable text order across columns/tables.
func pdftotext(t *testing.T, pdf string) string {
	t.Helper()
	cmd := exec.Command("pdftotext", "-layout", "-enc", "UTF-8", pdf, "-")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("pdftotext: %v\n%s", err, errb.String())
	}
	return out.String()
}

// pdfPageCount returns the page count by parsing pdfinfo output. Cheaper than
// re-rendering the whole PDF just to count pages.
func pdfPageCount(t *testing.T, pdf string) int {
	t.Helper()
	out, err := exec.Command("pdfinfo", pdf).Output()
	if err != nil {
		t.Fatalf("pdfinfo: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			return n
		}
	}
	return 0
}

// pdfPageSize parses `pdfinfo -f N -l N` for a specific page's media box and
// returns (widthPt, heightPt). Used to assert landscape vs portrait in
// multi-section documents — pdftotext is blind to page geometry.
func pdfPageSize(t *testing.T, pdf string, pageNo int) (float64, float64) {
	t.Helper()
	out, err := exec.Command("pdfinfo", "-f", strconv.Itoa(pageNo), "-l", strconv.Itoa(pageNo), pdf).Output()
	if err != nil {
		t.Fatalf("pdfinfo page %d: %v", pageNo, err)
	}
	// Line looks like: "Page    1 size: 595.32 x 841.92 pts (A4)"
	prefix := "Page"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) || !strings.Contains(line, "size:") {
			continue
		}
		i := strings.Index(line, "size:")
		if i < 0 {
			continue
		}
		dims := strings.TrimSpace(line[i+len("size:"):])
		var w, h float64
		_, err := fmt.Sscanf(dims, "%f x %f", &w, &h)
		if err == nil {
			return w, h
		}
	}
	return 0, 0
}

// renderPNG rasterizes the PDF to one PNG per page at 100 DPI into outDir.
// Returns the sorted list of generated PNG paths (relative to outDir).
func renderPNG(t *testing.T, pdf, outDir string) []string {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// pdftoppm writes <prefix>-<n>.png; we point prefix at outDir/page so the
	// files end up named e.g. page-1.png, page-2.png.
	prefix := filepath.Join(outDir, "page")
	cmd := exec.Command("pdftoppm", "-png", "-r", "100", pdf, prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	var pngs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			pngs = append(pngs, e.Name())
		}
	}
	sort.Strings(pngs)
	return pngs
}

// runCase drives one verification case end to end.
//
//	docx (in-memory build) → convert → PDF
//	                           ↓
//	                      pdftotext      → substring asserts
//	                      pdfinfo        → page count assert
//	                      pdftoppm       → PNG snapshots for visual report
func runCase(t *testing.T, c verifyCase, fontPath, fontCJK, outRoot string) caseResult {
	t.Helper()

	res := caseResult{name: c.name, description: c.description}

	dir := t.TempDir()
	docxPath := c.build(t, dir)
	pdfPath := filepath.Join(dir, c.name+".pdf")

	opts := convert.Options{
		FontRegular:     fontPath,
		DefaultFontSize: 11,
		PageNumbers:     c.pageNumbers,
	}
	if c.useCJK {
		opts.FontFallback = fontCJK
	}
	if err := convert.Convert(docxPath, pdfPath, opts); err != nil {
		res.failures = append(res.failures, "convert: "+err.Error())
		t.Errorf("convert: %v", err)
		return res
	}

	// Copy the PDF into outRoot for inspection alongside the PNGs.
	caseOut := filepath.Join(outRoot, c.name)
	if err := os.MkdirAll(caseOut, 0o755); err != nil {
		t.Fatal(err)
	}
	destPDF := filepath.Join(caseOut, c.name+".pdf")
	if err := copyFile(pdfPath, destPDF); err != nil {
		t.Fatal(err)
	}
	res.pdfPath = destPDF

	res.pngPaths = renderPNG(t, pdfPath, caseOut)
	res.pages = pdfPageCount(t, pdfPath)
	res.textSample = pdftotext(t, pdfPath)

	// Substring assertions.
	for _, want := range c.expectText {
		if !strings.Contains(res.textSample, want) {
			msg := fmt.Sprintf("missing text %q", want)
			res.failures = append(res.failures, msg)
			t.Errorf("%s: %s\n--- extracted ---\n%s", c.name, msg, res.textSample)
		}
	}
	if c.expectPages > 0 && res.pages != c.expectPages {
		msg := fmt.Sprintf("page count: got %d want %d", res.pages, c.expectPages)
		res.failures = append(res.failures, msg)
		t.Errorf("%s: %s", c.name, msg)
	}
	if c.custom != nil {
		fail := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			res.failures = append(res.failures, msg)
			t.Errorf("%s: %s", c.name, msg)
		}
		c.custom(t, pdfPath, fail)
	}
	return res
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}
