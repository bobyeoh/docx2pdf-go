package render

import "testing"

func TestIsKinsokuNoStart(t *testing.T) {
	noStart := []string{"、", "。", "，", "．", "！", "？", "）", "」", "』", "】", "・", "％"}
	for _, s := range noStart {
		if !isKinsokuNoStart(atom{kind: atomWord, text: s}) {
			t.Errorf("%q should be NoStart", s)
		}
	}
	allowed := []string{"あ", "中", "A", "1", "「", "（"}
	for _, s := range allowed {
		if isKinsokuNoStart(atom{kind: atomWord, text: s}) {
			t.Errorf("%q must NOT be NoStart", s)
		}
	}
}

func TestIsKinsokuNoEnd(t *testing.T) {
	noEnd := []string{"（", "「", "『", "【", "〔", "$", "￥"}
	for _, s := range noEnd {
		if !isKinsokuNoEnd(atom{kind: atomWord, text: s}) {
			t.Errorf("%q should be NoEnd", s)
		}
	}
	allowed := []string{"中", "A", "1", "）", "」"}
	for _, s := range allowed {
		if isKinsokuNoEnd(atom{kind: atomWord, text: s}) {
			t.Errorf("%q must NOT be NoEnd", s)
		}
	}
}

func TestIsKinsokuNoStart_NonWordAtom(t *testing.T) {
	if isKinsokuNoStart(atom{kind: atomSpace, text: "、"}) {
		t.Error("non-word atom must not trigger kinsoku")
	}
	if isKinsokuNoStart(atom{kind: atomWord, text: ""}) {
		t.Error("empty atom must not trigger kinsoku")
	}
}
