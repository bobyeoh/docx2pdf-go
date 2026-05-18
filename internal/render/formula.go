package render

import (
	"math"
	"strconv"
	"strings"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

func isFormulaCode(code string) bool {
	if code == "" {
		return false
	}
	if strings.HasPrefix(code, "=") {
		return true
	}
	return strings.EqualFold(code, "FORMULA")
}

func formulaExpression(code, arg, instrFull string) string {
	switch {
	case strings.HasPrefix(code, "="):
		if instrFull != "" {
			s := strings.TrimSpace(instrFull)
			if i := strings.Index(s, `\#`); i >= 0 {
				s = strings.TrimSpace(s[:i])
			}
			if i := strings.Index(s, `\*`); i >= 0 {
				s = strings.TrimSpace(s[:i])
			}
			return s
		}
		return code
	case strings.EqualFold(code, "FORMULA"):
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(instrFull), "FORMULA"))
		s = strings.TrimSpace(strings.TrimPrefix(s, "formula"))
		if i := strings.Index(s, `\#`); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		return s
	}
	return arg
}

func evalTableFormula(expr string, ctx *tableContext) (float64, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(expr), "="))
	p := &formulaParser{src: s, ctx: ctx}
	v, ok := p.parseExpr()
	if !ok || !p.atEnd() {
		return 0, false
	}
	return v, true
}

type formulaParser struct {
	src string
	pos int
	ctx *tableContext
}

func (p *formulaParser) atEnd() bool {
	for p.pos < len(p.src) {
		if !isFormulaSpace(p.src[p.pos]) {
			return false
		}
		p.pos++
	}
	return true
}

func (p *formulaParser) peek() byte {
	for p.pos < len(p.src) && isFormulaSpace(p.src[p.pos]) {
		p.pos++
	}
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *formulaParser) parseExpr() (float64, bool) {
	v, ok := p.parseTerm()
	if !ok {
		return 0, false
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			rhs, ok := p.parseTerm()
			if !ok {
				return 0, false
			}
			v += rhs
		case '-':
			p.pos++
			rhs, ok := p.parseTerm()
			if !ok {
				return 0, false
			}
			v -= rhs
		default:
			return v, true
		}
	}
}

func (p *formulaParser) parseTerm() (float64, bool) {
	v, ok := p.parseFactor()
	if !ok {
		return 0, false
	}
	for {
		switch p.peek() {
		case '*':
			p.pos++
			rhs, ok := p.parseFactor()
			if !ok {
				return 0, false
			}
			v *= rhs
		case '/':
			p.pos++
			rhs, ok := p.parseFactor()
			if !ok {
				return 0, false
			}
			if rhs == 0 {
				return 0, false
			}
			v /= rhs
		default:
			return v, true
		}
	}
}

func (p *formulaParser) parseFactor() (float64, bool) {
	c := p.peek()
	if c == 0 {
		return 0, false
	}
	if c == '-' {
		p.pos++
		v, ok := p.parseFactor()
		return -v, ok
	}
	if c == '+' {
		p.pos++
		return p.parseFactor()
	}
	if c == '(' {
		p.pos++
		v, ok := p.parseExpr()
		if !ok {
			return 0, false
		}
		if p.peek() != ')' {
			return 0, false
		}
		p.pos++
		return v, true
	}
	if c >= '0' && c <= '9' || c == '.' {
		return p.parseNumber()
	}
	if isFormulaAlpha(c) {
		return p.parseIdent()
	}
	return 0, false
}

func (p *formulaParser) parseNumber() (float64, bool) {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return 0, false
	}
	v, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func (p *formulaParser) parseIdent() (float64, bool) {
	start := p.pos
	for p.pos < len(p.src) && isFormulaAlpha(p.src[p.pos]) {
		p.pos++
	}
	name := strings.ToUpper(p.src[start:p.pos])
	if p.peek() == '(' {
		p.pos++
		args, ok := p.parseArgs()
		if !ok || p.peek() != ')' {
			return 0, false
		}
		p.pos++
		return applyFormulaFunc(name, args, p.ctx)
	}
	if isFormulaDigit(p.peek()) {
		digStart := p.pos
		for p.pos < len(p.src) && isFormulaDigit(p.src[p.pos]) {
			p.pos++
		}
		row := mustAtoi(p.src[digStart:p.pos])
		if v, ok := lookupCellByA1(name, row, p.ctx); ok {
			return v, true
		}
	}
	return 0, false
}

