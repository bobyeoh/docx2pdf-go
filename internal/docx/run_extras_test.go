package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestRPr_DStrike(t *testing.T) {
	const x = `<rPr xmlns="ns"><dstrike/></rPr>`
	var rp xmlRPr
	if err := xml.NewDecoder(strings.NewReader(x)).Decode(&rp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := rPrToProps(rp, RunProps{})
	if !p.DStrike {
		t.Errorf("DStrike not set: %+v", p)
	}
}

func TestRPr_UnderlineStyleCapture(t *testing.T) {
	const x = `<rPr xmlns="ns"><u val="wave"/></rPr>`
	var rp xmlRPr
	if err := xml.NewDecoder(strings.NewReader(x)).Decode(&rp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := rPrToProps(rp, RunProps{})
	if !p.Underline {
		t.Errorf("Underline not set: %+v", p)
	}
	if p.UnderlineStyle != "wave" {
		t.Errorf("UnderlineStyle = %q, want wave", p.UnderlineStyle)
	}
}

func TestMergeRunProps_DStrikePropagates(t *testing.T) {
	parent := RunProps{}
	child := RunProps{DStrike: true}
	out := MergeRunProps(parent, child)
	if !out.DStrike {
		t.Errorf("DStrike lost in merge: %+v", out)
	}
}
