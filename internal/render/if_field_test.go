package render

import "testing"

func TestEvaluateIfField(t *testing.T) {
	cases := []struct {
		instr string
		want  string
	}{
		{`IF 1 = 1 "yes" "no"`, "yes"},
		{`IF 1 = 2 "yes" "no"`, "no"},
		{`IF 5 > 3 "ok" "bad"`, "ok"},
		{`IF 5 < 3 "ok" "bad"`, "bad"},
		{`IF "alpha" = "ALPHA" "match" "diff"`, "match"},
		{`IF "x" <> "y" "diff" "same"`, "diff"},
	}
	for _, tc := range cases {
		got, ok := evaluateIfField(tc.instr)
		if !ok {
			t.Errorf("%q: evaluate failed", tc.instr)
			continue
		}
		if got != tc.want {
			t.Errorf("%q = %q, want %q", tc.instr, got, tc.want)
		}
	}
}

func TestTokenizeFieldArgs_QuotedSpaces(t *testing.T) {
	toks := tokenizeFieldArgs(`a "b c" d`)
	want := []string{"a", "b c", "d"}
	if len(toks) != len(want) {
		t.Fatalf("got %v, want %v", toks, want)
	}
	for i := range toks {
		if toks[i] != want[i] {
			t.Errorf("toks[%d] = %q, want %q", i, toks[i], want[i])
		}
	}
}
