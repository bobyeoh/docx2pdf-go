package render

import (
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func makeTable(rows [][]string) *docx.Table {
	t := &docx.Table{}
	for _, row := range rows {
		tr := docx.TableRow{}
		for _, text := range row {
			cell := docx.TableCell{
				GridSpan: 1,
				Blocks: []docx.Block{
					docx.Paragraph{Runs: []docx.Run{{Text: text}}},
				},
			}
			tr.Cells = append(tr.Cells, cell)
		}
		t.Rows = append(t.Rows, tr)
	}
	return t
}

func TestFormula_SumAbove(t *testing.T) {
	tbl := makeTable([][]string{{"10"}, {"20"}, {"30"}, {""}})
	ctx := &tableContext{table: tbl, row: 3, col: 0}
	v, ok := evalTableFormula("=SUM(ABOVE)", ctx)
	if !ok || v != 60 {
		t.Errorf("SUM(ABOVE) = (%v,%v), want (60,true)", v, ok)
	}
}

func TestFormula_SumLeft(t *testing.T) {
	tbl := makeTable([][]string{{"5", "7", "13", ""}})
	ctx := &tableContext{table: tbl, row: 0, col: 3}
	v, ok := evalTableFormula("=SUM(LEFT)", ctx)
	if !ok || v != 25 {
		t.Errorf("SUM(LEFT) = (%v,%v), want (25,true)", v, ok)
	}
}

func TestFormula_AverageBelow(t *testing.T) {
	tbl := makeTable([][]string{{""}, {"4"}, {"6"}, {"8"}})
	ctx := &tableContext{table: tbl, row: 0, col: 0}
	v, ok := evalTableFormula("=AVERAGE(BELOW)", ctx)
	if !ok || v != 6 {
		t.Errorf("AVERAGE(BELOW) = (%v,%v), want (6,true)", v, ok)
	}
}

func TestFormula_A1Reference(t *testing.T) {
	tbl := makeTable([][]string{{"3", "4"}, {"5", ""}})
	ctx := &tableContext{table: tbl, row: 1, col: 1}
	v, ok := evalTableFormula("=A1*B1+A2", ctx)
	if !ok || v != 17 {
		t.Errorf("=A1*B1+A2 = (%v,%v), want (17,true)", v, ok)
	}
}

func TestFormula_Range(t *testing.T) {
	tbl := makeTable([][]string{{"1", "2", "3"}, {"4", "5", "6"}, {"", "", ""}})
	ctx := &tableContext{table: tbl, row: 2, col: 2}
	v, ok := evalTableFormula("=SUM(A1:C2)", ctx)
	if !ok || v != 21 {
		t.Errorf("SUM(A1:C2) = (%v,%v), want (21,true)", v, ok)
	}
}

func TestFormula_CurrencyAndCommas(t *testing.T) {
	tbl := makeTable([][]string{{"$1,200"}, {"€800"}, {""}})
	ctx := &tableContext{table: tbl, row: 2, col: 0}
	v, ok := evalTableFormula("=SUM(ABOVE)", ctx)
	if !ok || v != 2000 {
		t.Errorf("SUM(ABOVE) with currency = (%v,%v), want (2000,true)", v, ok)
	}
}

func TestFormula_ParensAndDivision(t *testing.T) {
	tbl := makeTable([][]string{{"10", "5"}, {""}})
	ctx := &tableContext{table: tbl, row: 1, col: 0}
	v, ok := evalTableFormula("=(A1+B1)/3", ctx)
	if !ok || v != 5 {
		t.Errorf("(A1+B1)/3 = (%v,%v), want (5,true)", v, ok)
	}
}

func TestFormula_AbsAndRound(t *testing.T) {
	tbl := makeTable([][]string{{"7.456"}, {""}})
	ctx := &tableContext{table: tbl, row: 1, col: 0}
	v, ok := evalTableFormula("=ROUND(A1,1)", ctx)
	if !ok || v != 7.5 {
		t.Errorf("ROUND(A1,1) = (%v,%v), want (7.5,true)", v, ok)
	}
	v, ok = evalTableFormula("=ABS(-A1)", ctx)
	if !ok || v != 7.456 {
		t.Errorf("ABS(-A1) = (%v,%v), want (7.456,true)", v, ok)
	}
}

func TestFormula_NoTableContext(t *testing.T) {
	v, ok := evalTableFormula("=5+3", nil)
	if !ok || v != 8 {
		t.Errorf("=5+3 = (%v,%v), want (8,true)", v, ok)
	}
	if _, ok := evalTableFormula("=A1", nil); ok {
		t.Error("A1 with nil ctx should miss")
	}
}

func TestFormula_FormatNumber(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{{60, "60"}, {-3, "-3"}, {7.5, "7.5"}, {0, "0"}}
	for _, c := range cases {
		got := formatFormulaNumber(c.in)
		if got != c.want {
			t.Errorf("formatFormulaNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseCellNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"42", 42, true},
		{"$1,234.56", 1234.56, true},
		{"(99)", -99, true},
		{"25%", 0.25, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseCellNumber(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseCellNumber(%q) = (%v,%v), want (%v,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestA1ColToIndex(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"A", 0, true},
		{"B", 1, true},
		{"Z", 25, true},
		{"AA", 26, true},
		{"AC", 28, true},
		{"", 0, false},
		{"A1", 0, false},
	}
	for _, c := range cases {
		got, ok := a1ColToIndex(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("a1ColToIndex(%q) = (%v,%v), want (%v,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSymbolFieldCode(t *testing.T) {
	got, ok := lookupFieldValueFull("SYMBOL", "61472", "SYMBOL 61472 \\f Wingdings", fieldVars{})
	if !ok {
		t.Fatal("SYMBOL: ok=false")
	}
	if got != string(rune(61472)) {
		t.Errorf("SYMBOL = %q, want code point 61472", got)
	}
	got, ok = lookupFieldValueFull("SYMBOL", "0xF0E0", "SYMBOL 0xF0E0 \\f Wingdings", fieldVars{})
	if !ok || got != string(rune(0xF0E0)) {
		t.Errorf("SYMBOL hex = (%q,%v), want %q", got, ok, string(rune(0xF0E0)))
	}
}

func TestListNumField(t *testing.T) {
	v := fieldVars{listNumCounters: map[string]int{}}
	for i := 1; i <= 3; i++ {
		got, ok := lookupFieldValueFull("LISTNUM", "MyList", "LISTNUM MyList", v)
		if !ok {
			t.Fatalf("LISTNUM #%d: ok=false", i)
		}
		want := []string{"1)", "2)", "3)"}[i-1]
		if got != want {
			t.Errorf("LISTNUM #%d = %q, want %q", i, got, want)
		}
	}
	got, _ := lookupFieldValueFull("LISTNUM", "Other", "LISTNUM Other \\s 10", v)
	if got != "10)" {
		t.Errorf("LISTNUM with \\s 10 = %q, want 10)", got)
	}
}

func TestSetFieldInstr(t *testing.T) {
	cases := []struct {
		in        string
		wantName  string
		wantValue string
	}{
		{`SET foo "bar"`, "foo", "bar"},
		{`SET ver "2.1.0"`, "ver", "2.1.0"},
		{"SET foo bar", "foo", "bar"},
		{"NOTSET foo bar", "", ""},
	}
	for _, c := range cases {
		name, value := setFieldInstr(c.in)
		if name != c.wantName || value != c.wantValue {
			t.Errorf("setFieldInstr(%q) = (%q,%q), want (%q,%q)",
				c.in, name, value, c.wantName, c.wantValue)
		}
	}
}

func TestFlattenFields_SETAndREF(t *testing.T) {
	runs := []docx.Run{
		{FieldBegin: true},
		{InstrText: `SET version "2.4.0"`},
		{FieldSep: true},
		{Text: ""},
		{FieldEnd: true},
		{Text: "v"},
		{FieldBegin: true},
		{InstrText: "REF version"},
		{FieldSep: true},
		{Text: "OLD"},
		{FieldEnd: true},
	}
	out := flattenFields(runs, fieldVars{setVars: map[string]string{}})
	var got string
	for _, r := range out {
		if !r.FieldBegin && !r.FieldSep && !r.FieldEnd && r.InstrText == "" {
			got += r.Text
		}
	}
	if got != "v2.4.0" {
		t.Errorf("SET+REF flatten = %q, want v2.4.0", got)
	}
}
