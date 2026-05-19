package docx

import "testing"

func TestParseXPathStep_AndAttr(t *testing.T) {
	tests := []struct {
		input string
		want  xpathStep
	}{
		{
			input: `Item[@id='42' and @ver='2']`,
			want: xpathStep{
				name:        "Item",
				attrName:    "id",
				attrVal:     "42",
				andAttrName: "ver",
				andAttrVal:  "2",
			},
		},
		{
			input: `Foo[@k='v']`,
			want: xpathStep{
				name:     "Foo",
				attrName: "k",
				attrVal:  "v",
			},
		},
	}
	for _, tt := range tests {
		got := parseXPathStep(tt.input)
		if got.name != tt.want.name {
			t.Errorf("name = %q, want %q (input %q)", got.name, tt.want.name, tt.input)
		}
		if got.attrName != tt.want.attrName || got.attrVal != tt.want.attrVal {
			t.Errorf("primary attr = (%q,%q), want (%q,%q)",
				got.attrName, got.attrVal, tt.want.attrName, tt.want.attrVal)
		}
		if got.andAttrName != tt.want.andAttrName || got.andAttrVal != tt.want.andAttrVal {
			t.Errorf("and attr = (%q,%q), want (%q,%q)",
				got.andAttrName, got.andAttrVal, tt.want.andAttrName, tt.want.andAttrVal)
		}
	}
}

func TestResolveXPath_AndPredicate(t *testing.T) {
	data := []byte(`
<root>
  <Item id="1" ver="1">first</Item>
  <Item id="42" ver="1">old</Item>
  <Item id="42" ver="2">target</Item>
</root>`)
	parts := []CustomXMLPart{{Data: data}}
	got, ok := resolveXPath(parts, `/root/Item[@id='42' and @ver='2']`)
	if !ok {
		t.Fatal("resolveXPath: not ok")
	}
	if got != "target" {
		t.Errorf("got %q, want %q", got, "target")
	}
}

func TestSplitPredicateAnd(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"@a='x' and @b='y'", []string{"@a='x'", "@b='y'"}},
		{"@a='x and y'", []string{"@a='x and y'"}}, // 'and' inside quotes preserved
		{"@a='x'", []string{"@a='x'"}},
		{"@a='x' and @b='y' and @c='z'", []string{"@a='x'", "@b='y'", "@c='z'"}},
	}
	for _, tt := range tests {
		got := splitPredicateAnd(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("splitPredicateAnd(%q) → %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitPredicateAnd(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
