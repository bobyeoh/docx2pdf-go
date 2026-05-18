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

// TestExtractChartStruct_NewKinds verifies area / bubble / radar chart
// kinds are recognized by the parser so the renderer can paint them.
func TestExtractChartStruct_NewKinds(t *testing.T) {
	cases := map[string]string{
		"area":   `<c:areaChart><c:ser><c:val><c:numRef><c:numCache><c:pt><c:v>1</c:v></c:pt></c:numCache></c:numRef></c:val></c:ser></c:areaChart>`,
		"bubble": `<c:bubbleChart><c:ser><c:val><c:numRef><c:numCache><c:pt><c:v>2</c:v></c:pt></c:numCache></c:numRef></c:val></c:ser></c:bubbleChart>`,
		"radar":  `<c:radarChart><c:ser><c:val><c:numRef><c:numCache><c:pt><c:v>3</c:v></c:pt></c:numCache></c:numRef></c:val></c:ser></c:radarChart>`,
	}
	for wantKind, body := range cases {
		xml := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart">
  <c:chart><c:plotArea>` + body + `</c:plotArea></c:chart>
</c:chartSpace>`
		got, err := extractChartStruct(chartZipFile(t, xml))
		if err != nil {
			t.Fatalf("kind=%s parse: %v", wantKind, err)
		}
		if got.Kind != wantKind {
			t.Errorf("kind=%s: got Kind=%q", wantKind, got.Kind)
		}
	}
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

func TestExtractChartStruct_ColumnAndPie(t *testing.T) {
	// barChart with vertical bars + categories + two series.
	col := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="x" xmlns:a="y">
  <c:chart>
    <c:title><c:tx><c:rich><a:p><a:r><a:t>Sales</a:t></a:r></a:p></c:rich></c:tx></c:title>
    <c:plotArea>
      <c:barChart>
        <c:barDir val="col"/>
        <c:ser>
          <c:tx><c:strRef><c:strCache><c:pt><c:v>2023</c:v></c:pt></c:strCache></c:strRef></c:tx>
          <c:cat><c:strRef><c:strCache>
            <c:pt><c:v>Q1</c:v></c:pt><c:pt><c:v>Q2</c:v></c:pt>
          </c:strCache></c:strRef></c:cat>
          <c:val><c:numRef><c:numCache>
            <c:pt><c:v>10</c:v></c:pt><c:pt><c:v>20</c:v></c:pt>
          </c:numCache></c:numRef></c:val>
        </c:ser>
      </c:barChart>
    </c:plotArea>
  </c:chart>
</c:chartSpace>`
	got, err := extractChartStruct(chartZipFile(t, col))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "column" {
		t.Errorf("Kind = %q, want column", got.Kind)
	}
	if got.Title != "Sales" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(got.Series) != 1 || got.Series[0].Name != "2023" {
		t.Fatalf("series = %+v", got.Series)
	}
	if len(got.Series[0].Values) != 2 || got.Series[0].Values[0] != 10 || got.Series[0].Values[1] != 20 {
		t.Errorf("values = %v", got.Series[0].Values)
	}
	if len(got.Categories) != 2 || got.Categories[0] != "Q1" {
		t.Errorf("categories = %v", got.Categories)
	}

	// pieChart with a single series.
	pie := `<?xml version="1.0"?>
<c:chartSpace xmlns:c="x" xmlns:a="y">
  <c:chart><c:plotArea>
    <c:pieChart>
      <c:ser>
        <c:cat><c:strRef><c:strCache>
          <c:pt><c:v>A</c:v></c:pt><c:pt><c:v>B</c:v></c:pt><c:pt><c:v>C</c:v></c:pt>
        </c:strCache></c:strRef></c:cat>
        <c:val><c:numRef><c:numCache>
          <c:pt><c:v>50</c:v></c:pt><c:pt><c:v>30</c:v></c:pt><c:pt><c:v>20</c:v></c:pt>
        </c:numCache></c:numRef></c:val>
      </c:ser>
    </c:pieChart>
  </c:plotArea></c:chart>
</c:chartSpace>`
	got, err = extractChartStruct(chartZipFile(t, pie))
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "pie" {
		t.Errorf("Kind = %q, want pie", got.Kind)
	}
	if len(got.Series) != 1 || len(got.Series[0].Values) != 3 {
		t.Fatalf("series = %+v", got.Series)
	}
	if got.Series[0].Values[0] != 50 {
		t.Errorf("first value = %v", got.Series[0].Values[0])
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
