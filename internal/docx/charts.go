package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseChartPart reads word/charts/chartN.xml and returns a structured
// ChartData. It walks the c:chartSpace tree looking for one supported
// plot kind (c:barChart, c:lineChart, c:pieChart) and for each c:ser
// pulls categories from c:cat and numeric values from c:val. Fill
// colors come from c:spPr/a:solidFill/a:srgbClr when present.
//
// FlatText collects all CharData for legacy text-extraction users
// even when structured parsing succeeds.
func parseChartPart(f *zip.File) (ChartData, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return ChartData{}, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return ChartData{}, err
	}
	out := ChartData{
		FlatText: collectChartCharData(data),
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("chart decode: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "barChart":
			out.Type = "bar"
			if err := parseChartPlot(dec, se, &out); err != nil {
				return out, err
			}
		case "lineChart":
			out.Type = "line"
			if err := parseChartPlot(dec, se, &out); err != nil {
				return out, err
			}
		case "pieChart":
			out.Type = "pie"
			if err := parseChartPlot(dec, se, &out); err != nil {
				return out, err
			}
		case "title":
			t, err := readChartTitle(dec, se)
			if err != nil {
				return out, err
			}
			out.Title = t
		}
	}
}

// parseChartPlot reads one barChart/lineChart/pieChart element. It
// records BarDir (for bars) and walks each c:ser to collect series
// data.
func parseChartPlot(dec *xml.Decoder, start xml.StartElement, out *ChartData) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "barDir":
				out.BarDir = attr(t, "val")
				_ = dec.Skip()
			case "ser":
				s, cats, err := parseChartSeries(dec, t)
				if err != nil {
					return err
				}
				if len(cats) > len(out.Categories) {
					out.Categories = cats
				}
				out.Series = append(out.Series, s)
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

// parseChartSeries reads one c:ser element and returns the series
// data plus the category list. Categories often appear inside the
// first series' c:cat — subsequent series omit them. The caller
// merges into ChartData.Categories using max-length.
func parseChartSeries(dec *xml.Decoder, start xml.StartElement) (ChartSeries, []string, error) {
	var s ChartSeries
	var cats []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return s, cats, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return s, cats, nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "tx":
				name, err := readSeriesName(dec, t)
				if err != nil {
					return s, cats, err
				}
				s.Name = name
			case "spPr":
				color, err := readChartColor(dec, t)
				if err != nil {
					return s, cats, err
				}
				if color != "" {
					s.Color = color
				}
			case "cat":
				c, err := readChartStringRefValues(dec, t)
				if err != nil {
					return s, cats, err
				}
				cats = c
			case "val":
				v, err := readChartNumRefValues(dec, t)
				if err != nil {
					return s, cats, err
				}
				s.Values = v
			default:
				if err := dec.Skip(); err != nil {
					return s, cats, err
				}
			}
		}
	}
}

// readSeriesName reads c:tx/c:strRef/c:strCache/c:pt/c:v or the
// inline c:tx/c:v form Word uses for static series names.
func readSeriesName(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			return name, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return name, nil
			}
		case xml.StartElement:
			if t.Name.Local == "v" {
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return name, err
				}
				if name == "" {
					name = s
				}
				continue
			}
			// Descend into strRef / strCache etc. so we find c:v.
			if t.Name.Local == "strRef" || t.Name.Local == "strCache" || t.Name.Local == "pt" {
				continue
			}
			_ = dec.Skip()
		}
	}
}

// chartStringEntry / chartFloatEntry are package-level helpers used
// by the two read-by-index parsers. Local anonymous structs can't be
// cross-referenced by a top-level helper signature, hence these
// named types.
type chartStringEntry struct {
	idx int
	val string
}

type chartFloatEntry struct {
	idx int
	val float64
	ok  bool
}

