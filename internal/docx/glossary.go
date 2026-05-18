package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strings"
)

// parseGlossaryPart walks word/glossary/document.xml and populates
// doc.Glossary with a key→text map. The keys are the docPart names
// (w:docPartPr/w:name w:val="…"); the values are the concatenated
// plain-text contents of each docPart's body.
//
// Word stores building blocks (AutoText, Quick Parts, etc.) here. We
// keep the payload as plain text so AUTOTEXT / GLOSSARY fields can
// expand to something meaningful in the PDF — full rich-content
// reinjection would require deep-copying the parsed paragraph stream
// and is out of scope.
func parseGlossaryPart(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "docPart" {
			name, text := decodeGlossaryDocPart(dec, se)
			if name == "" {
				continue
			}
			if doc.Glossary == nil {
				doc.Glossary = map[string]string{}
			}
			doc.Glossary[name] = text
		}
	}
}

// decodeGlossaryDocPart walks one <w:docPart> and returns its name and
// concatenated text body. Both default to empty strings on miss.
func decodeGlossaryDocPart(dec *xml.Decoder, start xml.StartElement) (name, text string) {
	depth := 1
	var bodyTokens []byte
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return name, text
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "name":
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						name = a.Value
					}
				}
			case "t":
				// Plain text run inside the docPart body. Accumulate
				// CharData up to the matching end element.
				txt := readCharDataUntilEnd(dec)
				bodyTokens = append(bodyTokens, []byte(txt)...)
				depth--
			case "tab":
				bodyTokens = append(bodyTokens, '\t')
			case "br":
				bodyTokens = append(bodyTokens, '\n')
			case "p":
				// Mark paragraph boundary — newline between paragraphs so
				// AUTOTEXT expansion preserves the document writer's
				// blocking. Leading newline is trimmed at the end.
				if len(bodyTokens) > 0 {
					bodyTokens = append(bodyTokens, '\n')
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	text = strings.TrimSpace(string(bodyTokens))
	return name, text
}

// readCharDataUntilEnd consumes tokens until the matching EndElement
// of the calling element, returning the concatenated CharData. Used
// when a leaf element (like w:t) wraps text that may be split across
// multiple CharData chunks.
func readCharDataUntilEnd(dec *xml.Decoder) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return b.String()
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return b.String()
}
