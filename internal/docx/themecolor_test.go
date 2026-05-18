package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestScanColor_SchemeClr_ResolvesViaTheme(t *testing.T) {
	theme := Theme{Colors: map[string]string{"accent1": "4F81BD"}}
	const src = `<sf xmlns="x"><schemeClr val="accent1"/></sf>`
	dec := xml.NewDecoder(strings.NewReader(src))
	se := startToken(t, dec)
	got := ScanColor(dec, se, theme)
	if got != "4F81BD" {
		t.Fatalf("got %q, want 4F81BD", got)
	}
}

func TestScanColor_SchemeClr_LumMod(t *testing.T) {
	// accent1 at 50% luminance ought to darken.
	theme := Theme{Colors: map[string]string{"accent1": "808080"}}
	const src = `<sf xmlns="x"><schemeClr val="accent1"><lumMod val="50000"/></schemeClr></sf>`
	dec := xml.NewDecoder(strings.NewReader(src))
	se := startToken(t, dec)
	got := ScanColor(dec, se, theme)
	if got != "404040" {
		t.Fatalf("lumMod-50%% of 808080 = %q, want 404040", got)
	}
}

func TestScanColor_SrgbClr_StillWorks(t *testing.T) {
	const src = `<sf xmlns="x"><srgbClr val="123456"/></sf>`
	dec := xml.NewDecoder(strings.NewReader(src))
	se := startToken(t, dec)
	got := ScanColor(dec, se, Theme{})
	if got != "123456" {
		t.Fatalf("got %q", got)
	}
}

func TestScanColor_BgTxAlias(t *testing.T) {
	// "bg1" in body should map to "lt1" in theme.Colors.
	theme := Theme{Colors: map[string]string{"lt1": "FFFFFF"}}
	const src = `<sf xmlns="x"><schemeClr val="bg1"/></sf>`
	dec := xml.NewDecoder(strings.NewReader(src))
	se := startToken(t, dec)
	got := ScanColor(dec, se, theme)
	if got != "FFFFFF" {
		t.Fatalf("got %q, want FFFFFF", got)
	}
}

func TestScanColor_PrstClr(t *testing.T) {
	const src = `<sf xmlns="x"><prstClr val="red"/></sf>`
	dec := xml.NewDecoder(strings.NewReader(src))
	se := startToken(t, dec)
	got := ScanColor(dec, se, Theme{})
	if got != "FF0000" {
		t.Fatalf("got %q", got)
	}
}

// startToken consumes tokens until the first StartElement and returns it.
func startToken(t *testing.T, dec *xml.Decoder) xml.StartElement {
	t.Helper()
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("no start token: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se
		}
	}
}
