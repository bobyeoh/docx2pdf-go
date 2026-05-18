package render

import (
	"strings"
	"testing"
)

func TestParseTCInstr(t *testing.T) {
	cases := []struct {
		instr  string
		want   string
		level  int
		wantOk bool
	}{
		{` TC "Hello World" \l 2 \f t `, "Hello World", 2, true},
		{` TC "Top" `, "Top", 1, true},
		{` TC Quick \l 3 `, "Quick", 3, true},
		{` SEQ Figure `, "", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTCInstr(c.instr)
		if ok != c.wantOk {
			t.Errorf("parseTCInstr(%q) ok=%v want %v", c.instr, ok, c.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if got.Text != c.want || got.Level != c.level {
			t.Errorf("parseTCInstr(%q) = {%q %d}, want {%q %d}",
				c.instr, got.Text, got.Level, c.want, c.level)
		}
	}
}

func TestParseXEInstr(t *testing.T) {
	if got := parseXEInstr(` XE "Apples"`); got != "Apples" {
		t.Errorf("XE quoted: got %q", got)
	}
	if got := parseXEInstr(` XE Apples \b `); got != "Apples" {
		t.Errorf("XE unquoted with switch: got %q", got)
	}
	if got := parseXEInstr(` XE "Fruit:Apples"`); got != "Fruit:Apples" {
		t.Errorf("XE subentry: got %q", got)
	}
}

func TestFormatIndex(t *testing.T) {
	in := []string{"Apples", "Bananas", "Fruit:Apples", "Fruit:Pears", "Apples"}
	got := formatIndex(in)
	// Apples appears once (dedup), Bananas after, Fruit with subentries.
	if !strings.Contains(got, "Apples") {
		t.Errorf("missing Apples: %q", got)
	}
	if !strings.Contains(got, "Fruit") {
		t.Errorf("missing Fruit: %q", got)
	}
	if !strings.Contains(got, "  Apples") {
		t.Errorf("expected indented subentry, got %q", got)
	}
}
