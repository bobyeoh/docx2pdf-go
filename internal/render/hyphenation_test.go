package render

import "testing"

func TestHyphenateEnglish(t *testing.T) {
	tests := []struct {
		word    string
		wantAny bool
	}{
		// Words known to have suffix patterns in our table.
		{"connection", true}, // ...tion. suffix → break before "tion"
		{"adoption", true},   // ...tion.
		{"happiness", true},  // ...ness.
		{"computing", true},  // ...ing.
		{"international", true},
		{"preprocessing", true},
		// Words with prefix patterns.
		{"automobile", true},
		{"undefined", true},
		{"redesign", true},
		// Too short → no breaks.
		{"cat", false},
		{"of", false},
		{"the", false},
		// Non-letters → no breaks.
		{"abc123", false},
		{"hello-world", false},
	}
	for _, tt := range tests {
		got := hyphenateEnglish(tt.word)
		any := len(got) > 0
		if any != tt.wantAny {
			t.Errorf("hyphenateEnglish(%q) = %v (breaks=%v), wantAny=%v",
				tt.word, any, got, tt.wantAny)
		}
	}
}

func TestHyphenateEnglish_PositionSanity(t *testing.T) {
	// All recommended breaks must be within (2, len-2) — Liang's
	// boilerplate "no break of fewer than 2 letters at either edge".
	breaks := hyphenateEnglish("microservices")
	for _, k := range breaks {
		if k < 2 || k > len("microservices")-2 {
			t.Errorf("break at %d is outside [2..len-2] for 'microservices': %v", k, breaks)
		}
	}
}

func TestHyphenateForLang_German(t *testing.T) {
	// Knuth-Liang patterns produce a hyphen position for German
	// compounds. We don't assert specific positions (table is curated)
	// — just that the multi-language dispatcher honors the lang code.
	if got := hyphenateForLang("Geschwindigkeit", "de-DE"); len(got) == 0 {
		t.Error("German hyphenator returned no breaks for 'Geschwindigkeit'")
	}
}

func TestHyphenateForLang_DefaultsToEnglish(t *testing.T) {
	a := hyphenateEnglish("hyphenation")
	b := hyphenateForLang("hyphenation", "")
	if len(a) != len(b) {
		t.Errorf("empty lang did not default to English: %v vs %v", a, b)
	}
}
