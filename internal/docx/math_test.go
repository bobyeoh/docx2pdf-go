package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

// runMath parses an OMML fragment and returns the rendered Run slice.
// Each test supplies a single OMML element as its top-level `<m:oMath>`
// (or similar) — runMath strips the namespace prefix so handlers see
// local element names.
func runMath(t *testing.T, fragment string) []Run {
	t.Helper()
	xmlSrc := `<root xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"><m:oMath>` +
		fragment + `</m:oMath></root>`
	dec := xml.NewDecoder(strings.NewReader(xmlSrc))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "oMath" {
			runs, err := renderMath(dec, se, RunProps{})
			if err != nil {
				t.Fatalf("renderMath: %v", err)
			}
			return runs
		}
	}
}

// collectText joins all run texts, prefixing super/subscript with
// {^...} or {_...} so the test can assert on a single string.
func collectText(runs []Run) string {
	var sb strings.Builder
	for _, r := range runs {
		switch r.Props.VertAlign {
		case "superscript":
			sb.WriteString("{^")
			sb.WriteString(r.Text)
			sb.WriteString("}")
		case "subscript":
			sb.WriteString("{_")
			sb.WriteString(r.Text)
			sb.WriteString("}")
		default:
			sb.WriteString(r.Text)
		}
	}
	return sb.String()
}

// TestRenderMath_Superscript covers m:sSup — base + super.
// Expected: "x" + super "2".
func TestRenderMath_Superscript(t *testing.T) {
	runs := runMath(t, `
		<m:sSup>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
			<m:sup><m:r><m:t>2</m:t></m:r></m:sup>
		</m:sSup>`)
	got := collectText(runs)
	want := "x{^2}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRenderMath_Subscript covers m:sSub.
func TestRenderMath_Subscript(t *testing.T) {
	runs := runMath(t, `
		<m:sSub>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
			<m:sub><m:r><m:t>i</m:t></m:r></m:sub>
		</m:sSub>`)
	if got := collectText(runs); got != "x{_i}" {
		t.Errorf("got %q, want x{_i}", got)
	}
}

// TestRenderMath_SubSup covers m:sSubSup — sub and super on same base.
func TestRenderMath_SubSup(t *testing.T) {
	runs := runMath(t, `
		<m:sSubSup>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
			<m:sub><m:r><m:t>1</m:t></m:r></m:sub>
			<m:sup><m:r><m:t>2</m:t></m:r></m:sup>
		</m:sSubSup>`)
	if got := collectText(runs); got != "x{^2}{_1}" {
		t.Errorf("got %q, want x{^2}{_1}", got)
	}
}

// TestRenderMath_Fraction renders as "(num)/(den)".
func TestRenderMath_Fraction(t *testing.T) {
	runs := runMath(t, `
		<m:f>
			<m:num><m:r><m:t>a</m:t></m:r></m:num>
			<m:den><m:r><m:t>b</m:t></m:r></m:den>
		</m:f>`)
	if got := collectText(runs); got != "(a)/(b)" {
		t.Errorf("got %q, want (a)/(b)", got)
	}
}

// TestRenderMath_Radical_SquareRoot covers m:rad with no degree.
func TestRenderMath_Radical_SquareRoot(t *testing.T) {
	runs := runMath(t, `
		<m:rad>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
		</m:rad>`)
	if got := collectText(runs); got != "√(x)" {
		t.Errorf("got %q, want √(x)", got)
	}
}

// TestRenderMath_Radical_WithDegree covers m:rad with m:deg → cube
// root etc. — degree is emitted as a superscript prefix.
func TestRenderMath_Radical_WithDegree(t *testing.T) {
	runs := runMath(t, `
		<m:rad>
			<m:deg><m:r><m:t>3</m:t></m:r></m:deg>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
		</m:rad>`)
	if got := collectText(runs); got != "{^3}√(x)" {
		t.Errorf("got %q, want {^3}√(x)", got)
	}
}

// TestRenderMath_NarySum covers m:nary with custom operator and
// bounds.
func TestRenderMath_NarySum(t *testing.T) {
	runs := runMath(t, `
		<m:nary>
			<m:naryPr><m:chr m:val="∑"/></m:naryPr>
			<m:sub><m:r><m:t>i=1</m:t></m:r></m:sub>
			<m:sup><m:r><m:t>n</m:t></m:r></m:sup>
			<m:e><m:r><m:t>i</m:t></m:r></m:e>
		</m:nary>`)
	if got := collectText(runs); got != "∑{_i=1}{^n}i" {
		t.Errorf("got %q, want ∑{_i=1}{^n}i", got)
	}
}

// TestRenderMath_Delimiters wraps the argument in parens by default.
func TestRenderMath_Delimiters(t *testing.T) {
	runs := runMath(t, `
		<m:d>
			<m:e><m:r><m:t>a+b</m:t></m:r></m:e>
		</m:d>`)
	if got := collectText(runs); got != "(a+b)" {
		t.Errorf("got %q, want (a+b)", got)
	}
}

// TestRenderMath_Function covers m:func — emits "fName(arg)".
func TestRenderMath_Function(t *testing.T) {
	runs := runMath(t, `
		<m:func>
			<m:fName><m:r><m:t>sin</m:t></m:r></m:fName>
			<m:e><m:r><m:t>x</m:t></m:r></m:e>
		</m:func>`)
	if got := collectText(runs); got != "sin(x)" {
		t.Errorf("got %q, want sin(x)", got)
	}
}

// TestRenderMath_Matrix covers m:m with two rows.
func TestRenderMath_Matrix(t *testing.T) {
	runs := runMath(t, `
		<m:m>
			<m:mr>
				<m:e><m:r><m:t>a</m:t></m:r></m:e>
				<m:e><m:r><m:t>b</m:t></m:r></m:e>
			</m:mr>
			<m:mr>
				<m:e><m:r><m:t>c</m:t></m:r></m:e>
				<m:e><m:r><m:t>d</m:t></m:r></m:e>
			</m:mr>
		</m:m>`)
	if got := collectText(runs); got != "[ a, b ; c, d ]" {
		t.Errorf("got %q, want [ a, b ; c, d ]", got)
	}
}

// TestRenderMath_PlainRun covers the simplest case — m:r/m:t emits a
// plain italic run with no VertAlign.
func TestRenderMath_PlainRun(t *testing.T) {
	runs := runMath(t, `<m:r><m:t>hello</m:t></m:r>`)
	if len(runs) != 1 || runs[0].Text != "hello" {
		t.Errorf("got %+v, want one run 'hello'", runs)
	}
	if !runs[0].Props.Italic {
		t.Errorf("math run not italic")
	}
}
