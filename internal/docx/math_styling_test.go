package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

// decodeMath drives the OMML decoder against a fragment that has a single
// outer element named `root`. Returns the inner top-level node (first
// child) — keeps the test concise.
func decodeMathHelper(t *testing.T, body string) *MathNode {
	t.Helper()
	src := `<root xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">` + body + `</root>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, _ := dec.Token() // root start
	se, ok := tok.(xml.StartElement)
	if !ok {
		t.Fatalf("bad fixture")
	}
	n, err := decodeMathNode(dec, se)
	if err != nil {
		t.Fatalf("decodeMathNode: %v", err)
	}
	return n
}

// TestMathRPrFlags exercises the m:rPr → MathNode plumbing for the styling
// attributes that drive italic/upright/bold/script substitution.
func TestMathRPrFlags(t *testing.T) {
	cases := []struct {
		body     string
		wantNor  bool
		wantStyB bool
		wantStyI bool
		wantScr  string
	}{
		{`<m:r><m:rPr><m:nor/></m:rPr><m:t>x</m:t></m:r>`, true, false, false, ""},
		{`<m:r><m:rPr><m:sty m:val="b"/></m:rPr><m:t>x</m:t></m:r>`, false, true, false, ""},
		{`<m:r><m:rPr><m:sty m:val="i"/></m:rPr><m:t>x</m:t></m:r>`, false, false, true, ""},
		{`<m:r><m:rPr><m:scr m:val="doubleStruck"/></m:rPr><m:t>R</m:t></m:r>`, false, false, false, "doubleStruck"},
	}
	for _, c := range cases {
		root := decodeMathHelper(t, c.body)
		if len(root.Children) == 0 {
			t.Fatalf("no child for %q", c.body)
		}
		mr := root.Children[0]
		if mr.Nor != c.wantNor {
			t.Errorf("%q: Nor=%v want %v", c.body, mr.Nor, c.wantNor)
		}
		if mr.StyleB != c.wantStyB {
			t.Errorf("%q: StyleB=%v want %v", c.body, mr.StyleB, c.wantStyB)
		}
		if mr.StyleI != c.wantStyI {
			t.Errorf("%q: StyleI=%v want %v", c.body, mr.StyleI, c.wantStyI)
		}
		if mr.Script != c.wantScr {
			t.Errorf("%q: Script=%q want %q", c.body, mr.Script, c.wantScr)
		}
	}
}

// TestMathBoxRender confirms that m:box collapses to just its content
// (no longer the literal corner-brackets it used to emit).
func TestMathBoxRender(t *testing.T) {
	root := decodeMathHelper(t, `<m:box><m:e><m:r><m:t>x</m:t></m:r></m:e></m:box>`)
	box := root.Children[0]
	got := box.render()
	if got != "x" {
		t.Errorf("m:box render = %q want %q", got, "x")
	}
}

// TestMathGroupChrUnder confirms that m:groupChr with pos=bot renders the
// character after the base rather than before.
func TestMathGroupChrUnder(t *testing.T) {
	root := decodeMathHelper(t, `<m:groupChr><m:groupChrPr><m:chr m:val="⏟"/><m:pos m:val="bot"/></m:groupChrPr><m:e><m:r><m:t>n</m:t></m:r></m:e></m:groupChr>`)
	g := root.Children[0]
	if !g.AccUnder {
		t.Errorf("AccUnder not set")
	}
	got := g.render()
	if got != "n⏟" {
		t.Errorf("groupChr under render = %q want %q", got, "n⏟")
	}
}