func (p *formulaParser) parseArgs() ([]float64, bool) {
	var args []float64
	for p.peek() != ')' {
		vs, ok := p.parseSingleArg()
		if !ok {
			return nil, false
		}
		args = append(args, vs...)
		if p.peek() == ',' {
			p.pos++
			continue
		}
		break
	}
	return args, true
}

func (p *formulaParser) parseSingleArg() ([]float64, bool) {
	if isFormulaAlpha(p.peek()) {
		save := p.pos
		start := p.pos
		for p.pos < len(p.src) && isFormulaAlpha(p.src[p.pos]) {
			p.pos++
		}
		name := strings.ToUpper(p.src[start:p.pos])
		switch name {
		case "ABOVE", "BELOW", "LEFT", "RIGHT":
			return collectCellsDirectional(name, p.ctx), true
		}
		if isFormulaDigit(p.peek()) {
			digStart := p.pos
			for p.pos < len(p.src) && isFormulaDigit(p.src[p.pos]) {
				p.pos++
			}
			row := mustAtoi(p.src[digStart:p.pos])
			if p.peek() == ':' {
				p.pos++
				rs := p.pos
				for p.pos < len(p.src) && isFormulaAlpha(p.src[p.pos]) {
					p.pos++
				}
				name2 := strings.ToUpper(p.src[rs:p.pos])
				if name2 == "" {
					return nil, false
				}
				ds := p.pos
				for p.pos < len(p.src) && isFormulaDigit(p.src[p.pos]) {
					p.pos++
				}
				row2 := mustAtoi(p.src[ds:p.pos])
				return collectCellsRange(name, row, name2, row2, p.ctx), true
			}
			if v, ok := lookupCellByA1(name, row, p.ctx); ok {
				return []float64{v}, true
			}
			return nil, false
		}
		p.pos = save
	}
	v, ok := p.parseExpr()
	if !ok {
		return nil, false
	}
	return []float64{v}, true
}

func applyFormulaFunc(name string, args []float64, ctx *tableContext) (float64, bool) {
	switch name {
	case "SUM":
		var sum float64
		for _, v := range args {
			sum += v
		}
		return sum, true
	case "AVERAGE", "AVG", "MEAN":
		if len(args) == 0 {
			return 0, false
		}
		var sum float64
		for _, v := range args {
			sum += v
		}
		return sum / float64(len(args)), true
	case "PRODUCT":
		if len(args) == 0 {
			return 0, false
		}
		v := 1.0
		for _, x := range args {
			v *= x
		}
		return v, true
	case "MIN":
		if len(args) == 0 {
			return 0, false
		}
		v := args[0]
		for _, x := range args[1:] {
			if x < v {
				v = x
			}
		}
		return v, true
	case "MAX":
		if len(args) == 0 {
			return 0, false
		}
		v := args[0]
		for _, x := range args[1:] {
			if x > v {
				v = x
			}
		}
		return v, true
	case "COUNT":
		return float64(len(args)), true
	case "ABS":
		if len(args) != 1 {
			return 0, false
		}
		return math.Abs(args[0]), true
	case "INT":
		if len(args) != 1 {
			return 0, false
		}
		return math.Floor(args[0]), true
	case "ROUND":
		if len(args) < 1 {
			return 0, false
		}
		digits := 0
		if len(args) >= 2 {
			digits = int(args[1])
		}
		mul := math.Pow(10, float64(digits))
		return math.Round(args[0]*mul) / mul, true
	case "MOD":
		if len(args) != 2 || args[1] == 0 {
			return 0, false
		}
		return math.Mod(args[0], args[1]), true
	}
	_ = ctx
	return 0, false
}

func collectCellsDirectional(dir string, ctx *tableContext) []float64 {
	if ctx == nil || ctx.table == nil {
		return nil
	}
	t := ctx.table
	var out []float64
	switch dir {
	case "ABOVE":
		for r := ctx.row - 1; r >= 0; r-- {
			v, ok := cellNumericValue(t, r, ctx.col)
			if !ok {
				break
			}
			out = append(out, v)
		}
	case "BELOW":
		for r := ctx.row + 1; r < len(t.Rows); r++ {
			v, ok := cellNumericValue(t, r, ctx.col)
			if !ok {
				break
			}
			out = append(out, v)
		}
	case "LEFT":
		for c := ctx.col - 1; c >= 0; c-- {
			v, ok := cellNumericValue(t, ctx.row, c)
			if !ok {
				break
			}
			out = append(out, v)
		}
	case "RIGHT":
		row := getRow(t, ctx.row)
		if row == nil {
			return nil
		}
		startCol := ctx.col + cellSpanAt(row, ctx.col)
		for c := startCol; ; c++ {
			v, ok := cellNumericValue(t, ctx.row, c)
			if !ok {
				break
			}
			out = append(out, v)
		}
	}
	return out
}

