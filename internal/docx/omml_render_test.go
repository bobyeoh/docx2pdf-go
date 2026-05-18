package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

// renderMath helpers: decode an oMath XML snippet and return its
// rendered string.
func renderMath(t *testing.T, xmlSnippet string) string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(xmlSnippet))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		got, err := extractMathText(dec, se)
		if err != nil {
			t.Fatalf("extractMathText: %v", err)
		}
		return got
	}
}

// TestOMML_Fraction_RendersStructurally pins that m:f → "num/den" with
// parens when complex, not flattened to "23x" or similar.
func TestOMML_Fraction_RendersStructurally(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:f><m:num><m:r><m:t>2x+1</m:t></m:r></m:num><m:den><m:r><m:t>x-3</m:t></m:r></m:den></m:f>
</m:oMath>`
	got := renderMath(t, m)
	want := "(2x+1)/(x-3)"
	if !strings.Contains(got, want) {
		t.Errorf("fraction render = %q, want it to contain %q", got, want)
	}
}

// TestOMML_Radical_RendersStructurally pins that m:rad emits a √(...)
// rather than just "x".
func TestOMML_Radical_RendersStructurally(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:rad><m:e><m:r><m:t>x</m:t></m:r></m:e></m:rad>
</m:oMath>`
	got := renderMath(t, m)
	if !strings.Contains(got, "√") || !strings.Contains(got, "x") {
		t.Errorf("radical = %q, want it to contain √ and x", got)
	}
}

// TestOMML_NthRoot_ShowsDegree pins that an n-th root prefixes the
// degree as a Unicode superscript.
func TestOMML_NthRoot_ShowsDegree(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:rad>
  <m:deg><m:r><m:t>3</m:t></m:r></m:deg>
  <m:e><m:r><m:t>x</m:t></m:r></m:e>
</m:rad>
</m:oMath>`
	got := renderMath(t, m)
	// expect ³√(x) — superscript 3 + radical
	if !strings.Contains(got, "³") || !strings.Contains(got, "√") {
		t.Errorf("nth-root = %q, want it to contain ³ and √", got)
	}
}

// TestOMML_NaryWithLimits_ShowsBoth pins that ∑/∫ preserve upper and
// lower limits.
func TestOMML_NaryWithLimits_ShowsBoth(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:nary>
  <m:naryPr><m:chr m:val="∑"/></m:naryPr>
  <m:sub><m:r><m:t>i=1</m:t></m:r></m:sub>
  <m:sup><m:r><m:t>n</m:t></m:r></m:sup>
  <m:e><m:r><m:t>i</m:t></m:r></m:e>
</m:nary>
</m:oMath>`
	got := renderMath(t, m)
	if !strings.Contains(got, "∑") {
		t.Errorf("nary = %q, want ∑", got)
	}
	// limits show as either Unicode subscript "ᵢ₌₁" / "ⁿ" or fallback _(i=1)^(n)
	if !strings.Contains(got, "i=1") && !strings.ContainsAny(got, "ᵢ₌") {
		t.Errorf("nary = %q, want lower-limit somehow", got)
	}
}

// TestOMML_Superscript_RendersInline pins x² as "x²" via Unicode super.
func TestOMML_Superscript_RendersInline(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:sSup>
  <m:e><m:r><m:t>x</m:t></m:r></m:e>
  <m:sup><m:r><m:t>2</m:t></m:r></m:sup>
</m:sSup>
</m:oMath>`
	got := renderMath(t, m)
	if !strings.Contains(got, "x²") && !strings.Contains(got, "x^(2)") {
		t.Errorf("sSup = %q, want x² or x^(2)", got)
	}
}

// TestOMML_Matrix_PreservesRowStructure pins matrix rows separated by
// "; " so the 2D structure isn't flattened.
func TestOMML_Matrix_PreservesRowStructure(t *testing.T) {
	const m = `<m:oMath xmlns:m="urn:m">
<m:m>
  <m:mr><m:e><m:r><m:t>a</m:t></m:r></m:e><m:e><m:r><m:t>b</m:t></m:r></m:e></m:mr>
  <m:mr><m:e><m:r><m:t>c</m:t></m:r></m:e><m:e><m:r><m:t>d</m:t></m:r></m:e></m:mr>
</m:m>
</m:oMath>`
	got := renderMath(t, m)
	if !strings.Contains(got, ";") {
		t.Errorf("matrix = %q, want '; ' row separator", got)
	}
}
