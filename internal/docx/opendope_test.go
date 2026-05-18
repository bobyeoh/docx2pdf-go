package docx

import (
	"testing"
)

func TestParseOpenDoPETag(t *testing.T) {
	cases := []struct {
		in              string
		wantCond        string
		wantRepeat      string
		wantXpath       string
	}{
		{"od:xpath=adX1", "", "", "adX1"},
		{"od:condition=adC1&od:xpath=adX2", "adC1", "", "adX2"},
		{"od:repeat=adR1", "", "adR1", ""},
		{"od:repeat=adR1&od:condition=adC2", "adC2", "adR1", ""},
	}
	for _, c := range cases {
		cond, rep, xp := parseOpenDoPETag(c.in, "", "", "")
		if cond != c.wantCond || rep != c.wantRepeat || xp != c.wantXpath {
			t.Errorf("parseOpenDoPETag(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, cond, rep, xp, c.wantCond, c.wantRepeat, c.wantXpath)
		}
	}
}

func TestParseOpenDoPEXPaths(t *testing.T) {
	xml := `<?xml version="1.0"?>
<od:xpaths xmlns:od="http://opendope.org/xpaths">
  <od:xpath id="adX1"><od:dataBinding xpath="/root/title"/></od:xpath>
  <od:xpath id="adX2"><od:dataBinding xpath="/root/author"/></od:xpath>
</od:xpaths>`
	table := parseOpenDoPEXPaths([]byte(xml))
	if got, want := table["adX1"], "/root/title"; got != want {
		t.Errorf("adX1 = %q, want %q", got, want)
	}
	if got, want := table["adX2"], "/root/author"; got != want {
		t.Errorf("adX2 = %q, want %q", got, want)
	}
}

func TestResolveOpenDoPECondition(t *testing.T) {
	doc := &Document{
		OpenDoPEXPaths: map[string]string{
			"adC1": "/root/yes",
			"adC2": "/root/no",
		},
		CustomXMLRoots: []CustomXMLPart{{
			PartName: "x",
			Data:     []byte(`<root><yes>true</yes><no>false</no></root>`),
		}},
	}
	if !resolveOpenDoPECondition(doc, "adC1") {
		t.Errorf("adC1 should be true")
	}
	if resolveOpenDoPECondition(doc, "adC2") {
		t.Errorf("adC2 should be false")
	}
	if resolveOpenDoPECondition(doc, "missing") {
		t.Errorf("missing should be false")
	}
}
