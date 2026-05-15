package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// TestApplyEmbeddedDocFonts confirms that an embedded body/heading
// font in the document gets bound to Options.Font* via sentinel
// paths, and that explicit caller paths are preserved.
func TestApplyEmbeddedDocFonts(t *testing.T) {
	doc := &docx.Document{
		Theme: docx.Theme{
			Fonts: map[string]string{
				"minorAscii": "Arial",
				"majorAscii": "Calibri",
			},
		},
		EmbeddedFonts: map[string]docx.EmbeddedFontSet{
			"arial": {
				Regular: []byte("ARIAL-REGULAR"),
				Bold:    []byte("ARIAL-BOLD"),
				// no Italic — leave slot empty
			},
			"calibri": {
				Regular: []byte("CALIBRI-REGULAR"),
			},
		},
	}

	opts := Options{}
	data := applyEmbeddedDocFonts(&opts, doc)

	if opts.FontRegular == "" || opts.FontBold == "" || opts.FontHeading == "" {
		t.Fatalf("expected FontRegular/FontBold/FontHeading filled, got %+v", opts)
	}
	if opts.FontItalic != "" {
		t.Errorf("FontItalic should be untouched (no embed bytes), got %q", opts.FontItalic)
	}
	if string(data[opts.FontRegular]) != "ARIAL-REGULAR" {
		t.Errorf("FontRegular sentinel points at %q, want ARIAL-REGULAR", data[opts.FontRegular])
	}
	if string(data[opts.FontBold]) != "ARIAL-BOLD" {
		t.Errorf("FontBold sentinel points at %q, want ARIAL-BOLD", data[opts.FontBold])
	}
	if string(data[opts.FontHeading]) != "CALIBRI-REGULAR" {
		t.Errorf("FontHeading sentinel points at %q, want CALIBRI-REGULAR", data[opts.FontHeading])
	}
}

// TestApplyEmbeddedDocFonts_ExplicitWins confirms a caller-supplied
// Options.FontRegular path is preserved even when the doc has an
// embedded body font.
func TestApplyEmbeddedDocFonts_ExplicitWins(t *testing.T) {
	doc := &docx.Document{
		Theme: docx.Theme{Fonts: map[string]string{"minorAscii": "Arial"}},
		EmbeddedFonts: map[string]docx.EmbeddedFontSet{
			"arial": {Regular: []byte("ARIAL-REGULAR")},
		},
	}
	opts := Options{FontRegular: "/explicit/path.ttf"}
	applyEmbeddedDocFonts(&opts, doc)
	if opts.FontRegular != "/explicit/path.ttf" {
		t.Errorf("caller path overwritten: got %q", opts.FontRegular)
	}
}

// TestApplyEmbeddedDocFonts_FallbackToDefaultsFamily covers the
// pre-theme docDefaults path: no theme entry, but Defaults.FontFamily
// names the body face that's embedded.
func TestApplyEmbeddedDocFonts_FallbackToDefaultsFamily(t *testing.T) {
	doc := &docx.Document{
		Defaults: docx.RunProps{FontFamily: "Times New Roman"},
		EmbeddedFonts: map[string]docx.EmbeddedFontSet{
			"times new roman": {Regular: []byte("TIMES-R")},
		},
	}
	opts := Options{}
	data := applyEmbeddedDocFonts(&opts, doc)
	if opts.FontRegular == "" {
		t.Fatal("expected FontRegular filled from docDefaults")
	}
	if string(data[opts.FontRegular]) != "TIMES-R" {
		t.Errorf("FontRegular bytes = %q, want TIMES-R", data[opts.FontRegular])
	}
}

// TestApplyEmbeddedDocFonts_NoEmbeds is a no-op smoke test: empty
// EmbeddedFonts map returns nil and leaves Options untouched.
func TestApplyEmbeddedDocFonts_NoEmbeds(t *testing.T) {
	doc := &docx.Document{}
	opts := Options{}
	if data := applyEmbeddedDocFonts(&opts, doc); data != nil {
		t.Errorf("expected nil map for doc with no embeds, got %v", data)
	}
	if opts.FontRegular != "" || opts.FontBold != "" {
		t.Errorf("Options changed when no embeds present: %+v", opts)
	}
}
