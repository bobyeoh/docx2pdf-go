package render

import (
	"reflect"
	"testing"
)

func TestBidiStrongClass(t *testing.T) {
	cases := []struct {
		r    rune
		want bidiStrong
	}{
		{'A', bidiStrongL},
		{'a', bidiStrongL},
		{'中', bidiStrongL}, // CJK is strong-L per UAX#9
		{'א', bidiStrongR}, // Hebrew
		{'ا', bidiStrongR}, // Arabic
		{'1', bidiNeutral},
		{' ', bidiNeutral},
		{',', bidiNeutral},
	}
	for _, c := range cases {
		if got := bidiStrongClass(c.r); got != c.want {
			t.Errorf("bidiStrongClass(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestFirstStrongClass(t *testing.T) {
	cases := []struct {
		s    string
		want bidiStrong
	}{
		{"hello", bidiStrongL},
		{"123 hello", bidiStrongL},
		{"مرحبا", bidiStrongR},
		{"123 مرحبا", bidiStrongR}, // first letter wins
		{"123", bidiNeutral},
		{"", bidiNeutral},
		{" .,", bidiNeutral},
	}
	for _, c := range cases {
		if got := firstStrongClass(c.s); got != c.want {
			t.Errorf("firstStrongClass(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestAtomBidiLevel(t *testing.T) {
	cases := []struct {
		text         string
		paragraphRTL bool
		want         uint8
	}{
		// LTR paragraph
		{"hello", false, 0},
		{"مرحبا", false, 1}, // RTL atom in LTR para
		{"123", false, 0},   // neutral inherits base
		// RTL paragraph
		{"hello", true, 2}, // LTR atom in RTL para
		{"مرحبا", true, 1}, // RTL atom in RTL para
		{"123", true, 1},   // neutral inherits base
	}
	for _, c := range cases {
		got := atomBidiLevel(c.text, c.paragraphRTL)
		if got != c.want {
			t.Errorf("atomBidiLevel(%q, paragraphRTL=%v) = %d, want %d",
				c.text, c.paragraphRTL, got, c.want)
		}
	}
}

// TestReorderAtomsL2_AllLTR confirms the all-LTR fast path leaves atoms
// in their original order.
func TestReorderAtomsL2_AllLTR(t *testing.T) {
	in := []atom{
		{text: "A", bidiLevel: 0},
		{text: "B", bidiLevel: 0},
		{text: "C", bidiLevel: 0},
	}
	got := reorderAtomsL2(in)
	want := []string{"A", "B", "C"}
	for i, a := range got {
		if a.text != want[i] {
			t.Errorf("[%d] text = %q, want %q", i, a.text, want[i])
		}
	}
}

// TestReorderAtomsL2_AllRTL reverses the line — the simple
// all-RTL-paragraph case.
func TestReorderAtomsL2_AllRTL(t *testing.T) {
	in := []atom{
		{text: "A", bidiLevel: 1},
		{text: "B", bidiLevel: 1},
		{text: "C", bidiLevel: 1},
	}
	got := reorderAtomsL2(in)
	want := []string{"C", "B", "A"}
	for i, a := range got {
		if a.text != want[i] {
			t.Errorf("[%d] text = %q, want %q", i, a.text, want[i])
		}
	}
}

// TestReorderAtomsL2_RTLEmbeddedInLTR: "Hello [Arabic] World"
// LTR paragraph; the single Arabic atom is at level 1, neighbours level 0.
// L2 (lvl=1) reverses the maximal contiguous level-1 subsequence — just
// the single atom — so order is unchanged.
func TestReorderAtomsL2_RTLEmbeddedInLTR(t *testing.T) {
	in := []atom{
		{text: "Hello", bidiLevel: 0},
		{text: " ", bidiLevel: 0},
		{text: "Arabic", bidiLevel: 1},
		{text: " ", bidiLevel: 0},
		{text: "World", bidiLevel: 0},
	}
	got := reorderAtomsL2(in)
	want := []string{"Hello", " ", "Arabic", " ", "World"}
	for i, a := range got {
		if a.text != want[i] {
			t.Errorf("[%d] text = %q, want %q", i, a.text, want[i])
		}
	}
}

// TestReorderAtomsL2_LTREmbeddedInRTL: RTL paragraph with embedded LTR
// fragment. RTL atoms are level 1, the LTR fragment is level 2.
// L2 algorithm:
//
//	lvl=2: reverse maximal level>=2 subseq — only the LTR atom (no
//	       effect).
//	lvl=1: reverse maximal level>=1 subseq — every atom — reverses
//	       the whole line.
//
// Result: visual order is reverse of logical (entire RTL line),
// preserving the LTR fragment as a sub-unit.
func TestReorderAtomsL2_LTREmbeddedInRTL(t *testing.T) {
	in := []atom{
		{text: "ARA1", bidiLevel: 1},
		{text: " ", bidiLevel: 1},
		{text: "LTR", bidiLevel: 2},
		{text: " ", bidiLevel: 1},
		{text: "ARA2", bidiLevel: 1},
	}
	got := reorderAtomsL2(in)
	want := []string{"ARA2", " ", "LTR", " ", "ARA1"}
	gotTexts := make([]string, len(got))
	for i, a := range got {
		gotTexts[i] = a.text
	}
	if !reflect.DeepEqual(gotTexts, want) {
		t.Errorf("reorder = %v, want %v", gotTexts, want)
	}
}

// TestReorderAtomsL2_AdjacentLTRsInRTL: two adjacent LTR atoms inside an
// RTL line should reverse only the wider RTL flow, not split the LTR
// pair. lvl=2 reverses the contiguous level-2 pair (LTR1 LTR2 → LTR2
// LTR1), then lvl=1 reverses the entire line. End result places the
// LTR pair in its original sub-order at the new (right-to-left) spot.
func TestReorderAtomsL2_AdjacentLTRsInRTL(t *testing.T) {
	in := []atom{
		{text: "ARA", bidiLevel: 1},
		{text: "LTR1", bidiLevel: 2},
		{text: "LTR2", bidiLevel: 2},
	}
	got := reorderAtomsL2(in)
	gotTexts := make([]string, len(got))
	for i, a := range got {
		gotTexts[i] = a.text
	}
	// Step 1 (lvl=2): swap LTR1 and LTR2 → [ARA, LTR2, LTR1]
	// Step 2 (lvl=1): reverse all → [LTR1, LTR2, ARA]
	want := []string{"LTR1", "LTR2", "ARA"}
	if !reflect.DeepEqual(gotTexts, want) {
		t.Errorf("reorder = %v, want %v", gotTexts, want)
	}
}

func TestParagraphBaseLevel(t *testing.T) {
	if got := paragraphBaseLevel(false); got != 0 {
		t.Errorf("LTR base = %d, want 0", got)
	}
	if got := paragraphBaseLevel(true); got != 1 {
		t.Errorf("RTL base = %d, want 1", got)
	}
}
