package docx

import (
	"encoding/xml"
	"strings"
)

// OpenDoPE (Open Document Processing Engine) is a conventional layer on top
// of OOXML SDT content controls that adds:
//
//   - w:dataBinding (XPath) — already handled in sdt.go
//   - w:tag with annotations:
//       od:xpath=adX1       (alternate binding form, looked up via opendope.xml)
//       od:condition=adC1   (drop SDT content if XPath returns falsy)
//       od:repeat=adR1      (clone SDT content per node-set match)
//
// The "adX1" identifier resolves through customXml/itemN.xml, which carries
// an <od:xpaths> list:
//
//   <od:xpaths>
//     <od:xpath id="adX1"><od:dataBinding xpath="/x/y/@z"/></od:xpath>
//     ...
//   </od:xpaths>
//
// We parse this list at load time and stash it on the doc so the SDT
// decoder can resolve markers without a second pass over the package.

// OpenDoPEXPath maps an ID like "adX1" to its XPath string.
type OpenDoPEXPath struct {
	ID    string
	XPath string
}

// parseOpenDoPETag splits a w:tag value into the three known OpenDoPE
// keys. Each key is overwritten if already set so the caller can chain
// multiple tag entries (rare but tolerated). Format is `&` separated.
func parseOpenDoPETag(val, prevCond, prevRepeat, prevXpath string) (cond, rep, xpath string) {
	cond, rep, xpath = prevCond, prevRepeat, prevXpath
	for _, pair := range strings.Split(val, "&") {
		key, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "od:condition":
			cond = strings.TrimSpace(v)
		case "od:repeat":
			rep = strings.TrimSpace(v)
		case "od:xpath":
			xpath = strings.TrimSpace(v)
		}
	}
	return cond, rep, xpath
}

// parseOpenDoPEXPaths walks a customXml/itemN.xml payload that follows the
// OpenDoPE schema and returns the id→xpath table. Returns nil when the
// payload isn't an OpenDoPE xpath list.
func parseOpenDoPEXPaths(data []byte) map[string]string {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	out := map[string]string{}
	inList := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "xpaths":
				inList = true
			case "xpath":
				if !inList {
					continue
				}
				id := ""
				for _, a := range t.Attr {
					if a.Name.Local == "id" {
						id = a.Value
					}
				}
				// Walk children for dataBinding xpath.
				depth := 1
				bound := ""
				for depth > 0 {
					inner, err := dec.Token()
					if err != nil {
						break
					}
					switch it := inner.(type) {
					case xml.StartElement:
						depth++
						if it.Name.Local == "dataBinding" {
							for _, a := range it.Attr {
								if a.Name.Local == "xpath" {
									bound = a.Value
								}
							}
						}
					case xml.EndElement:
						depth--
					}
				}
				if id != "" && bound != "" {
					out[id] = bound
				}
			}
		case xml.EndElement:
			if t.Name.Local == "xpaths" {
				inList = false
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveOpenDoPEXPath looks up the XPath for an OpenDoPE id and evaluates
// it against the doc's custom XML stores. Returns ("", false) when the id
// isn't bound or the XPath has no match.
func resolveOpenDoPEXPath(doc *Document, id string) (string, bool) {
	if doc == nil || id == "" {
		return "", false
	}
	if doc.OpenDoPEXPaths == nil {
		return "", false
	}
	xpath, ok := doc.OpenDoPEXPaths[id]
	if !ok {
		return "", false
	}
	return resolveXPath(doc.CustomXMLRoots, xpath)
}

// applyRepeatContext rewrites an XPath to honor the renderer's
// open-repeat stack. For each active repeat frame whose xpathPrefix is
// a prefix of the given path, the prefix is replaced with
// "<prefix>[index]" so the lookup hits the i-th iteration. Returns the
// original xpath unchanged when no repeat frame applies. This is what
// lets a single SDT template render different values for each clone in
// a repeat group.
func applyRepeatContext(xpath string, stack []openDopeRepeatFrame) string {
	if xpath == "" || len(stack) == 0 {
		return xpath
	}
	for _, f := range stack {
		if f.xpathPrefix == "" || f.index <= 0 {
			continue
		}
		if !strings.HasPrefix(xpath, f.xpathPrefix) {
			continue
		}
		rest := xpath[len(f.xpathPrefix):]
		// Only rewrite when the prefix ends at a path boundary — we
		// must not splice inside an element name.
		if rest != "" && rest[0] != '/' && rest[0] != '[' && rest[0] != '@' {
			continue
		}
		// If the prefix already has a positional predicate in the
		// source (e.g. the writer hardcoded "[1]"), leave it alone.
		if strings.HasPrefix(rest, "[") {
			continue
		}
		return f.xpathPrefix + "[" + intStr(f.index) + "]" + rest
	}
	return xpath
}

// resolveOpenDoPECondition evaluates a condition ID. The returned bool is
// true when the bound value is non-empty AND not literally "false" / "0".
func resolveOpenDoPECondition(doc *Document, id string) bool {
	v, ok := resolveOpenDoPEXPath(doc, id)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "n":
		return false
	}
	return true
}

// resolveOpenDoPERepeatCount returns N for a repeat ID, where N is the
// number of element matches the bound XPath has across the custom XML
// stores. Returns 0 when nothing matches.
//
// Note: the SDT clone path emits identical block content for each of
// the N iterations — we do not currently re-resolve nested XPath
// references against the i-th match. Templates that need per-iteration
// data binding should be pre-processed (e.g. via the docx4j OpenDoPE
// runner) before being handed to this renderer.
func resolveOpenDoPERepeatCount(doc *Document, id string) int {
	if doc == nil || id == "" || doc.OpenDoPEXPaths == nil {
		return 0
	}
	xpath, ok := doc.OpenDoPEXPaths[id]
	if !ok {
		return 0
	}
	if n := countXPathMatches(doc.CustomXMLRoots, xpath); n > 0 {
		return n
	}
	// Fallback for documents whose customXml store doesn't contain the
	// path verbatim (the writer used an alias prefix etc.): if at least
	// one match resolves to text, treat as a one-iteration repeat.
	if _, ok := resolveXPath(doc.CustomXMLRoots, xpath); ok {
		return 1
	}
	return 0
}

// intStr is a tiny strconv.Itoa shim used by the loop above to avoid an
// import cycle with the larger reader.go.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
