package docx

import "testing"

func TestParseXPathCond_AttrEq(t *testing.T) {
	c, ok := parseXPathCond(`@id='42'`)
	if !ok || c.kind != "attr" || c.Name != "id" || c.Val != "42" || c.Negated {
		t.Errorf("got kind=%q name=%q val=%q negated=%v ok=%v",
			c.kind, c.Name, c.Val, c.Negated, ok)
	}
}

func TestParseXPathCond_Not(t *testing.T) {
	c, ok := parseXPathCond(`not(@id='42')`)
	if !ok || c.kind != "attr" || c.Name != "id" || c.Val != "42" || !c.Negated {
		t.Errorf("not(): got %+v ok=%v", c, ok)
	}
}

func TestParseXPathCond_Contains(t *testing.T) {
	c, ok := parseXPathCond(`contains(@name, 'foo')`)
	if !ok || c.kind != "contains" || c.Name != "name" || c.Val != "foo" {
		t.Errorf("contains: got %+v ok=%v", c, ok)
	}
}

func TestParseXPathCond_StartsWith(t *testing.T) {
	c, ok := parseXPathCond(`starts-with(@k, "prefix")`)
	if !ok || c.kind != "starts-with" || c.Name != "k" || c.Val != "prefix" {
		t.Errorf("starts-with: got %+v ok=%v", c, ok)
	}
}

func TestParseXPathCond_PositionEq(t *testing.T) {
	c, ok := parseXPathCond(`position()=3`)
	if !ok || c.kind != "position-eq" || c.PosN != 3 {
		t.Errorf("position(): got %+v ok=%v", c, ok)
	}
}

func TestParseXPathCond_Last(t *testing.T) {
	c, ok := parseXPathCond(`last()`)
	if !ok || c.kind != "last" {
		t.Errorf("last(): got %+v ok=%v", c, ok)
	}
}

func TestResolveXPath_Contains(t *testing.T) {
	data := []byte(`
<root>
  <Item name="lorem ipsum">one</Item>
  <Item name="dolor amet">two</Item>
</root>`)
	parts := []CustomXMLPart{{Data: data}}
	got, ok := resolveXPath(parts, `/root/Item[contains(@name, 'lor')]`)
	if !ok || got != "one" {
		t.Errorf("got=%q ok=%v, want one/true", got, ok)
	}
}

func TestResolveXPath_StartsWith(t *testing.T) {
	data := []byte(`
<root>
  <Item code="USD-100">one</Item>
  <Item code="EUR-200">two</Item>
</root>`)
	parts := []CustomXMLPart{{Data: data}}
	got, ok := resolveXPath(parts, `/root/Item[starts-with(@code, 'EUR')]`)
	if !ok || got != "two" {
		t.Errorf("got=%q ok=%v, want two/true", got, ok)
	}
}

func TestResolveXPath_NotAttr(t *testing.T) {
	data := []byte(`
<root>
  <Item id="x">first</Item>
  <Item id="y">second</Item>
</root>`)
	parts := []CustomXMLPart{{Data: data}}
	got, ok := resolveXPath(parts, `/root/Item[not(@id='x')]`)
	if !ok || got != "second" {
		t.Errorf("got=%q ok=%v, want second/true", got, ok)
	}
}
