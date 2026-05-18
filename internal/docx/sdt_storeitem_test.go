package docx

import "testing"

// TestResolveXPathInStore_FiltersByGUID verifies that when a SDT
// declares a storeItemID, the resolver pulls from the matching store
// only — overlapping element names in other stores must not leak.
func TestResolveXPathInStore_FiltersByGUID(t *testing.T) {
	parts := []CustomXMLPart{
		{
			StoreItemID: "{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}",
			Data:        []byte(`<Root><Name>FROM_A</Name></Root>`),
		},
		{
			StoreItemID: "{bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb}",
			Data:        []byte(`<Root><Name>FROM_B</Name></Root>`),
		},
	}
	got, ok := resolveXPathInStore(parts, "{bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb}", "/Root/Name")
	if !ok || got != "FROM_B" {
		t.Errorf("storeItemID-scoped lookup = (%q, %v), want (FROM_B, true)", got, ok)
	}
}

func TestResolveXPathInStore_StripsBraces(t *testing.T) {
	parts := []CustomXMLPart{
		{StoreItemID: "aaaa-bbbb", Data: []byte(`<Root><Name>OK</Name></Root>`)},
	}
	// Caller may or may not include braces around the GUID.
	got, ok := resolveXPathInStore(parts, "{aaaa-bbbb}", "/Root/Name")
	if !ok || got != "OK" {
		t.Errorf("brace-stripping = (%q, %v), want (OK, true)", got, ok)
	}
}

// TestResolveXPathInStore_EmptyGUIDIsLegacyBehavior asserts that the
// store-scoped helper falls back to the original "scan all parts"
// behavior when storeItemID is empty.
func TestResolveXPathInStore_EmptyGUIDIsLegacyBehavior(t *testing.T) {
	parts := []CustomXMLPart{
		{Data: []byte(`<Root><Name>FIRST</Name></Root>`)},
		{Data: []byte(`<Root><Name>SECOND</Name></Root>`)},
	}
	got, ok := resolveXPathInStore(parts, "", "/Root/Name")
	if !ok || got != "FIRST" {
		t.Errorf("empty-GUID legacy lookup = (%q, %v), want (FIRST, true)", got, ok)
	}
}

// TestResolveXPathInStore_FallsBackWhenGUIDMissing asserts that an
// unknown storeItemID falls back to scanning all parts rather than
// failing — older writers sometimes drop the itemPropsN.xml file.
func TestResolveXPathInStore_FallsBackWhenGUIDMissing(t *testing.T) {
	parts := []CustomXMLPart{
		{Data: []byte(`<Root><Name>FOUND</Name></Root>`)},
	}
	got, ok := resolveXPathInStore(parts, "{no-such-guid}", "/Root/Name")
	if !ok || got != "FOUND" {
		t.Errorf("fallback = (%q, %v), want (FOUND, true)", got, ok)
	}
}

func TestStorePropsCompanion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"word/customXml/item1.xml", "word/customXml/itemProps1.xml"},
		{"word/customXml/item42.xml", "word/customXml/itemProps42.xml"},
		{"weird/path/notitem.xml", "weird/path/notitem.xml"},
	}
	for _, c := range cases {
		if got := storePropsCompanion(c.in); got != c.want {
			t.Errorf("companion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
