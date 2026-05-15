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
	keywords string
	comments string
	company  string
	username string

	seqCounters map[string]int
	bookmarks   map[string]string
	// docProperties indexes custom + standard doc properties so the
	// DOCPROPERTY field can resolve `{ DOCPROPERTY "AppVersion" }`.
	docProperties map[string]string
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
	case "CREATEDATE", "SAVEDATE", "PRINTDATE":
		// We don't track docProps/core.xml's per-event timestamps yet;
		// best-effort: use "now" as a reasonable stand-in. Falls back to
		// the cached Word result if for some reason vars.now isn't set.
		if !vars.now.IsZero() {
			return vars.now.Format("2006-01-02"), true
		}
	case "EDITTIME":
		// Total editing time in minutes — we don't have this metric; let
		// Word's cached value through.
		return "", false
	case "FILENAME":
		if vars.filename != "" {
			return vars.filename, true
		}
	case "USERNAME":
		if vars.username != "" {
			return vars.username, true
		}
		// Fall through to the author when USERNAME is unset — close enough
		// for most templates.
		if vars.author != "" {
			return vars.author, true
		}
	case "USERINITIALS":
		// Approximate initials from the username/author.
		if name := firstNonEmpty(vars.username, vars.author); name != "" {
			return initialsOf(name), true
		}
	case "AUTHOR":
		if vars.author != "" {
			return vars.author, true
		}
	case "LASTSAVEDBY":
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
	case "PAGEREF":
		// PAGEREF resolves to the page number of a bookmark. We don't
		// have a bookmark→page index, so we surface the bookmark text
		// itself when available; otherwise fall through to the cached
		// Word result (which usually has the correct page).
		if arg != "" && vars.bookmarks != nil {
			if text, ok := vars.bookmarks[arg]; ok && text != "" {
				return text, true
			}
		}
		return "", false
	case "NOTEREF":
		// NOTEREF resolves to a footnote/endnote reference number. We
		// don't track these separately; surface the bookmark text when
		// possible, else let the cached result win.
		if arg != "" && vars.bookmarks != nil {
			if text, ok := vars.bookmarks[arg]; ok && text != "" {
				return text, true
			}
		}
		return "", false
	case "STYLEREF":
		// STYLEREF prints the most-recent text styled with the named
		// style on the current page (used for running heads). We don't
		// have a per-page style index; fall through to the cached value.
		return "", false
	case "TITLE":
		if vars.title != "" {
			return vars.title, true
		}
	case "SUBJECT":
		if vars.subject != "" {
			return vars.subject, true
		}
	case "KEYWORDS":
		if vars.keywords != "" {
			return vars.keywords, true
		}
	case "COMMENTS":
		if vars.comments != "" {
			return vars.comments, true
		}
	case "COMPANY":
		if vars.company != "" {
			return vars.company, true
		}
	case "DOCPROPERTY":
		if arg != "" && vars.docProperties != nil {
			if v, ok := vars.docProperties[arg]; ok && v != "" {
				return v, true
			}
		}
		return "", false
	case "MERGEFIELD":
		// MERGEFIELD names a mail-merge column. Word caches the rendered
		// merge value in the result region (e.g. "John Smith"); we let
		// that flow through. Returning a placeholder here would override
		// the cached value, which breaks templates that ship pre-merged.
		return "", false
	case "FORMTEXT":
		// FORMTEXT shows the result region's content as-is — return ""
		// + false so the result region's text streams through normally.
		return "", false
	case "FORMCHECKBOX":
		// Checkbox: cached result is empty (Word draws the box from a
		// separate FFData blob). Surface ☐ as a visible placeholder.
		return "☐", true
	case "FORMDROPDOWN":
		// Dropdown: same situation as FORMCHECKBOX — surface ▾ as the
		// "selected value" placeholder when no result was cached.
		return "▾", true
	case "QUOTE":
		// QUOTE simply emits its argument as text.
		if arg != "" {
			return strings.Trim(arg, `"`), true
		}
	case "IF":
		// IF is a conditional expression; we don't evaluate it. Let the
		// cached result win.
		return "", false
	case "INCLUDETEXT", "INCLUDEPICTURE":
		// External include — we can't resolve the relationship target as
		// arbitrary content; let cached result win.
		return "", false
	case "TOC", "INDEX", "TOA":
		// Table-of-contents, index, table-of-authorities — Word caches
		// the rendered TOC into the result region as plain text + line
		// breaks. We just let the cached content flow through.
		return "", false
	case "ADDRESSBLOCK", "GREETINGLINE", "MACROBUTTON", "AUTOTEXT", "AUTOTEXTLIST":
		// Mail-merge / interactive elements with cached display text.
		return "", false
	case "EQ":
		// Legacy equation field — Word stores the typeset glyphs in the
		// result region. Let those through.
		return "", false
	case "HYPERLINK":
		return "", false
	}
	return "", false
}

// initialsOf extracts a 2-3 letter initials string from a full name.
// "Alice Wonder Land" → "AWL". Falls back to the whole name if it has
// no spaces.
func initialsOf(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(p)
		b.WriteRune(r[0])
	}
	return strings.ToUpper(b.String())
}
