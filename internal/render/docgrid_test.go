package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestApplyLineHeight_DocGridSnap exercises the new behavior where
// w:docGrid w:linePitch snaps line heights up to a grid step. This is
// what CJK documents rely on so consecutive lines align to the page's
// underlying ruled grid.
func TestApplyLineHeight_DocGridSnap(t *testing.T) {
	r := makeTestRenderer(t)
	// 400 twips = 20 pt pitch. Natural lineH 14 should snap to 20.
	r.activeDocGrid = docx.DocGrid{Type: "lines", LinePitch: 400}
	got := r.applyLineHeight(14)
	if got != 20 {
		t.Errorf("docGrid snap: got %v want 20", got)
	}
	// Natural lineH 21 should snap to 40 (next multiple).
	got = r.applyLineHeight(21)
	if got != 40 {
		t.Errorf("docGrid snap multi: got %v want 40", got)
	}
}

// TestApplyLineHeight_DocGridIgnoresWhenNoneOrExact confirms that no
// snapping happens when the grid is "none" or the rule is "exact".
func TestApplyLineHeight_DocGridIgnoresWhenNoneOrExact(t *testing.T) {
	r := makeTestRenderer(t)
	r.activeDocGrid = docx.DocGrid{Type: "default", LinePitch: 400}
	got := r.applyLineHeight(14)
	if got != 14 {
		t.Errorf("none-grid should not snap: got %v want 14", got)
	}
	r.activeDocGrid = docx.DocGrid{Type: "lines", LinePitch: 400}
	r.lineHeight = docx.LineHeight{Rule: "exact", Pt: 12}
	got = r.applyLineHeight(14)
	if got != 12 {
		t.Errorf("exact rule should bypass snap: got %v want 12", got)
	}
}
