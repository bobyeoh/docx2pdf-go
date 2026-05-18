package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestParseCnfStyle_Individual(t *testing.T) {
	const x = `<cnfStyle xmlns="ns" firstRow="1" oddVBand="1"/>`
	dec := xml.NewDecoder(strings.NewReader(x))
	tok, _ := dec.Token()
	s := parseCnfStyle(tok.(xml.StartElement))
	if !s.FirstRow {
		t.Errorf("firstRow flag missing: %+v", s)
	}
	if !s.Band1Vert {
		t.Errorf("oddVBand should map to Band1Vert: %+v", s)
	}
}

func TestParseCnfStyle_BitString(t *testing.T) {
	// Position 0=firstRow ... 9=NWCell. Set firstRow + NWCell.
	const x = `<cnfStyle xmlns="ns" val="100000000100"/>`
	dec := xml.NewDecoder(strings.NewReader(x))
	tok, _ := dec.Token()
	s := parseCnfStyle(tok.(xml.StartElement))
	if !s.FirstRow {
		t.Errorf("FirstRow not set from bit string: %+v", s)
	}
	if !s.NWCell {
		t.Errorf("NWCell not set from bit string: %+v", s)
	}
}

func TestCnfStyle_Any(t *testing.T) {
	if (CnfStyle{}).Any() {
		t.Error("empty CnfStyle.Any() should be false")
	}
	if !(CnfStyle{FirstRow: true}).Any() {
		t.Error("FirstRow CnfStyle.Any() should be true")
	}
}
