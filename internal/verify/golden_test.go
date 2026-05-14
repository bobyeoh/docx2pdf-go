package verify

import (
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestGoldenSnapshots compares every PNG in internal/verify/out/<case>/ against
// the corresponding baseline in internal/verify/golden/<case>/ using mean
// per-pixel L1 distance.
//
// Behavior:
//
//	GOLDEN=1            → run comparison (default: test skipped)
//	UPDATE_GOLDEN=1     → overwrite baselines with current PNGs; pass.
//	(neither set)       → skipped, so CI doesn't fail on fontconfig drift.
//
// This is opt-in because pdftoppm output isn't byte-stable across machines:
// font versions, freetype hinting, and renderer arithmetic all jitter the
// rasterized bytes a little. Running it locally before a release is the
// intended workflow.
func TestGoldenSnapshots(t *testing.T) {
	if os.Getenv("GOLDEN") != "1" && os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set GOLDEN=1 to run golden snapshot diff (UPDATE_GOLDEN=1 to refresh baselines)")
	}
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	outRoot := mustAbs(t, "out")
	goldenRoot := mustAbs(t, "golden")
	if _, err := os.Stat(outRoot); err != nil {
		t.Skip("out/ directory missing — run TestVerifyAll first to populate snapshots")
	}
	if update {
		if err := os.MkdirAll(goldenRoot, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Walk out/<case>/<page>.png; for each, compare against golden/<case>/.
	cases, err := os.ReadDir(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	const threshold = 0.02 // ~2% mean L1 — tolerates AA/hinting drift, catches structure changes.

	for _, c := range cases {
		if !c.IsDir() {
			continue
		}
		caseName := c.Name()
		caseDir := filepath.Join(outRoot, caseName)
		goldenDir := filepath.Join(goldenRoot, caseName)

		pngs, err := listPNGs(caseDir)
		if err != nil {
			t.Errorf("%s: %v", caseName, err)
			continue
		}
		if len(pngs) == 0 {
			continue
		}
		t.Run(caseName, func(t *testing.T) {
			for _, png := range pngs {
				cur := filepath.Join(caseDir, png)
				ref := filepath.Join(goldenDir, png)
				if update {
					if err := os.MkdirAll(goldenDir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := copyFile(cur, ref); err != nil {
						t.Fatalf("copy golden: %v", err)
					}
					continue
				}
				if _, err := os.Stat(ref); err != nil {
					// First-time bootstrap: golden missing. Copy and pass.
					if err := os.MkdirAll(goldenDir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := copyFile(cur, ref); err != nil {
						t.Fatalf("seed golden %s: %v", ref, err)
					}
					t.Logf("seeded golden %s", ref)
					continue
				}
				diff, err := pngL1Diff(cur, ref)
				if err != nil {
					t.Errorf("%s: %v", png, err)
					continue
				}
				if diff > threshold {
					t.Errorf("%s/%s: diff %.4f > threshold %.4f", caseName, png, diff, threshold)
				} else {
					t.Logf("%s/%s: diff %.4f (ok)", caseName, png, diff)
				}
			}
		})
	}
}

func listPNGs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// pngL1Diff returns the mean per-pixel L1 distance between two PNGs, scaled
// to [0,1]. Returns 1.0 (max different) if dimensions don't match.
//
// We deliberately do not use SSIM or a perceptual metric — for catching the
// kinds of regression we're protecting against (a feature stops rendering,
// fonts change, layout shifts), simple L1 is sensitive enough and trivial
// to debug.
func pngL1Diff(aPath, bPath string) (float64, error) {
	a, err := loadPNG(aPath)
	if err != nil {
		return 0, err
	}
	b, err := loadPNG(bPath)
	if err != nil {
		return 0, err
	}
	if a.Bounds().Size() != b.Bounds().Size() {
		return 1.0, nil
	}
	bounds := a.Bounds()
	var sum int64
	var n int64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			sum += abs64(int64(ar) - int64(br))
			sum += abs64(int64(ag) - int64(bg))
			sum += abs64(int64(ab) - int64(bb))
			n += 3
		}
	}
	if n == 0 {
		return 0, nil
	}
	// Each channel is in [0, 65535] after RGBA(). Normalize to [0, 1].
	return float64(sum) / (float64(n) * 65535), nil
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// io import kept so future readers see the intentional pattern even if not
// directly referenced; helps when extending this file with stream readers.
var _ = io.EOF
