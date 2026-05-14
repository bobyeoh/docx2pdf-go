package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// firstNonEmpty returns the first non-empty string among its args.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// formatPageNumber returns the page-number string in the requested format
// after applying the section's "start at N" offset.
func formatPageNumber(page int, fmt string) string {
	if page < 1 {
		page = 1
	}
	switch fmt {
	case "upperRoman":
		return roman(page, true)
	case "lowerRoman":
		return roman(page, false)
	case "upperLetter":
		return alphaLabel(page, true)
	case "lowerLetter":
		return alphaLabel(page, false)
	}
	return strconv.Itoa(page)
}

// fieldVars supplies values for w:instrText codes. The zero value means
// "use the docx-cached field result as-is" — the body's default behavior
// since Word keeps a snapshot of the rendered text so even unsupported
// fields look right enough. Header/footer rendering overrides these per page.
type fieldVars struct {
	page     int
	numPages int
	pageFmt  string

	now      time.Time
	filename string
	author   string
	title    string
	subject  string

	seqCounters map[string]int
	bookmarks   map[string]string
}

// fieldCodeAndArgs splits an instrText like ` SEQ Figure \* ARABIC ` into the
// code ("SEQ") and the first non-switch argument.
func fieldCodeAndArgs(s string) (code, primary string) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", ""
	}
	code = strings.ToUpper(parts[0])
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "\\") {
			continue
		}
		p = strings.Trim(p, `"`)
		if p != "" {
			primary = p
			break
		}
	}
	return code, primary
}

// hyperlinkFieldInstr decodes a HYPERLINK instrText into (target, isAnchor).
// `\l` means the primary arg is an internal bookmark name, not a URL.
func hyperlinkFieldInstr(s string) (target string, isAnchor bool) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	skipNext := false
	for i := 1; i < len(parts); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		p := parts[i]
		switch p {
		case "\\l":
			isAnchor = true
			continue
		case "\\o", "\\t", "\\m", "\\n":
			skipNext = true
			continue
		}
		if strings.HasPrefix(p, "\\") {
			continue
		}
		target = strings.Trim(p, `"`)
		return target, isAnchor
	}
	return "", isAnchor
}

// flattenFields walks a paragraph's raw Run stream and resolves
// w:fldChar / w:instrText structure into plain text runs.
func flattenFields(runs []docx.Run, vars fieldVars) []docx.Run {
	type frame struct {
		instr       strings.Builder
		inResult    bool
		code        string
		arg         string
		substituted bool
		linkURL     string
		linkAnchor  string
	}
	var stack []*frame
	top := func() *frame {
		if len(stack) == 0 {
			return nil
		}
		return stack[len(stack)-1]
	}

	out := make([]docx.Run, 0, len(runs))
	for _, r := range runs {
		switch {
		case r.FieldBegin:
			stack = append(stack, &frame{})
		case r.FieldSep:
			if f := top(); f != nil {
				f.code, f.arg = fieldCodeAndArgs(f.instr.String())
				f.inResult = true
				if f.code == "HYPERLINK" {
					target, isAnchor := hyperlinkFieldInstr(f.instr.String())
					if isAnchor {
						f.linkAnchor = target
					} else {
						f.linkURL = target
					}
				}
			}
		case r.FieldEnd:
			if n := len(stack); n > 0 {
				stack = stack[:n-1]
			}
		case r.InstrText != "":
			if f := top(); f != nil && !f.inResult {
				f.instr.WriteString(r.InstrText)
			}
		default:
			f := top()
			if f == nil {
				out = append(out, r)
				continue
			}
			if !f.inResult {
				continue
			}
			if value, ok := lookupFieldValueWith(f.code, f.arg, vars); ok {
				if !f.substituted {
					rr := r
					rr.Text = value
					out = append(out, rr)
					f.substituted = true
				}
				continue
			}
			if f.linkURL != "" || f.linkAnchor != "" {
				rr := r
				if f.linkURL != "" {
					rr.LinkURL = f.linkURL
				}
				if f.linkAnchor != "" {
					rr.LinkAnchor = f.linkAnchor
				}
				out = append(out, rr)
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

func fieldCodeFromInstr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	return strings.ToUpper(s)
}

func lookupFieldValue(code string, vars fieldVars) (string, bool) {
	return lookupFieldValueWith(code, "", vars)
}

// lookupFieldValueWith resolves one field code+arg pair to its rendered
// value. Returning (_, false) lets the caller fall back to the cached Word
// result.
func lookupFieldValueWith(code, arg string, vars fieldVars) (string, bool) {
	switch code {
	case "PAGE":
		if vars.page > 0 {
			return formatPageNumber(vars.page, vars.pageFmt), true
		}
	case "NUMPAGES":
		if vars.numPages > 0 {
			return formatPageNumber(vars.numPages, vars.pageFmt), true
		}
	case "DATE":
		if !vars.now.IsZero() {
			return vars.now.Format("2006-01-02"), true
		}
	case "TIME":
		if !vars.now.IsZero() {
			return vars.now.Format("15:04"), true
		}
	case "FILENAME":
		if vars.filename != "" {
			return vars.filename, true
		}
	case "AUTHOR":
		if vars.author != "" {
			return vars.author, true
		}
	case "SEQ":
		if arg != "" && vars.seqCounters != nil {
			vars.seqCounters[arg]++
			return strconv.Itoa(vars.seqCounters[arg]), true
		}
	case "REF":
		if arg != "" && vars.bookmarks != nil {
			if text, ok := vars.bookmarks[arg]; ok && text != "" {
				return text, true
			}
		}
	case "TITLE":
		if vars.title != "" {
			return vars.title, true
		}
	case "SUBJECT":
		if vars.subject != "" {
			return vars.subject, true
		}
	case "HYPERLINK":
		return "", false
	}
	return "", false
}
