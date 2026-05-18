package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestScanSdtProps_DropdownListItem(t *testing.T) {
	src := `<w:sdtPr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:dropDownList w:lastValue="GREEN">
			<w:listItem w:displayText="Red"  w:value="RED"/>
			<w:listItem w:displayText="Green" w:value="GREEN"/>
			<w:listItem w:displayText="Blue" w:value="BLUE"/>
		</w:dropDownList>
	</w:sdtPr>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, _ := dec.Token()
	start := tok.(xml.StartElement)
	props := scanSdtProps(dec, start)
	if props.kind != "dropdown" {
		t.Errorf("kind = %q, want dropdown", props.kind)
	}
	if len(props.choices) != 3 {
		t.Fatalf("got %d choices, want 3", len(props.choices))
	}
	if props.choices[1] != "Green" || props.choiceValues[1] != "GREEN" {
		t.Errorf("choice[1] = (%q,%q), want (Green,GREEN)", props.choices[1], props.choiceValues[1])
	}
	if props.selectedValue != "GREEN" {
		t.Errorf("selectedValue = %q", props.selectedValue)
	}
	got, ok := sdtSyntheticText(props)
	if !ok || got != "Green" {
		t.Errorf("synthetic = (%q,%v), want (Green,true)", got, ok)
	}
}

func TestScanSdtProps_DropdownFallsBackToValueWhenNoMatch(t *testing.T) {
	src := `<w:sdtPr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:dropDownList w:lastValue="orphan-key">
			<w:listItem w:displayText="A" w:value="A"/>
		</w:dropDownList>
	</w:sdtPr>`
	dec := xml.NewDecoder(strings.NewReader(src))
	tok, _ := dec.Token()
	start := tok.(xml.StartElement)
	props := scanSdtProps(dec, start)
	got, ok := sdtSyntheticText(props)
	if !ok || got != "orphan-key" {
		t.Errorf("synthetic = (%q,%v), want fallback to raw value", got, ok)
	}
}
