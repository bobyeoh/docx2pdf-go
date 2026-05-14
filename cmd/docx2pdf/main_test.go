package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestFindDocxFlat(t *testing.T) {
	dir := t.TempDir()
	touch := func(p string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch("a.docx")
	touch("b.DOCX") // case-insensitive match
	touch("~$tmp.docx")
	touch("notes.txt")
	mk("sub")
	if err := os.WriteFile(filepath.Join(dir, "sub", "c.docx"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findDocx(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 {
		t.Fatalf("non-recursive: got %v, want 2 entries (a.docx, b.DOCX)", got)
	}

	got2, err := findDocx(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 3 {
		t.Fatalf("recursive: got %v, want 3 entries", got2)
	}

	// Confirm we filter the Word lockfile pattern.
	for _, p := range got2 {
		if filepath.Base(p) == "~$tmp.docx" {
			t.Errorf("lockfile %s should be filtered", p)
		}
	}
}

func TestWithExt(t *testing.T) {
	cases := map[string]string{
		"a.docx":     "a.pdf",
		"x/y/b.docx": "x/y/b.pdf",
		"no_ext":     "no_ext.pdf",
	}
	for in, want := range cases {
		if got := withExt(in, ".pdf"); got != want {
			t.Errorf("withExt(%q) = %q want %q", in, got, want)
		}
	}
}
