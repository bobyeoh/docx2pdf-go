package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// chartZipFile builds a tiny zip containing one file at the given name
// with the given XML content, and returns the *zip.File entry pointing
// at it. This lets us exercise extractChartText against a realistic
// zip.File without needing the rest of the docx machinery.
func chartZipFile(t *testing.T, xml string) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("chart1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File[0]
}

func TestExtractChartText_SpacesBetweenBursts(t *testing.T) {
	// Title + axis labels in separate elements. Without spacing between
	// them the result reads "SalesQ1Q2" — wrong. We want them separated.
	xml := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="x">
  <c:title><c:tx><c:rich><a:p><a:r><a:t>Sales</a:t></a:r></a:p></c:rich></c:tx></c:title>
  <c:plotArea><c:cat><c:strRef><c:strCache>
    <c:pt><c:v>Q1</c:v></c:pt>
    <c:pt><c:v>Q2</c:v></c:pt>
  </c:strCache></c:strRef></c:cat></c:plotArea>
</c:chartSpace>`
	got, err := extractChartText(chartZipFile(t, xml))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Sales", "Q1", "Q2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	// And they must NOT be concatenated.
	if strings.Contains(got, "SalesQ1") || strings.Contains(got, "Q1Q2") {
		t.Errorf("text bursts concatenated without separator: %q", got)
	}
}

func TestExtractChartText_Empty(t *testing.T) {
	xml := `<?xml version="1.0"?><c:chartSpace xmlns:c="x"/>`
	got, err := extractChartText(chartZipFile(t, xml))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty chart returned %q, want empty", got)
	}
}

func TestExtractChartText_WhitespaceOnly(t *testing.T) {
	// CharData consisting purely of whitespace must not produce spurious
	// space-only entries that would expand the output.
	xml := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="x">
  <c:title>   </c:title>
  <c:plotArea>
    <c:cat><c:v>Real</c:v></c:cat>
  </c:plotArea>
</c:chartSpace>`
	got, err := extractChartText(chartZipFile(t, xml))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Real" {
		t.Errorf("whitespace-only chunks leaked: got %q want %q", got, "Real")
	}
}
