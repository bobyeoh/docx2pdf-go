package render

import (
	"strings"
	"testing"
)

func TestFormatIndexWithPages_MergesAndSorts(t *testing.T) {
	entries := []string{"Apple", "Apple", "Banana", "Apple:red", "Apple:green"}
	pages := map[string][]int{
		"Apple":       {1, 5, 5, 12, 8},
		"Banana":      {3},
		"Apple:red":   {7, 7},
		"Apple:green": {11},
	}
	got := formatIndexWithPages(entries, &pages)
	// Apple line: "Apple, 1, 5, 8, 12"
	if !strings.Contains(got, "Apple, 1, 5, 8, 12") {
		t.Errorf("missing merged page list:\n%s", got)
	}
	// Subentry lines indented with leading spaces
	if !strings.Contains(got, "\n  green, 11") {
		t.Errorf("missing minor 'green':\n%s", got)
	}
	if !strings.Contains(got, "\n  red, 7") {
		t.Errorf("missing minor 'red':\n%s", got)
	}
	// Banana sorts after Apple
	bi := strings.Index(got, "Banana")
	ai := strings.Index(got, "Apple")
	if bi < 0 || ai < 0 || bi < ai {
		t.Errorf("Apple should sort before Banana; ai=%d bi=%d\n%s", ai, bi, got)
	}
}

func TestFormatIndexWithPages_FallsBackWhenNoPages(t *testing.T) {
	entries := []string{"Foo", "Bar"}
	got := formatIndexWithPages(entries, nil)
	// Without pages it should match the page-less formatter.
	want := formatIndex(entries)
	if got != want {
		t.Errorf("nil-pages path mismatch:\ngot: %q\nwant: %q", got, want)
	}
}
