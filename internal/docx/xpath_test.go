package docx

import "testing"

func TestResolveXPath_Element(t *testing.T) {
	data := []byte(`<r><a><b>hello</b></a></r>`)
	parts := []CustomXMLPart{{PartName: "p", Data: data}}
	v, ok := resolveXPath(parts, "/r/a/b")
	if !ok || v != "hello" {
		t.Errorf("element: got (%q,%v), want (hello,true)", v, ok)
	}
}

func TestResolveXPath_AttrSelector(t *testing.T) {
	data := []byte(`<r><item code="A1">first</item></r>`)
	parts := []CustomXMLPart{{PartName: "p", Data: data}}
	v, ok := resolveXPath(parts, "/r/item/@code")
	if !ok || v != "A1" {
		t.Errorf("attr selector: got (%q,%v), want (A1,true)", v, ok)
	}
}

func TestResolveXPath_PositionalPredicate(t *testing.T) {
	data := []byte(`<r><item>one</item><item>two</item><item>three</item></r>`)
	parts := []CustomXMLPart{{PartName: "p", Data: data}}
	cases := []struct {
		xp   string
		want string
	}{
		{"/r/item[1]", "one"},
		{"/r/item[2]", "two"},
		{"/r/item[3]", "three"},
	}
	for _, c := range cases {
		v, ok := resolveXPath(parts, c.xp)
		if !ok || v != c.want {
			t.Errorf("positional %s: got (%q,%v), want (%q,true)", c.xp, v, ok, c.want)
		}
	}
}

func TestResolveXPath_AttrEquality(t *testing.T) {
	data := []byte(`<r>
		<entry kind="email"><value>a@b.com</value></entry>
		<entry kind="phone"><value>555-1234</value></entry>
	</r>`)
	parts := []CustomXMLPart{{PartName: "p", Data: data}}
	v, ok := resolveXPath(parts, "/r/entry[@kind='phone']/value")
	if !ok || v != "555-1234" {
		t.Errorf("attr-eq predicate: got (%q,%v), want (555-1234,true)", v, ok)
	}
}

func TestResolveXPath_NamespacePrefix(t *testing.T) {
	data := []byte(`<x:r xmlns:x="urn:x"><x:a><x:b>ok</x:b></x:a></x:r>`)
	parts := []CustomXMLPart{{PartName: "p", Data: data}}
	v, ok := resolveXPath(parts, "/x:r/x:a/x:b")
	if !ok || v != "ok" {
		t.Errorf("namespaced: got (%q,%v), want (ok,true)", v, ok)
	}
}
