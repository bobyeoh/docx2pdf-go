package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestApplyRevisionPolicy_AcceptDrops(t *testing.T) {
	r := &renderer{opts: Options{ShowRevisions: false}}
	runs := []docx.Run{
		{Text: "kept"},
		{Text: "removed", RevisionType: "del"},
		{Text: "moved-out", RevisionType: "moveFrom"},
		{Text: "added", RevisionType: "ins"},
	}
	out := r.applyRevisionPolicy(runs)
	if len(out) != 2 {
		t.Fatalf("got %d runs, want 2 (kept + added)", len(out))
	}
	if out[0].Text != "kept" || out[1].Text != "added" {
		t.Errorf("got %+v, want [kept, added]", out)
	}
	if out[1].Props.Underline {
		t.Error("accept mode shouldn't decorate ins")
	}
}

func TestApplyRevisionPolicy_ShowDecorates(t *testing.T) {
	r := &renderer{opts: Options{ShowRevisions: true}}
	runs := []docx.Run{
		{Text: "kept"},
		{Text: "removed", RevisionType: "del", RevisionAuthor: "Alice"},
		{Text: "added", RevisionType: "ins", RevisionAuthor: "Bob"},
	}
	out := r.applyRevisionPolicy(runs)
	if len(out) != 3 {
		t.Fatalf("got %d runs, want 3", len(out))
	}
	if !out[1].Props.Strike {
		t.Error("del run should have strike")
	}
	if out[1].Props.Color == "" {
		t.Error("del run should pick up a color")
	}
	if !out[2].Props.Underline {
		t.Error("ins run should have underline")
	}
	// Author-based colors should differ.
	if out[1].Props.Color == out[2].Props.Color {
		t.Errorf("different authors should hash to different colors (got %s)", out[1].Props.Color)
	}
}

func TestRevisionColorDeterministic(t *testing.T) {
	a := revisionColorForAuthor("Alice", "FALLBK")
	b := revisionColorForAuthor("Alice", "FALLBK")
	if a != b {
		t.Errorf("same author → different colors: %s vs %s", a, b)
	}
	if revisionColorForAuthor("", "FALLBK") != "FALLBK" {
		t.Error("empty author should return fallback")
	}
}
