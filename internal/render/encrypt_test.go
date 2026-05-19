package render

import (
	"bytes"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func TestPDFEncryption(t *testing.T) {
	doc := &docx.Document{
		Body: []docx.Block{
			docx.Paragraph{Runs: []docx.Run{{Text: "Hello secret world"}}},
		},
		Sections: []docx.Section{{
			PageSize: docx.PageSize{WidthTwips: 12240, HeightTwips: 15840},
			Margins:  docx.DefaultMarginsTwips,
			Blocks: []docx.Block{
				docx.Paragraph{Runs: []docx.Run{{Text: "Hello secret world"}}},
			},
		}},
	}
	var buf bytes.Buffer
	err := RenderWriter(doc, &buf, Options{
		PDFUserPassword: "letmein",
	})
	if err != nil {
		t.Fatalf("RenderWriter encrypted: %v", err)
	}
	// PDF /Encrypt dict is emitted for protected docs. Unprotected
	// versions never carry it.
	if !bytes.Contains(buf.Bytes(), []byte("/Encrypt")) {
		t.Errorf("PDF lacks /Encrypt object (encryption not active)")
	}
	// Without a password, the same render should NOT carry /Encrypt.
	var plain bytes.Buffer
	err = RenderWriter(doc, &plain, Options{})
	if err != nil {
		t.Fatalf("RenderWriter plain: %v", err)
	}
	if bytes.Contains(plain.Bytes(), []byte("/Encrypt")) {
		t.Errorf("plain PDF unexpectedly carries /Encrypt")
	}
}
