package render

import (
	"math"
	"testing"
)

// TestPolygonScanline checks that a closed polygon's scan-line crossings
// produce the expected left/right span.
func TestPolygonScanline(t *testing.T) {
	// A diamond (rhombus) inscribed in a 100x100 box centred at (100, 100).
	poly := []polyVertex{
		{x: 100, y: 50},  // top
		{x: 150, y: 100}, // right
		{x: 100, y: 150}, // bottom
		{x: 50, y: 100},  // left
	}
	cases := []struct {
		y      float64
		lo, hi float64
		ok     bool
	}{
		{y: 75, lo: 75, hi: 125, ok: true},
		{y: 100, lo: 50, hi: 150, ok: true},
		{y: 125, lo: 75, hi: 125, ok: true},
		{y: 49, ok: false},
		{y: 151, ok: false},
	}
	for _, c := range cases {
		lo, hi, ok := polygonScanline(poly, 50, 150, c.y)
		if ok != c.ok {
			t.Errorf("y=%.0f: ok=%v, want %v", c.y, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if math.Abs(lo-c.lo) > 0.01 || math.Abs(hi-c.hi) > 0.01 {
			t.Errorf("y=%.0f: span=[%g,%g], want [%g,%g]", c.y, lo, hi, c.lo, c.hi)
		}
	}
}
