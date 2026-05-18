package render

import (
	"reflect"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestComputeCommentDepths(t *testing.T) {
	doc := &docx.Document{
		CommentMeta: map[string]docx.CommentMeta{
			"1": {ParaID: "p1"},
			"2": {ParaID: "p2"}, // reply to 1
			"3": {ParaID: "p3"}, // reply to 2 (depth 2)
			"4": {ParaID: "p4"}, // root
		},
		CommentsExtended: map[string]docx.CommentExtended{
			"p2": {ParaID: "p2", ParentParaID: "p1"},
			"p3": {ParaID: "p3", ParentParaID: "p2"},
		},
	}
	paraToID := map[string]string{
		"p1": "1", "p2": "2", "p3": "3", "p4": "4",
	}
	d := computeCommentDepths(doc, paraToID)
	if d["1"] != 0 || d["2"] != 1 || d["3"] != 2 || d["4"] != 0 {
		t.Errorf("depths = %+v, want 1:0 2:1 3:2 4:0", d)
	}
}

func TestOrderCommentsByThread(t *testing.T) {
	doc := &docx.Document{
		CommentMeta: map[string]docx.CommentMeta{
			"1": {ParaID: "p1"},
			"2": {ParaID: "p2"},
			"3": {ParaID: "p3"},
			"4": {ParaID: "p4"},
		},
		CommentsExtended: map[string]docx.CommentExtended{
			"p3": {ParaID: "p3", ParentParaID: "p1"},
			"p4": {ParaID: "p4", ParentParaID: "p3"},
		},
	}
	paraToID := map[string]string{"p1": "1", "p2": "2", "p3": "3", "p4": "4"}
	// Source order is 1,2,3,4. After threading: 1, 3, 4, 2 (replies
	// nested under 1; 2 is its own root).
	out := orderCommentsByThread([]string{"1", "2", "3", "4"}, doc, paraToID)
	want := []string{"1", "3", "4", "2"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("ordered = %v, want %v", out, want)
	}
}
