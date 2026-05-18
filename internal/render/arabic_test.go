package render

import (
	"testing"
)

// TestShapeArabic_BasicJoining covers the four contextual forms via a
// classic three-letter Arabic word.
//
// كتب means "wrote" (kataba). Logical order: KAF (0643), TEH (062A),
// BEH (0628). Joining types: all dual. Expected forms after shaping:
//
//	KAF: initial → FEDB
//	TEH: medial  → FE98
//	BEH: final   → FE90
func TestShapeArabic_BasicJoining(t *testing.T) {
	in := string([]rune{0x0643, 0x062A, 0x0628})
	got := []rune(shapeArabic(in))
	want := []rune{0xFEDB, 0xFE98, 0xFE90}
	if len(got) != len(want) {
		t.Fatalf("got %d runes, want %d: %x", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("rune[%d] = U+%04X, want U+%04X", i, got[i], want[i])
		}
	}
}

// TestShapeArabic_RightJoinerBreaks covers a right-joining letter
// (REH, 0631) in the middle of a word: it must accept a join from
// the previous letter but break the chain forward.
//
// برج (burj, "tower"): BEH (D) + REH (R) + JEEM (D).
//
//	BEH: only joins on right side here (REH joins prev) → initial FE91
//	REH: joins prev (BEH joins next) → final FEAE
//	JEEM: REH does not join next → isolated FE9D
func TestShapeArabic_RightJoinerBreaks(t *testing.T) {
	in := string([]rune{0x0628, 0x0631, 0x062C})
	got := []rune(shapeArabic(in))
	want := []rune{0xFE91, 0xFEAE, 0xFE9D}
	for i := range got {
		if i >= len(want) || got[i] != want[i] {
			t.Errorf("rune[%d] = U+%04X, want U+%04X", i, got[i], want[i])
		}
	}
}

// TestShapeArabic_NonJoining covers HAMZA (0621), a non-joining
// letter that should never affect neighbors and always render as
// FE80. Pair it with a dual letter and confirm the dual letter is
// shaped as if HAMZA isn't there.
func TestShapeArabic_NonJoining(t *testing.T) {
	in := string([]rune{0x0621, 0x0628}) // HAMZA + BEH
	got := []rune(shapeArabic(in))
	// HAMZA: isolated → FE80
	// BEH: HAMZA doesn't join next, so BEH has no left-joining
	// predecessor → no medial/final possible. BEH could be initial
	// only if a joinable next letter exists; here it's alone →
	// isolated FE8F.
	want := []rune{0xFE80, 0xFE8F}
	for i := range got {
		if i >= len(want) || got[i] != want[i] {
			t.Errorf("rune[%d] = U+%04X, want U+%04X", i, got[i], want[i])
		}
	}
}

// TestShapeArabic_LamAlef covers the most common Arabic ligature.
// لا (LAM + ALEF, "no") should collapse to FEFB (isolated form).
func TestShapeArabic_LamAlef(t *testing.T) {
	in := string([]rune{0x0644, 0x0627})
	got := []rune(shapeArabic(in))
	if len(got) != 1 {
		t.Fatalf("got %d runes, want 1 (ligature): %x", len(got), got)
	}
	if got[0] != 0xFEFB {
		t.Errorf("ligature = U+%04X, want U+FEFB", got[0])
	}
}

// TestShapeArabic_LamAlefFinal: the ligature in final form (after a
// joinable predecessor) should be FEFC.
//
// بلا (BEH + LAM + ALEF). BEH joins LAM, so the LAM+ALEF pair should
// render as the final-form ligature.
func TestShapeArabic_LamAlefFinal(t *testing.T) {
	in := string([]rune{0x0628, 0x0644, 0x0627})
	got := []rune(shapeArabic(in))
	if len(got) != 2 {
		t.Fatalf("got %d runes, want 2 (BEH + ligature): %x", len(got), got)
	}
	// BEH: joinable next (LAM) and no joinable prev → initial FE91.
	if got[0] != 0xFE91 {
		t.Errorf("BEH = U+%04X, want U+FE91", got[0])
	}
	if got[1] != 0xFEFC {
		t.Errorf("ligature = U+%04X, want U+FEFC", got[1])
	}
}

// TestShapeArabic_NonArabicPassthrough confirms text without any
// Arabic runes is returned unchanged (cheap pre-check).
func TestShapeArabic_NonArabicPassthrough(t *testing.T) {
	in := "Hello World 123"
	if got := shapeArabic(in); got != in {
		t.Errorf("non-Arabic text changed: %q → %q", in, got)
	}
}

// TestShapeArabic_TashkeelTransparent: diacritics (064B..0652)
// should not break joining chains. كَتَبَ has the same shapes as
// كتب for the consonants.
func TestShapeArabic_TashkeelTransparent(t *testing.T) {
	// KAF + FATHA + TEH + FATHA + BEH + FATHA
	in := string([]rune{0x0643, 0x064E, 0x062A, 0x064E, 0x0628, 0x064E})
	got := []rune(shapeArabic(in))
	// Expected: initial KAF (FEDB), FATHA, medial TEH (FE98), FATHA,
	// final BEH (FE90), FATHA.
	want := []rune{0xFEDB, 0x064E, 0xFE98, 0x064E, 0xFE90, 0x064E}
	if len(got) != len(want) {
		t.Fatalf("got %d runes, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("rune[%d] = U+%04X, want U+%04X", i, got[i], want[i])
		}
	}
}

func TestContainsArabic(t *testing.T) {
	if !containsArabic("Hello مرحبا") {
		t.Error("Arabic-containing string returned false")
	}
	if containsArabic("Hello World") {
		t.Error("non-Arabic returned true")
	}
	if containsArabic("") {
		t.Error("empty returned true")
	}
}
