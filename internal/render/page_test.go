package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestSortStringDecimals(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"3", "1", "2"}, []string{"1", "2", "3"}},
		{[]string{"10", "2", "1"}, []string{"1", "2", "10"}},
		{[]string{"5"}, []string{"5"}},
		{nil, nil},
	}
	for _, c := range cases {
		got := append([]string(nil), c.in...)
		sortStringDecimals(got)
		if len(got) != len(c.want) {
			t.Errorf("sortStringDecimals(%v) length: got %v want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("sortStringDecimals(%v) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestApplyLineHeight_Rules verifies the line-height mode lookup logic on a
// dummy renderer — the math is independent of the gopdf state.
func TestApplyLineHeight_Rules(t *testing.T) {
	r := &renderer{}
	natural := 12.0

	// Empty rule → natural.
	if got := r.applyLineHeight(natural); got != natural {
		t.Errorf("empty rule: got %v want %v", got, natural)
	}
	// Exact wins.
	r.lineHeight = docx.LineHeight{Rule: "exact", Pt: 20}
	if got := r.applyLineHeight(natural); got != 20 {
		t.Errorf("exact: got %v want 20", got)
	}
	// atLeast = max(Pt, natural).
	r.lineHeight = docx.LineHeight{Rule: "atLeast", Pt: 8}
	if got := r.applyLineHeight(natural); got != natural {
		t.Errorf("atLeast (smaller Pt): got %v want %v", got, natural)
	}
	r.lineHeight = docx.LineHeight{Rule: "atLeast", Pt: 30}
	if got := r.applyLineHeight(natural); got != 30 {
		t.Errorf("atLeast (larger Pt): got %v want 30", got)
	}
	// auto scales by multiplier.
	r.lineHeight = docx.LineHeight{Rule: "auto", Mul: 1.5}
	if got := r.applyLineHeight(natural); got != natural*1.5 {
		t.Errorf("auto: got %v want %v", got, natural*1.5)
	}
}
