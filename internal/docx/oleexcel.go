package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ExcelEmbed is a minimal extraction of an embedded .xlsx OLE blob. Cells
// is the first worksheet's used range as a row-major [][]string grid; we
// don't preserve types or formulas (formulas are evaluated to the cached
// <v> value by Excel — we take that). Title, when non-empty, is the
// worksheet name. The renderer surfaces this as a simple text grid.
type ExcelEmbed struct {
	Title string
	Cells [][]string
}

// extractExcelEmbed opens an embedded .xlsx, reads xl/sharedStrings.xml +
// xl/worksheets/sheet1.xml, and returns the parsed grid. Empty grids are
// reported as ExcelEmbed{} with Title set if we could read the sheet name.
//
// Only the first sheet is read — Word's embedded Excel ranges nearly
// always live on sheet1. We cap rows × cols at maxRows × maxCols so a
// pathological file can't blow up memory.
func extractExcelEmbed(f *zip.File) (ExcelEmbed, error) {
	const (
		maxRows = 100
		maxCols = 50
	)
	var out ExcelEmbed
	rc, err := openZipFile(f)
	if err != nil {
		return out, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return out, err
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return out, fmt.Errorf("oleexcel: open inner zip: %w", err)
	}
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	var shared []string
	if zf, ok := files["xl/sharedStrings.xml"]; ok {
		s, err := parseSharedStrings(zf)
		if err == nil {
			shared = s
		}
	}
	// Find sheet1; we accept either xl/worksheets/sheet1.xml or the first
	// entry under xl/worksheets/ for documents that renamed it.
	var sheetZF *zip.File
	if zf, ok := files["xl/worksheets/sheet1.xml"]; ok {
		sheetZF = zf
	} else {
		for name, zf := range files {
			if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
				sheetZF = zf
				break
			}
		}
	}
	if sheetZF == nil {
		return out, nil
	}
	cells, err := parseExcelSheet(sheetZF, shared, maxRows, maxCols)
	if err != nil {
		return out, err
	}
	out.Cells = cells
	return out, nil
}

// parseSharedStrings reads xl/sharedStrings.xml into a flat slice. Each
// <si> entry collapses to its concatenated <t> text. Inline rich-text
// runs (<r><t>…</t></r>) are flattened.
func parseSharedStrings(f *zip.File) ([]string, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var out []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "si" {
			var sb strings.Builder
			depth := 1
			for depth > 0 {
				inner, e := dec.Token()
				if e != nil {
					return out, nil
				}
				switch t := inner.(type) {
				case xml.StartElement:
					depth++
				case xml.EndElement:
					depth--
				case xml.CharData:
					sb.Write(t)
				}
			}
			out = append(out, sb.String())
		}
	}
}

// parseExcelSheet streams <sheetData><row><c>…</c></row></sheetData>
// into a row-major grid. Cells with t="s" are looked up in the shared
// string table; t="b" emits TRUE/FALSE; numeric cells render via
// strconv.FormatFloat. Rows are addressed by their @r attribute when
// present so empty intermediate rows are preserved.
func parseExcelSheet(f *zip.File, shared []string, maxRows, maxCols int) ([][]string, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var grid [][]string
	var curRow []string
	var curColIdx int
	rowIdx := 0
	inSheet := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return grid, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sheetData":
				inSheet = true
			case "row":
				if !inSheet {
					continue
				}
				rowIdx = 0
				if v := attr(t, "r"); v != "" {
					if x, e := strconv.Atoi(v); e == nil {
						rowIdx = x
					}
				}
				// Pad grid up to rowIdx-1 with empty rows.
				for rowIdx > 0 && len(grid) < rowIdx-1 && len(grid) < maxRows {
					grid = append(grid, nil)
				}
				curRow = nil
				curColIdx = 0
			case "c":
				if !inSheet {
					continue
				}
				ref := attr(t, "r")        // e.g. "B7"
				typ := attr(t, "t")        // s = shared string, b = bool, str = formula-string
				colIdx := colIndexFromRef(ref, curColIdx)
				// Read <v> or <is> inline.
				var raw string
				depth := 1
				for depth > 0 {
					inner, e := dec.Token()
					if e != nil {
						return grid, nil
					}
					switch it := inner.(type) {
					case xml.StartElement:
						depth++
						if it.Name.Local == "v" || it.Name.Local == "t" {
							var s string
							_ = dec.DecodeElement(&s, &it)
							raw = s
							depth--
						}
					case xml.EndElement:
						depth--
					}
				}
				val := raw
				if typ == "s" {
					if i, err := strconv.Atoi(raw); err == nil && i >= 0 && i < len(shared) {
						val = shared[i]
					}
				} else if typ == "b" {
					if raw == "1" {
						val = "TRUE"
					} else {
						val = "FALSE"
					}
				}
				if colIdx >= maxCols {
					continue
				}
				// Grow curRow to fit.
				for len(curRow) <= colIdx {
					curRow = append(curRow, "")
				}
				curRow[colIdx] = val
				curColIdx = colIdx + 1
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "row":
				if len(grid) < maxRows {
					grid = append(grid, curRow)
				}
				curRow = nil
			case "sheetData":
				inSheet = false
			}
		}
	}
	return grid, nil
}

// flattenExcelGrid renders an ExcelEmbed as a single multi-line string —
// rows joined by '\n', cells joined by '\t'. The renderer's text path
// preserves tabs and line breaks so the grid is visually distinguishable
// from surrounding prose. Empty trailing rows / columns are trimmed.
func flattenExcelGrid(g ExcelEmbed) string {
	if len(g.Cells) == 0 {
		return ""
	}
	// Trim trailing empty rows.
	end := len(g.Cells)
	for end > 0 && rowAllEmpty(g.Cells[end-1]) {
		end--
	}
	if end == 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < end; i++ {
		row := g.Cells[i]
		// Trim trailing empty cells.
		j := len(row)
		for j > 0 && row[j-1] == "" {
			j--
		}
		if j > 0 {
			sb.WriteString(strings.Join(row[:j], "\t"))
		}
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func rowAllEmpty(r []string) bool {
	for _, c := range r {
		if c != "" {
			return false
		}
	}
	return true
}

// colIndexFromRef parses an Excel A1 reference (e.g. "AB7") and returns
// the 0-based column index. When ref is empty we fall back to the
// "next column" hint passed by the caller.
func colIndexFromRef(ref string, fallback int) int {
	if ref == "" {
		return fallback
	}
	col := 0
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c < 'A' || c > 'Z' {
			break
		}
		col = col*26 + int(c-'A'+1)
	}
	if col == 0 {
		return fallback
	}
	return col - 1
}
