package render

import "testing"

func TestAutotextField_LooksUpDocPart(t *testing.T) {
	vars := fieldVars{glossary: map[string]string{
		"Sig": "Sincerely yours,\nThe team",
	}}
	got, ok := lookupFieldValueFull("AUTOTEXT", "Sig", "AUTOTEXT Sig", vars)
	if !ok || got != "Sincerely yours,\nThe team" {
		t.Errorf("AUTOTEXT = (%q,%v), want the glossary value", got, ok)
	}
}

func TestGlossaryField_LooksUpDocPart(t *testing.T) {
	vars := fieldVars{glossary: map[string]string{"Greeting": "Hello!"}}
	got, ok := lookupFieldValueFull("GLOSSARY", "Greeting", "GLOSSARY Greeting", vars)
	if !ok || got != "Hello!" {
		t.Errorf("GLOSSARY = (%q,%v), want Hello!", got, ok)
	}
}

func TestAutotextField_QuotedNameStillResolves(t *testing.T) {
	vars := fieldVars{glossary: map[string]string{"My Note": "BODY"}}
	got, ok := lookupFieldValueFull("AUTOTEXT", "\"My Note\"", "AUTOTEXT \"My Note\"", vars)
	if !ok || got != "BODY" {
		t.Errorf("quoted AUTOTEXT = (%q,%v), want BODY", got, ok)
	}
}

func TestAutotextField_MissingNameFallsThrough(t *testing.T) {
	vars := fieldVars{glossary: map[string]string{"X": "y"}}
	_, ok := lookupFieldValueFull("AUTOTEXT", "Missing", "AUTOTEXT Missing", vars)
	if ok {
		t.Error("missing AUTOTEXT key must not resolve — cached result should take over")
	}
}

func TestAutotextField_NoGlossaryFallsThrough(t *testing.T) {
	_, ok := lookupFieldValueFull("AUTOTEXT", "Sig", "AUTOTEXT Sig", fieldVars{})
	if ok {
		t.Error("no glossary should fall through to cached")
	}
}
