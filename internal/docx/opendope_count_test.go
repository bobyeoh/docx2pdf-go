package docx

import "testing"

// TestCountXPathMatches_DirectChildren counts every direct match across
// the customXml store — used by the OpenDoPE repeat resolver so it can
// emit N clones without probing positional predicates one at a time.
func TestCountXPathMatches_DirectChildren(t *testing.T) {
	parts := []CustomXMLPart{
		{Data: []byte(`<Root>
  <Items>
    <Item><Title>A</Title></Item>
    <Item><Title>B</Title></Item>
    <Item><Title>C</Title></Item>
  </Items>
</Root>`)},
	}
	got := countXPathMatches(parts, "/Root/Items/Item")
	if got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
}

func TestCountXPathMatches_WithAttributePredicate(t *testing.T) {
	parts := []CustomXMLPart{
		{Data: []byte(`<Root>
  <Item k="x"/>
  <Item k="y"/>
  <Item k="x"/>
</Root>`)},
	}
	got := countXPathMatches(parts, "/Root/Item[@k='x']")
	if got != 2 {
		t.Errorf("attr-pred count = %d, want 2", got)
	}
}

func TestCountXPathMatches_AttrSelector(t *testing.T) {
	parts := []CustomXMLPart{
		{Data: []byte(`<Root>
  <Item id="a"/>
  <Item id="b"/>
  <Item/>
</Root>`)},
	}
	got := countXPathMatches(parts, "/Root/Item/@id")
	if got != 2 {
		t.Errorf("attr-sel count = %d, want 2 (third Item has no id attr)", got)
	}
}

func TestApplyRepeatContext_RewritesPrefix(t *testing.T) {
	stack := []openDopeRepeatFrame{
		{xpathPrefix: "/Root/Items/Item", index: 3},
	}
	got := applyRepeatContext("/Root/Items/Item/Title", stack)
	want := "/Root/Items/Item[3]/Title"
	if got != want {
		t.Errorf("rewrite = %q, want %q", got, want)
	}
}

func TestApplyRepeatContext_LeavesUnrelatedAlone(t *testing.T) {
	stack := []openDopeRepeatFrame{
		{xpathPrefix: "/Root/Items/Item", index: 2},
	}
	got := applyRepeatContext("/Other/Path/Name", stack)
	if got != "/Other/Path/Name" {
		t.Errorf("rewrite mistakenly altered unrelated path: %q", got)
	}
}

func TestApplyRepeatContext_RespectsExistingPredicate(t *testing.T) {
	stack := []openDopeRepeatFrame{
		{xpathPrefix: "/Root/Items/Item", index: 2},
	}
	// Already hard-coded [1] — don't splice in another predicate.
	got := applyRepeatContext("/Root/Items/Item[1]/Title", stack)
	if got != "/Root/Items/Item[1]/Title" {
		t.Errorf("rewrite touched an existing predicate: %q", got)
	}
}

func TestApplyRepeatContext_RejectsMidElementMatch(t *testing.T) {
	stack := []openDopeRepeatFrame{
		{xpathPrefix: "/Root/Item", index: 1},
	}
	// "/Root/ItemList" must NOT match "/Root/Item" — the prefix would
	// have to splice mid-element-name which is wrong.
	got := applyRepeatContext("/Root/ItemList/Name", stack)
	if got != "/Root/ItemList/Name" {
		t.Errorf("rewrite spliced mid-element-name: %q", got)
	}
}
