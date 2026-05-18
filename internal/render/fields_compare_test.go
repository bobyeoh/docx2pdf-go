package render

import "testing"

func TestLookupFieldValue_COMPARE(t *testing.T) {
	cases := []struct {
		instr string
		want  string
	}{
		{` COMPARE 5 > 3 `, "1"},
		{` COMPARE 5 < 3 `, "0"},
		{` COMPARE "a" = "a" `, "1"},
		{` COMPARE 2 <> 2 `, "0"},
	}
	for _, c := range cases {
		got, ok := lookupFieldValueFull("COMPARE", "", c.instr, fieldVars{})
		if !ok {
			t.Errorf("%q: not evaluated", c.instr)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.instr, got, c.want)
		}
	}
}

func TestLookupFieldValue_MERGEREC(t *testing.T) {
	got, ok := lookupFieldValueFull("MERGEREC", "", " MERGEREC ", fieldVars{})
	if !ok || got != "1" {
		t.Errorf("MERGEREC: got %q ok=%v, want \"1\" true", got, ok)
	}
}

func TestLookupFieldValue_NEXTIsSuppressed(t *testing.T) {
	got, ok := lookupFieldValueFull("NEXT", "", " NEXT ", fieldVars{})
	if !ok || got != "" {
		t.Errorf("NEXT: got %q ok=%v, want \"\" true", got, ok)
	}
}
