package render

import "testing"

func TestShapeArabic_SimpleWord(t *testing.T) {
	// "بيت" (house) — three Arabic letters all dual-joining.
	// Expected after shaping (visual order, post-bidi reverse):
	//   ت Final + ي Medial + ب Initial
	// As a logical-order Unicode string, ب=0x0628, ي=0x064A, ت=0x062A
	// → Initial-BEH (FE91) + Medial-YEH (FEF4) + Final-TEH (FE96).
	// reorderBidi reverses to visual, then shapeArabic picks forms.
	input := "بيت"
	got := shapeArabic(reorderBidi(input, true))
	if got == input {
		t.Errorf("shapeArabic returned input unchanged — expected presentation forms")
	}
	// Confirm at least one rune is in the Arabic Presentation Forms-B
	// block (U+FE70..FEFC).
	found := false
	for _, r := range got {
		if r >= 0xFE70 && r <= 0xFEFC {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("shaped output %q has no FE70-FEFC runes", got)
	}
}

func TestShapeArabic_PassThroughNonArabic(t *testing.T) {
	in := "Hello, World!"
	if got := shapeArabic(in); got != in {
		t.Errorf("non-Arabic input was modified: %q → %q", in, got)
	}
}

func TestReorderBidi_PureLTR_NoChange(t *testing.T) {
	in := "Hello, World!"
	if got := reorderBidi(in, false); got != in {
		t.Errorf("pure LTR reorder changed: %q → %q", in, got)
	}
}

func TestReorderBidi_RTLReverses(t *testing.T) {
	in := "אבג"
	got := reorderBidi(in, true)
	if got == in {
		t.Errorf("Hebrew was not reordered: %q", got)
	}
}

func TestArabicJoinClass_Transparent(t *testing.T) {
	if c := arabicJoinClass(0x064E); c != "T" {
		t.Errorf("FATHA join class = %q, want T", c)
	}
}

func TestShapeArabic_LamAlefLigature(t *testing.T) {
	// "لا" — Lam (U+0644) + Alef (U+0627). In logical order the lam
	// comes first; after reorderBidi the alef ends up before the lam in
	// the rune slice. Our substitution combines them into U+FEFB
	// (isolated Lam-Alef ligature) when nothing else attaches to the
	// lam on its right.
	input := "لا"
	got := shapeArabic(reorderBidi(input, true))
	runes := []rune(got)
	if len(runes) != 1 {
		t.Fatalf("lam+alef should collapse to 1 rune, got %d: %U", len(runes), runes)
	}
	if runes[0] != 0xFEFB {
		t.Errorf("lam-alef ligature = U+%04X, want U+FEFB", runes[0])
	}
}

func TestShapeArabic_LamAlefHamzaAbove(t *testing.T) {
	// "لأ" — Lam + Alef-Hamza-Above → U+FEF7 (isolated) ligature.
	input := "لأ"
	got := shapeArabic(reorderBidi(input, true))
	runes := []rune(got)
	if len(runes) != 1 || runes[0] != 0xFEF7 {
		t.Errorf("lam-alef-hamza ligature = %U, want [U+FEF7]", runes)
	}
}

func TestShapeArabic_LamAlefAfterConnectingLetter(t *testing.T) {
	// "بلا" — beh + lam + alef. The lam connects to the beh on its
	// right, so the lam-alef collapses to the FINAL form U+FEFC.
	input := "بلا"
	got := shapeArabic(reorderBidi(input, true))
	found := false
	for _, r := range got {
		if r == 0xFEFC {
			found = true
		}
	}
	if !found {
		t.Errorf("lam-alef-final not present in %U", []rune(got))
	}
}
