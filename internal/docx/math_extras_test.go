package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func decodeOMMLRoot(t *testing.T, xs string) *MathNode {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(xs))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			n, err := decodeMathNode(dec, se)
			if err != nil {
				t.Fatalf("decodeMathNode: %v", err)
			}
			return n
		}
	}
}

func TestMath_FracTypeParsed(t *testing.T) {
	const x = `<f xmlns="ns"><fPr><type val="skw"/></fPr><num><r><t>a</t></r></num><den><r><t>b</t></r></den></f>`
	n := decodeOMMLRoot(t, x)
	if n.FracType != "skw" {
		t.Errorf("FracType = %q, want skw", n.FracType)
	}
}

func TestMath_NaryLimLocAndHide(t *testing.T) {
	const x = `<nary xmlns="ns"><naryPr><limLoc val="subSup"/><supHide val="1"/></naryPr><sub><r><t>1</t></r></sub><sup><r><t>n</t></r></sup><e><r><t>x</t></r></e></nary>`
	n := decodeOMMLRoot(t, x)
	if n.NaryLimLoc != "subSup" {
		t.Errorf("NaryLimLoc = %q, want subSup", n.NaryLimLoc)
	}
	if !n.NarySupHide {
		t.Errorf("NarySupHide should be true")
	}
}

func TestMath_DGrow(t *testing.T) {
	const x = `<d xmlns="ns"><dPr><grow val="1"/></dPr><e><r><t>x</t></r></e></d>`
	n := decodeOMMLRoot(t, x)
	if !n.DGrow {
		t.Errorf("DGrow should be true")
	}
}

func TestMath_BarPos(t *testing.T) {
	const x = `<bar xmlns="ns"><barPr><pos val="bot"/></barPr><e><r><t>x</t></r></e></bar>`
	n := decodeOMMLRoot(t, x)
	if !n.AccUnder {
		t.Errorf("AccUnder should be true for pos=bot")
	}
}
