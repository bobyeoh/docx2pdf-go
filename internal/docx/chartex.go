package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// chartex.go parses the modern Office 2016+ chartEx XML schema (cx
// namespace). chartEx introduces chart families that pre-2016
// "c:" charts cannot represent: waterfall, sunburst, treemap, funnel,
// histogram, pareto, boxAndWhisker, regionMap.
//
// We model the three chartEx families most commonly seen in Word
// documents and that map cleanly onto a 2D rectangle (waterfall,
// treemap, sunburst). The rest fall through to a flat title + label
// extraction so at least chart text survives in the PDF.
//
// chartEx structural skeleton (simplified):
//
//	<cx:chartSpace>
//	  <cx:chartData>
//	    <cx:data id="0">
//	      <cx:strDim type="cat"><cx:lvl><cx:pt idx="0">Cat1</cx:pt>…</cx:lvl></cx:strDim>
//	      <cx:numDim type="val"><cx:lvl><cx:pt idx="0">10</cx:pt>…</cx:lvl></cx:numDim>
//	    </cx:data>
//	  </cx:chartData>
//	  <cx:chart>
//	    <cx:title><cx:tx><cx:rich>…</cx:rich></cx:tx></cx:title>
//	    <cx:plotArea>
//	      <cx:plotAreaRegion>
//	        <cx:series ownerIdx="0" layoutId="waterfall">
//	          <cx:tx><cx:txData><cx:v>Series Label</cx:v></cx:txData></cx:tx>
//	          <cx:dataId val="0"/>
//	          <cx:dataPt idx="3"><cx:subtotals><cx:subtotal idx="3"/></cx:subtotals></cx:dataPt>
//	        </cx:series>
//	      </cx:plotAreaRegion>
//	    </cx:plotArea>
//	  </cx:chart>
//	</cx:chartSpace>

// isChartExRel matches the chartEx relationship type added in Office 2016.
func isChartExRel(t string) bool {
	return strings.HasSuffix(t, "/chartEx") || strings.Contains(t, "chartex")
}

// extractChartExStruct walks a chartEx part and returns a ChartData model
// shaped like the classic c: chart path so the renderer doesn't need to
// learn a parallel API. Kind names introduced here are:
//
//	"waterfall"  — vertical waterfall (running totals + subtotal markers)
//	"treemap"    — nested rectangle hierarchy
//	"sunburst"   — concentric ring hierarchy
//
// Anything else returns ChartData{Title: …} so the title + caption still
// surface even when the chart family isn't paintable.
func extractChartExStruct(f *zip.File) (ChartData, error) {
	var out ChartData
	rc, err := openZipFile(f)
	if err != nil {
		return out, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)

	// dimensions[dataID][dimType] → []string for cat / []float for val.
	type dim struct {
		strs []string
		nums []float64
	}
	dims := map[string]map[string]*dim{}
	currentDataID := ""
	currentDimType := ""
	currentDim := (*dim)(nil)
	layoutID := ""
	seriesName := ""
	subtotalIdx := map[int]bool{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "title":
			out.Title = extractChartTitle(dec, se)
		case "data":
			currentDataID = attr(se, "id")
			if _, ok := dims[currentDataID]; !ok {
				dims[currentDataID] = map[string]*dim{}
			}
		case "strDim":
			currentDimType = attr(se, "type")
			if currentDimType == "" {
				currentDimType = "cat"
			}
			d := &dim{}
			dims[currentDataID][currentDimType] = d
			currentDim = d
		case "numDim":
			currentDimType = attr(se, "type")
			if currentDimType == "" {
				currentDimType = "val"
			}
			d := &dim{}
			dims[currentDataID][currentDimType] = d
			currentDim = d
		case "pt":
			val, _ := readElementText(dec, se)
			if currentDim != nil {
				if currentDimType == "val" || currentDimType == "size" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
						currentDim.nums = append(currentDim.nums, f)
					}
				} else {
					currentDim.strs = append(currentDim.strs, val)
				}
			}
		case "series":
			layoutID = ""
			seriesName = ""
		case "layoutId":
			// Cell content is the layout ID; cx:layoutId is an element
			// whose text is the layout name in some schemas, but the
			// canonical form is an attribute. Try both.
			if v := attr(se, "val"); v != "" {
				layoutID = v
			} else {
				val, _ := readElementText(dec, se)
				if v := strings.TrimSpace(val); v != "" {
					layoutID = v
				}
			}
		case "tx":
			// Series label. Pull text-only.
			seriesName = readChartExText(dec, se)
		case "subtotal":
			if v := attr(se, "idx"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					subtotalIdx[n] = true
				}
			}
		}
	}

	// Resolve the data via the layoutId we observed.
	if layoutID == "" {
		// Series wasn't tagged — try to infer from data shape (1 strDim +
		// 1 numDim → bar-ish; no inference for sunburst/treemap which
		// need a strDim hierarchy with multiple lvls).
		return out, nil
	}
	kind := ""
	switch strings.ToLower(layoutID) {
	case "waterfall":
		kind = "waterfall"
	case "treemap":
		kind = "treemap"
	case "sunburst":
		kind = "sunburst"
	case "funnel":
		kind = "funnel"
	case "histogram", "pareto":
		kind = "histogram"
	case "boxwhisker", "boxandwhisker":
		kind = "boxWhisker"
	case "regionmap":
		kind = "regionMap"
	}
	if kind == "" {
		return out, nil
	}
	out.Kind = kind
	// Find the first dataID that has a numeric dimension; pair it with the
	// matching category dimension if present.
	for _, m := range dims {
		var cats []string
		var vals []float64
		if d := m["cat"]; d != nil {
			cats = d.strs
		}
		if d := m["val"]; d != nil {
			vals = d.nums
		}
		if d := m["size"]; d != nil && len(vals) == 0 {
			vals = d.nums
		}
		if len(vals) > 0 {
			ser := ChartSeries{Name: seriesName, Values: vals}
			out.Series = append(out.Series, ser)
			out.Categories = cats
			break
		}
	}
	// Waterfall subtotal markers: stash the indexes onto the series via
	// the dataLabels field — renderer can stamp "Σ" on them.
	if kind == "waterfall" && len(subtotalIdx) > 0 {
		out.WaterfallSubtotals = make([]int, 0, len(subtotalIdx))
		for idx := range subtotalIdx {
			out.WaterfallSubtotals = append(out.WaterfallSubtotals, idx)
		}
	}
	return out, nil
}

// readChartExText pulls visible text out of <cx:txData>/<cx:rich>/<a:t>
// — chartEx wraps strings in a Rich Text shell similar to classic chart
// titles. depth-counted walker; returns concatenated <cx:v> + <a:t>.
func readChartExText(dec *xml.Decoder, start xml.StartElement) string {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return sb.String()
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			_ = t
		case xml.EndElement:
			depth--
		case xml.CharData:
			sb.WriteString(string(t))
		}
	}
	return strings.TrimSpace(sb.String())
}
