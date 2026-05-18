package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// parseFontTable reads word/fontTable.xml and the matching
// word/_rels/fontTable.xml.rels to populate doc.EmbeddedFonts.
//
// fontTable.xml has the shape:
//
//	<w:fonts>
//	  <w:font w:name="Arial">
//	    <w:embedRegular r:id="rIdFont1" w:fontKey="{GUID}"/>
//	    <w:embedBold    r:id="rIdFont2" w:fontKey="{GUID}"/>
//	    ...
//	  </w:font>
//	  ...
//	</w:fonts>
//
// Each r:id resolves via the rels file to a font file under word/fonts/,
// typically `font1.odttf`. ODTTF is a TrueType blob with the first 32
// bytes XOR-obfuscated against the fontKey GUID (ECMA-376 Part 1 §17.8).
// Plain `.ttf` parts also occur and pass through unchanged.
//
// Errors loading individual fonts are swallowed: embedded fonts are an
// optional enhancement, so a malformed entry falls through to system
// font resolution rather than failing the whole parse.
func parseFontTable(files map[string]*zip.File, doc *Document) error {
	fontTable, ok := files["word/fontTable.xml"]
	if !ok {
		return nil
	}

	rels := map[string]relEntry{}
	if relsFile, ok := files["word/_rels/fontTable.xml.rels"]; ok {
		if err := parseRels(relsFile, rels); err != nil {
			return fmt.Errorf("fontTable.rels: %w", err)
		}
	}

	rc, err := openZipFile(fontTable)
	if err != nil {
		return fmt.Errorf("fontTable.xml: %w", err)
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)

	type embedRef struct {
		rid     string
		fontKey string
		variant string // "Regular" / "Bold" / "Italic" / "BoldItalic"
	}

	var currentName string
	var currentEmbeds []embedRef

	flush := func() {
		if currentName == "" || len(currentEmbeds) == 0 {
			return
		}
		set := EmbeddedFontSet{}
		gotAny := false
		for _, e := range currentEmbeds {
			rel, ok := rels[e.rid]
			if !ok || rel.External {
				continue
			}
			target := rel.Target
			// Targets are usually relative to word/_rels/, i.e. "fonts/font1.odttf".
			// They can also be absolute "/word/fonts/font1.odttf". Normalize.
			target = strings.TrimPrefix(target, "/")
			if !strings.HasPrefix(target, "word/") {
				target = "word/" + target
			}
			zf, ok := files[target]
			if !ok {
				continue
			}
			data, err := readZipBytes(zf)
			if err != nil {
				continue
			}
			if strings.HasSuffix(strings.ToLower(target), ".odttf") {
				data = deobfuscateODTTF(data, e.fontKey)
			}
			switch e.variant {
			case "Regular":
				set.Regular = data
			case "Bold":
				set.Bold = data
			case "Italic":
				set.Italic = data
			case "BoldItalic":
				set.BoldItalic = data
			}
			gotAny = true
		}
		if gotAny {
			if doc.EmbeddedFonts == nil {
				doc.EmbeddedFonts = map[string]EmbeddedFontSet{}
			}
			doc.EmbeddedFonts[strings.ToLower(currentName)] = set
		}
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("fontTable.xml decode: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "font":
				flush()
				currentName = attr(t, "name")
				currentEmbeds = currentEmbeds[:0]
			case "embedRegular", "embedBold", "embedItalic", "embedBoldItalic":
				variant := strings.TrimPrefix(t.Name.Local, "embed")
				currentEmbeds = append(currentEmbeds, embedRef{
					rid:     attr(t, "id"),
					fontKey: attr(t, "fontKey"),
					variant: variant,
				})
			}
		}
	}
	flush()
	return nil
}

// readZipBytes reads the full contents of a zip entry. Small helper so
// callers don't repeat the Open/ReadAll/Close dance.
func readZipBytes(f *zip.File) ([]byte, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// deobfuscateODTTF reverses Word's ODTTF font obfuscation: the first 32
// bytes are XORed with the 16-byte GUID, repeated twice (so bytes 0-15
// use GUID byte by byte, and bytes 16-31 use it again). The GUID comes
// in `{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}` form; Word writes the
// hex pairs in mixed-endian order matching how the GUID prints, so the
// bytes are consumed right-to-left from the parsed string per ECMA-376.
//
// Returns the original bytes unchanged if the key is malformed — that
// case typically means a plain .ttf was mislabeled and works as-is.
func deobfuscateODTTF(data []byte, fontKey string) []byte {
	key := parseODTTFKey(fontKey)
	if key == nil {
		return data
	}
	if len(data) < 32 {
		return data
	}
	out := make([]byte, len(data))
	copy(out, data)
	// XOR first 32 bytes with the 16-byte key, applied twice.
	for i := 0; i < 32 && i < len(out); i++ {
		out[i] ^= key[i%16]
	}
	return out
}

// parseODTTFKey extracts the 16 bytes from a `{XXXXXXXX-XXXX-XXXX-XXXX-
// XXXXXXXXXXXX}` GUID string. The OOXML spec stores them so the XOR
// proceeds in reverse-of-hex order: e.g. for {AAAAAAAA-BBBB-CCCC-DDDD-
// EEEEEEEEEEEE} the first XOR byte is the last hex pair "EE". Returns
// nil on any parse error so the caller can fall back to raw data.
func parseODTTFKey(s string) []byte {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return nil
	}
	// Parse hex into 16 bytes (big-endian as written).
	raw := make([]byte, 16)
	for i := 0; i < 16; i++ {
		b, err := hexByte(s[i*2 : i*2+2])
		if err != nil {
			return nil
		}
		raw[i] = b
	}
	// Reverse: the spec consumes bytes from least-significant-printed
	// to most-significant-printed, which for a printed GUID is
	// right-to-left across the whole string (after removing dashes).
	key := make([]byte, 16)
	for i := 0; i < 16; i++ {
		key[i] = raw[15-i]
	}
	return key
}

// hexByte parses a two-character hex string. Local helper to avoid
// pulling in strconv just for one call site.
func hexByte(s string) (byte, error) {
	if len(s) != 2 {
		return 0, fmt.Errorf("hex: bad length %d", len(s))
	}
	hi, err := hexNibble(s[0])
	if err != nil {
		return 0, err
	}
	lo, err := hexNibble(s[1])
	if err != nil {
		return 0, err
	}
	return hi<<4 | lo, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("hex: bad char %q", c)
}
