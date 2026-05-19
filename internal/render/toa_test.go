package render

import (
	"strings"
	"testing"
)

func TestParseTAInstr(t *testing.T) {
	tests := []struct {
		name      string
		instr     string
		wantLong  string
		wantShort string
		wantCat   int
		wantOk    bool
	}{
		{
			name:      "basic_long_only",
			instr:     `TA \l "Marbury v. Madison, 5 U.S. 137 (1803)"`,
			wantLong:  "Marbury v. Madison, 5 U.S. 137 (1803)",
			wantShort: "",
			wantCat:   1,
			wantOk:    true,
		},
		{
			name:      "long_short_category",
			instr:     `TA \l "U.S. Const. amend. XIV" \s "14th Am." \c 7`,
			wantLong:  "U.S. Const. amend. XIV",
			wantShort: "14th Am.",
			wantCat:   7,
			wantOk:    true,
		},
		{
			name:     "no_long",
			instr:    `TA \c 2`,
			wantLong: "",
			wantCat:  1,
			wantOk:   false,
		},
		{
			name:     "positional_long",
			instr:    `TA "Statute §42-101" \c 2`,
			wantLong: "Statute §42-101",
			wantCat:  2,
			wantOk:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTAInstr(tt.instr)
			if ok != tt.wantOk {
				t.Fatalf("parseTAInstr ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if got.Long != tt.wantLong {
				t.Errorf("Long = %q, want %q", got.Long, tt.wantLong)
			}
			if got.Short != tt.wantShort {
				t.Errorf("Short = %q, want %q", got.Short, tt.wantShort)
			}
			if got.Category != tt.wantCat {
				t.Errorf("Category = %d, want %d", got.Category, tt.wantCat)
			}
		})
	}
}

func TestFormatTOA_GroupsAndSorts(t *testing.T) {
	entries := []taEntry{
		{Long: "Brown v. Board, 347 U.S. 483", Category: 1, PageNum: 12},
		{Long: "Marbury v. Madison, 5 U.S. 137", Category: 1, PageNum: 3},
		{Long: "Brown v. Board, 347 U.S. 483", Category: 1, PageNum: 18},
		{Long: "U.S. Const. amend. I", Category: 7, PageNum: 5},
	}
	got := formatTOA(entries, toaSwitches{Leader: "."})
	// Cases header first (cat=1)
	if !strings.Contains(got, "Cases") {
		t.Fatalf("missing Cases header:\n%s", got)
	}
	// Both cases must be present, alphabetically (Brown before Marbury)
	brownIdx := strings.Index(got, "Brown")
	marburyIdx := strings.Index(got, "Marbury")
	if brownIdx < 0 || marburyIdx < 0 || brownIdx > marburyIdx {
		t.Errorf("Brown should sort before Marbury; brown=%d marbury=%d", brownIdx, marburyIdx)
	}
	// Brown page numbers merged + de-duped
	if !strings.Contains(got, "12, 18") {
		t.Errorf("expected merged page list '12, 18'; got:\n%s", got)
	}
	// Constitutional Provisions header (cat=7) for U.S. Const.
	if !strings.Contains(got, "Constitutional Provisions") {
		t.Errorf("missing Constitutional Provisions header:\n%s", got)
	}
}

func TestFormatTOA_CategoryFilter(t *testing.T) {
	entries := []taEntry{
		{Long: "A", Category: 1, PageNum: 1},
		{Long: "B", Category: 2, PageNum: 2},
	}
	got := formatTOA(entries, toaSwitches{Leader: ".", Category: 2})
	if strings.Contains(got, "Cases") {
		t.Errorf("category filter \\c 2 should suppress Cases header:\n%s", got)
	}
	if !strings.Contains(got, "Statutes") {
		t.Errorf("expected Statutes header:\n%s", got)
	}
}

func TestFormatTOA_OmitHeader(t *testing.T) {
	entries := []taEntry{{Long: "A", Category: 1, PageNum: 1}}
	got := formatTOA(entries, toaSwitches{OmitHeader: true, Leader: "."})
	if strings.Contains(got, "Cases") {
		t.Errorf("\\f should omit headers; got:\n%s", got)
	}
	if !strings.HasPrefix(got, "A") {
		t.Errorf("expected entry first; got:\n%s", got)
	}
}

func TestLookupTOA(t *testing.T) {
	vars := fieldVars{taEntries: []taEntry{
		{Long: "Roe v. Wade", Category: 1, PageNum: 7},
	}}
	out, ok := lookupFieldValueFull("TOA", "", `TOA \h`, vars)
	if !ok {
		t.Fatal("TOA lookup returned !ok")
	}
	if !strings.Contains(out, "Roe v. Wade") {
		t.Errorf("output missing entry:\n%s", out)
	}
}
