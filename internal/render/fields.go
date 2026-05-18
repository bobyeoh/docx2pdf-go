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

	// Doc properties exposed by name to DOCPROPERTY. Populated once
	// during renderer init from doc.Properties; per-page rebuilds keep
	// the same map reference. Keys are case-insensitive — see
	// docPropertyByName.
	docProps map[string]string

	// w:docVars from settings.xml, surfaced to DOCVARIABLE. Same
	// case-insensitive lookup semantics as docProps.
	docVars map[string]string

	// Timestamps surfaced to SAVEDATE / CREATEDATE / PRINTDATE.
	created     time.Time
	modified    time.Time
	lastPrinted time.Time

	// Edit-minutes for EDITTIME.
	totalTime int

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
	case "DOCPROPERTY":
		if v, ok := docPropertyByName(vars.docProps, arg); ok {
			return v, true
		}
	case "DOCVARIABLE":
		if v, ok := docPropertyByName(vars.docVars, arg); ok {
			return v, true
		}
	case "MERGEFIELD":
		if arg != "" {
			// Without a merge data source we render the field name in
			// the French double-angle quotes Word uses to mark unfilled
			// merge fields. Matches what Word shows in "view field
			// codes off" mode when no recipient is selected.
			return "«" + arg + "»", true
		}
	case "SAVEDATE":
		if v, ok := formatDateField(vars.modified); ok {
			return v, true
		}
	case "CREATEDATE":
		if v, ok := formatDateField(vars.created); ok {
			return v, true
		}
	case "PRINTDATE":
		if v, ok := formatDateField(vars.lastPrinted); ok {
			return v, true
		}
	case "EDITTIME":
		if vars.totalTime > 0 {
			return strconv.Itoa(vars.totalTime), true
		}
	case "NUMCHARS":
		if v, ok := docPropertyByName(vars.docProps, "Characters"); ok {
			return v, true
		}
	case "NUMWORDS":
		if v, ok := docPropertyByName(vars.docProps, "Words"); ok {
			return v, true
		}
	case "KEYWORDS":
		if v, ok := docPropertyByName(vars.docProps, "Keywords"); ok {
			return v, true
		}
	case "COMMENTS":
		// COMMENTS field renders the "Comments" core property (which we
		// store under Description), NOT reviewer comments.
		if v, ok := docPropertyByName(vars.docProps, "Comments"); ok {
			return v, true
		}
	case "CATEGORY":
		if v, ok := docPropertyByName(vars.docProps, "Category"); ok {
			return v, true
		}
	case "COMPANY":
		if v, ok := docPropertyByName(vars.docProps, "Company"); ok {
			return v, true
		}
	case "MANAGER":
		if v, ok := docPropertyByName(vars.docProps, "Manager"); ok {
			return v, true
		}
	case "LASTSAVEDBY":
		if v, ok := docPropertyByName(vars.docProps, "LastModifiedBy"); ok {
			return v, true
		}
	case "REVNUM":
		if v, ok := docPropertyByName(vars.docProps, "Revision"); ok {
			return v, true
		}
	}
	return "", false
}

// docPropertyByName looks up a doc-property value by case-insensitive
// name. Returns ("", false) when the map is nil, the key is missing,
// or the value is empty — caller falls back to the cached Word result.
func docPropertyByName(m map[string]string, name string) (string, bool) {
	if m == nil || name == "" {
		return "", false
	}
	key := strings.ToLower(name)
	if v, ok := m[key]; ok && v != "" {
		return v, true
	}
	return "", false
}

// buildDocPropertyMap builds a lower-cased name → value map for the
// standard core/app doc properties so DOCPROPERTY and friends can look
// them up by Word's canonical names ("Title", "Author", "Comments",
// "Pages", etc.). The author override takes precedence so the field
// agrees with the value in the PDF /Info dictionary when the caller
// passes Options.Author.
//
// Integer/time fields are formatted up-front: callers want strings.
// Time uses the same YYYY-MM-DD layout as SAVEDATE / CREATEDATE.
func buildDocPropertyMap(p *docx.Properties, authorOverride string) map[string]string {
	if p == nil {
		return map[string]string{}
	}
	m := map[string]string{}
	set := func(name, value string) {
		if value != "" {
			m[strings.ToLower(name)] = value
		}
	}
	setInt := func(name string, value int) {
		if value > 0 {
			m[strings.ToLower(name)] = strconv.Itoa(value)
		}
	}
	setTime := func(name string, t time.Time) {
		if !t.IsZero() {
			m[strings.ToLower(name)] = t.Format("2006-01-02")
		}
	}
	set("Title", p.Title)
	set("Author", firstNonEmpty(authorOverride, p.Author))
	set("Subject", p.Subject)
	// "Comments" is the Word display name for dc:description.
	set("Comments", p.Description)
	set("Description", p.Description)
	set("Keywords", p.Keywords)
	set("Category", p.Category)
	set("LastModifiedBy", p.LastModifiedBy)
	set("Revision", p.Revision)
	set("Company", p.Company)
	set("Manager", p.Manager)
	set("Application", p.Application)
	setInt("Pages", p.Pages)
	setInt("Words", p.Words)
	setInt("Characters", p.Characters)
	setInt("Lines", p.Lines)
	setInt("TotalTime", p.TotalTime)
	setTime("Created", p.Created)
	setTime("Modified", p.Modified)
	setTime("LastPrinted", p.LastPrinted)
	return m
}

// formatDateField renders a Properties timestamp for SAVEDATE /
// CREATEDATE / PRINTDATE. We ignore the `\@ "format"` switch and use
// a fixed YYYY-MM-DD layout — matching how DATE / TIME already render.
// Word users who need a specific layout still see the cached value
// since we return (_, false) on zero time, letting the snapshot win.
func formatDateField(t time.Time) (string, bool) {
	if t.IsZero() {
		return "", false
	}
	return t.Format("2006-01-02"), true
}