func collectCellsRange(c1 string, r1 int, c2 string, r2 int, ctx *tableContext) []float64 {
	if ctx == nil || ctx.table == nil {
		return nil
	}
	col1, ok1 := a1ColToIndex(c1)
	col2, ok2 := a1ColToIndex(c2)
	if !ok1 || !ok2 {
		return nil
	}
	if col1 > col2 {
		col1, col2 = col2, col1
	}
	row1 := r1 - 1
	row2 := r2 - 1
	if row1 > row2 {
		row1, row2 = row2, row1
	}
	var out []float64
	for r := row1; r <= row2; r++ {
		for c := col1; c <= col2; c++ {
			if v, ok := cellNumericValue(ctx.table, r, c); ok {
				out = append(out, v)
			} else {
				out = append(out, 0)
			}
		}
	}
	return out
}

func lookupCellByA1(colName string, row int, ctx *tableContext) (float64, bool) {
	if ctx == nil {
		return 0, false
	}
	col, ok := a1ColToIndex(colName)
	if !ok {
		return 0, false
	}
	return cellNumericValue(ctx.table, row-1, col)
}

func a1ColToIndex(name string) (int, bool) {
	name = strings.ToUpper(name)
	if name == "" {
		return 0, false
	}
	out := 0
	for _, c := range name {
		if c < 'A' || c > 'Z' {
			return 0, false
		}
		out = out*26 + int(c-'A'+1)
	}
	return out - 1, true
}

func cellNumericValue(t *docx.Table, row, col int) (float64, bool) {
	cell, ok := cellAt(t, row, col)
	if !ok {
		return 0, false
	}
	text := cellPlainText(cell)
	return parseCellNumber(text)
}

func cellAt(t *docx.Table, row, col int) (docx.TableCell, bool) {
	if row < 0 || row >= len(t.Rows) {
		return docx.TableCell{}, false
	}
	r := t.Rows[row]
	c := 0
	for _, cell := range r.Cells {
		span := cell.GridSpan
		if span < 1 {
			span = 1
		}
		if col >= c && col < c+span {
			return cell, true
		}
		c += span
	}
	return docx.TableCell{}, false
}

func getRow(t *docx.Table, row int) *docx.TableRow {
	if row < 0 || row >= len(t.Rows) {
		return nil
	}
	return &t.Rows[row]
}

func cellSpanAt(row *docx.TableRow, col int) int {
	c := 0
	for _, cell := range row.Cells {
		span := cell.GridSpan
		if span < 1 {
			span = 1
		}
		if col >= c && col < c+span {
			return span
		}
		c += span
	}
	return 1
}

func cellPlainText(c docx.TableCell) string {
	var b strings.Builder
	for _, b2 := range c.Blocks {
		p, ok := b2.(docx.Paragraph)
		if !ok {
			continue
		}
		for _, r := range p.Runs {
			if r.FieldBegin || r.FieldSep || r.FieldEnd || r.InstrText != "" {
				continue
			}
			b.WriteString(r.Text)
		}
		b.WriteByte(' ')
	}
	return strings.TrimSpace(b.String())
}

func parseCellNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	pct := false
	if strings.HasSuffix(s, "%") {
		pct = true
		s = strings.TrimSpace(s[:len(s)-1])
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = s[1 : len(s)-1]
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.':
			b.WriteRune(r)
		case r == '-' || r == '+':
			if i == 0 {
				b.WriteRune(r)
			}
		}
	}
	num := b.String()
	if num == "" || num == "-" || num == "+" || num == "." {
		return 0, false
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	if pct {
		v /= 100.0
	}
	if neg {
		v = -v
	}
	return v, true
}

func formatFormulaNumber(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if s == "-0" {
		return "0"
	}
	return s
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func isFormulaSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isFormulaAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isFormulaDigit(c byte) bool { return c >= '0' && c <= '9' }