// readChartStringRefValues reads c:strRef/c:strCache/c:pt[@idx]/c:v
// and returns the values in index order. Sparse indices are
// permitted — gaps fill with empty strings.
func readChartStringRefValues(dec *xml.Decoder, start xml.StartElement) ([]string, error) {
	var entries []chartStringEntry
	curIdx := -1
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return materializeStringValues(entries), nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "pt":
				idxStr := attr(t, "idx")
				if i, err := strconv.Atoi(idxStr); err == nil {
					curIdx = i
				}
			case "v":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				if curIdx >= 0 {
					entries = append(entries, chartStringEntry{idx: curIdx, val: s})
				}
				curIdx = -1
			case "strRef", "strCache", "numRef", "numCache":
				// descend
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

// readChartNumRefValues mirrors readChartStringRefValues but parses
// numeric values from c:numRef/c:numCache/c:pt[@idx]/c:v.
// Non-numeric or empty cells become NaN (skipped at render time).
func readChartNumRefValues(dec *xml.Decoder, start xml.StartElement) ([]float64, error) {
	var entries []chartFloatEntry
	curIdx := -1
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return materializeFloatValues(entries), nil
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "pt":
				idxStr := attr(t, "idx")
				if i, err := strconv.Atoi(idxStr); err == nil {
					curIdx = i
				}
			case "v":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				if curIdx >= 0 {
					v, perr := strconv.ParseFloat(s, 64)
					entries = append(entries, chartFloatEntry{idx: curIdx, val: v, ok: perr == nil})
				}
				curIdx = -1
			case "numRef", "numCache", "strRef", "strCache":
				// descend
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func materializeStringValues(entries []chartStringEntry) []string {
	maxIdx := -1
	for _, e := range entries {
		if e.idx > maxIdx {
			maxIdx = e.idx
		}
	}
	out := make([]string, maxIdx+1)
	for _, e := range entries {
		out[e.idx] = e.val
	}
	return out
}

func materializeFloatValues(entries []chartFloatEntry) []float64 {
	maxIdx := -1
	for _, e := range entries {
		if e.idx > maxIdx {
			maxIdx = e.idx
		}
	}
	out := make([]float64, maxIdx+1)
	// pre-fill NaN so missing cells don't render as zero bars.
	nan := zeroNaN()
	for i := range out {
		out[i] = nan
	}
	for _, e := range entries {
		if e.ok {
			out[e.idx] = e.val
		}
	}
	return out
}

// zeroNaN returns a NaN float64. Indirected through a function so we
// avoid importing "math" just for this constant.
func zeroNaN() float64 {
	var z float64
	return z / z
}

// readChartColor pulls the first a:solidFill/a:srgbClr/@val out of an
// spPr subtree. Returns "" if no color is set or if the fill uses
// theme/scheme colors we don't model here.
func readChartColor(dec *xml.Decoder, start xml.StartElement) (string, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return "", nil
			}
		case xml.StartElement:
			if t.Name.Local == "srgbClr" {
				v := attr(t, "val")
				_ = dec.Skip()
				// Drain to end.
				for {
					tok2, err := dec.Token()
					if err != nil {
						return v, err
					}
					if e, ok := tok2.(xml.EndElement); ok && e.Name.Local == start.Name.Local {
						return v, nil
					}
				}
			}
		}
	}
}

// readChartTitle reads c:title and pulls the visible title text out
// of its c:tx/c:rich/a:t descendants.
func readChartTitle(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return strings.TrimSpace(sb.String()), nil
			}
		case xml.StartElement:
			if t.Name.Local == "t" {
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return sb.String(), err
				}
				sb.WriteString(s)
			}
		case xml.CharData:
			// Some title encodings drop a:t and put text in m:t /
			// raw CharData; pick it up either way.
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.Write(t)
		}
	}
}

// collectChartCharData is the legacy text concatenator — same
// behavior as the old extractChartText. We keep it so FlatText
// remains populated for tests + the rare unsupported chart fallback.
func collectChartCharData(data []byte) string {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var sb []byte
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return string(sb)
		}
		if cd, ok := tok.(xml.CharData); ok {
			cdStr := strings.TrimSpace(string(cd))
			if cdStr == "" {
				continue
			}
			if len(sb) > 0 {
				sb = append(sb, ' ')
			}
			sb = append(sb, cdStr...)
		}
	}
	return string(sb)
}
