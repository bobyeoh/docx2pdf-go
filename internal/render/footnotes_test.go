package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestBuildNoteLabels_Continuous(t *testing.T) {
	doc := &docx.Document{
		Footnotes: map[string][]docx.Block{
			"5": {}, "7": {}, "8": {},
		},
		Sections: []docx.Section{
			{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{
					{FootnoteID: "5", Text: "[5]"},
					{Text: " hello "},
					{FootnoteID: "7", Text: "[7]"},
					{FootnoteID: "8", Text: "[8]"},
				}},
			}},
		},
	}
	labels := buildNoteLabels(doc, false)
	if labels["5"] != "1" || labels["7"] != "2" || labels["8"] != "3" {
		t.Errorf("continuous labels = %+v, want 5→1,7→2,8→3", labels)
	}
}

func TestBuildNoteLabels_RomanFormat(t *testing.T) {
	doc := &docx.Document{
		Footnotes: map[string][]docx.Block{"1": {}, "2": {}, "3": {}, "4": {}},
		Sections: []docx.Section{
			{
				FootnotePr: &docx.NoteConfig{NumFmt: "lowerRoman"},
				Blocks: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{
						{FootnoteID: "1"}, {FootnoteID: "2"},
						{FootnoteID: "3"}, {FootnoteID: "4"},
					}},
				},
			},
		},
	}
	labels := buildNoteLabels(doc, false)
	if labels["1"] != "i" || labels["4"] != "iv" {
		t.Errorf("roman labels = %+v, want 1→i, 4→iv", labels)
	}
}

func TestBuildNoteLabels_RestartEachSect(t *testing.T) {
	doc := &docx.Document{
		Footnotes: map[string][]docx.Block{"a": {}, "b": {}, "c": {}},
		Sections: []docx.Section{
			{Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{FootnoteID: "a"}, {FootnoteID: "b"}}},
			}},
			{
				FootnotePr: &docx.NoteConfig{Restart: "eachSect"},
				Blocks: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{{FootnoteID: "c"}}},
				},
			},
		},
	}
	labels := buildNoteLabels(doc, false)
	if labels["a"] != "1" || labels["b"] != "2" {
		t.Errorf("first sect labels = %+v", labels)
	}
	if labels["c"] != "1" {
		t.Errorf("second sect should restart, got %q for c", labels["c"])
	}
}

func TestFormatNoteNumber_Chicago(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "*"}, {2, "†"}, {6, "¶"}, {7, "**"},
	}
	for _, c := range cases {
		got := formatNoteNumber(c.n, "chicago")
		if got != c.want {
			t.Errorf("chicago(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
