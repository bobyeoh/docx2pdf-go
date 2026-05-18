package docx

import "testing"

func TestResolveNumStyleLinks(t *testing.T) {
	n := &Numbering{
		Abstract: map[int]AbstractNum{
			1: {
				StyleLink: "MyListStyle",
				Levels: map[int]NumLevel{
					0: {Format: "decimal", Text: "%1.", Start: 1},
					1: {Format: "lowerLetter", Text: "%2)", Start: 1},
				},
			},
			5: {
				NumStyleLink: "MyListStyle",
				Levels:       map[int]NumLevel{},
			},
		},
	}
	resolveNumStyleLinks(n)
	target := n.Abstract[5]
	if len(target.Levels) != 2 {
		t.Fatalf("expected 2 levels after resolve; got %d", len(target.Levels))
	}
	if target.Levels[0].Format != "decimal" {
		t.Errorf("level 0 format = %q, want decimal", target.Levels[0].Format)
	}
	if target.Levels[1].Format != "lowerLetter" {
		t.Errorf("level 1 format = %q, want lowerLetter", target.Levels[1].Format)
	}
	if len(n.Abstract[1].Levels) != 2 {
		t.Errorf("source abstractNum mutated")
	}
}

func TestResolveNumStyleLinks_NoTarget(t *testing.T) {
	n := &Numbering{
		Abstract: map[int]AbstractNum{
			5: {NumStyleLink: "Missing", Levels: map[int]NumLevel{}},
		},
	}
	resolveNumStyleLinks(n)
	if len(n.Abstract[5].Levels) != 0 {
		t.Errorf("levels should remain empty")
	}
}

func TestResolveNumStyleLinks_DoesNotOverwriteFilledStub(t *testing.T) {
	n := &Numbering{
		Abstract: map[int]AbstractNum{
			1: {StyleLink: "A", Levels: map[int]NumLevel{0: {Format: "decimal"}}},
			5: {NumStyleLink: "A", Levels: map[int]NumLevel{0: {Format: "bullet", Text: "•"}}},
		},
	}
	resolveNumStyleLinks(n)
	if n.Abstract[5].Levels[0].Format != "bullet" {
		t.Errorf("levels overwritten: got %q, want bullet", n.Abstract[5].Levels[0].Format)
	}
}
