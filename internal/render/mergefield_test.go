package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestMergeField_Substitutes(t *testing.T) {
	vars := fieldVars{mergeData: map[string]string{
		"FirstName": "Alice",
	}}
	got, ok := lookupFieldValueFull("MERGEFIELD", "FirstName", "MERGEFIELD FirstName", vars)
	if !ok || got != "Alice" {
		t.Errorf("MERGEFIELD = (%q,%v), want (Alice,true)", got, ok)
	}
}

func TestMergeField_CaseInsensitive(t *testing.T) {
	vars := fieldVars{mergeData: map[string]string{"FirstName": "Alice"}}
	got, ok := lookupFieldValueFull("MERGEFIELD", "firstname", "MERGEFIELD firstname", vars)
	if !ok || got != "Alice" {
		t.Errorf("MERGEFIELD lowercase = (%q,%v)", got, ok)
	}
}

func TestMergeField_FallsThroughWhenMissing(t *testing.T) {
	vars := fieldVars{mergeData: map[string]string{"X": "Y"}}
	_, ok := lookupFieldValueFull("MERGEFIELD", "Missing", "MERGEFIELD Missing", vars)
	if ok {
		t.Error("missing key should not resolve")
	}
}

func TestMergeField_NoMapFallsThrough(t *testing.T) {
	// No MergeData supplied → must not override Word's cached value.
	_, ok := lookupFieldValueFull("MERGEFIELD", "X", "MERGEFIELD X", fieldVars{})
	if ok {
		t.Error("no mergeData should fall through")
	}
}

func TestMergeField_PrefixSuffix(t *testing.T) {
	vars := fieldVars{mergeData: map[string]string{"Name": "Bob"}}
	got, ok := lookupFieldValueFull("MERGEFIELD", "Name",
		`MERGEFIELD Name \b "Mr. " \f " Esq."`, vars)
	if !ok || got != "Mr. Bob Esq." {
		t.Errorf("affixed = (%q,%v), want (Mr. Bob Esq.,true)", got, ok)
	}
}

func TestFlattenFields_MergeFieldRendersWithSwitches(t *testing.T) {
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: `MERGEFIELD Name \* Upper`},
		{FieldSep: true},
		{Text: "«Name»"},
		{FieldEnd: true},
	}
	vars := fieldVars{mergeData: map[string]string{"Name": "alice"}}
	out := flattenFields(runs, vars)
	if len(out) != 1 || out[0].Text != "ALICE" {
		t.Errorf("merged = %+v, want one run with ALICE", out)
	}
}
