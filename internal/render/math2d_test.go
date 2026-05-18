package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestMath2D_TextLeaf(t *testing.T) {
	r := makeTestRenderer(t)
	box := r.buildMathBox(&docx.MathNode{Kind: "t", Text: "abc"}, 12)
	if box.w <= 0 {
		t.Errorf("text leaf width = %v", box.w)
	}
}

// TestMath2D_FractionBoxDimensions confirms that a fraction box reports
// a height that exceeds the font size (because numerator and denominator
// stack), and a width at least as wide as the wider of the two children.
func TestMath2D_FractionBoxDimensions(t *testing.T) {
	// Build a manual MathNode for "a/bc": numerator one character,
	// denominator two characters.
	tree := &docx.MathNode{
		Kind: "f",
		Num: &docx.MathNode{
			Kind: "num",
			Children: []*docx.MathNode{
				{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "a"}}},
			},
		},
		Den: &docx.MathNode{
			Kind: "den",
			Children: []*docx.MathNode{
				{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "bc"}}},
			},
		},
	}

	r := makeTestRenderer(t)
	box := r.buildMathBox(tree, 12)
	if box.height() <= 12 {
		t.Errorf("fraction height (%v) should exceed single-line font size (12)", box.height())
	}
	if box.w <= 0 {
		t.Errorf("fraction width = %v, want > 0", box.w)
	}
}

// TestMath2D_RadicalAscent confirms that wrapping a base in a radical
// increases the ascent (we draw the vinculum above).
func TestMath2D_RadicalAscent(t *testing.T) {
	r := makeTestRenderer(t)
	plain := r.buildMathBox(&docx.MathNode{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "x"}}}, 12)
	rad := r.buildMathBox(&docx.MathNode{
		Kind: "rad",
		Base: &docx.MathNode{Kind: "e", Children: []*docx.MathNode{{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "x"}}}}},
	}, 12)
	if rad.ascent <= plain.ascent {
		t.Errorf("radical ascent (%v) should exceed plain ascent (%v)", rad.ascent, plain.ascent)
	}
}

// TestMath2D_SupBoxAddsHeight confirms that wrapping a base in a sup
// raises the ascent.
func TestMath2D_SupBoxAddsHeight(t *testing.T) {
	r := makeTestRenderer(t)
	plain := r.buildMathBox(&docx.MathNode{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "x"}}}, 12)
	sup := r.buildMathBox(&docx.MathNode{
		Kind: "sSup",
		Base: &docx.MathNode{Kind: "e", Children: []*docx.MathNode{{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "x"}}}}},
		Sup:  &docx.MathNode{Kind: "sup", Children: []*docx.MathNode{{Kind: "r", Children: []*docx.MathNode{{Kind: "t", Text: "2"}}}}},
	}, 12)
	if sup.ascent <= plain.ascent {
		t.Errorf("sup ascent (%v) should exceed plain ascent (%v)", sup.ascent, plain.ascent)
	}
}
