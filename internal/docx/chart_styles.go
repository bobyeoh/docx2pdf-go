package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// chartSiblingColors / chartSiblingStyle return the cs:colorStyle and
// cs:chartStyle sibling parts paired with a given chartN.xml target.
// Word writes these in word/charts/ with parallel numeric suffixes:
//
//	word/charts/chart1.xml
//	word/charts/colors1.xml      ← cs:colorStyle (palette)
//	word/charts/style1.xml       ← cs:chartStyle (text + fill rules)
//
// The Office 2011 chartStyle/chartColorStyle relationship types point
// here; we just resolve by sibling pattern for simplicity.
func chartSiblingColors(files map[string]*zip.File, chartTarget string) *zip.File {
	return chartSibling(files, chartTarget, "colors")
}

func chartSiblingStyle(files map[string]*zip.File, chartTarget string) *zip.File {
	return chartSibling(files, chartTarget, "style")
}

func chartSibling(files map[string]*zip.File, chartTarget, prefix string) *zip.File {
	full := chartTarget
	if !strings.HasPrefix(full, "word/") {
		full = "word/" + full
	}
	slash := strings.LastIndex(full, "/")
	if slash < 0 {
		return nil
	}
	dir, fname := full[:slash+1], full[slash+1:]
	base := strings.TrimSuffix(fname, ".xml")
	// Strip leading "chart" prefix to recover the numeric suffix.
	suffix := strings.TrimPrefix(base, "chart")
	if suffix == base {
		// Not a chart-N name — bail.
		return nil
	}
	guess := dir + prefix + suffix + ".xml"
	if zf, ok := files[guess]; ok {
		return zf
	}
	return nil
}

// extractChartColorStyle reads a colorsN.xml part and returns the ordered
// list of palette colors. Word uses <cs:colorStyle> with <a:schemeClr val=
// "accentN"/> children. We resolve the scheme color via the supplied
// theme map; literal srgbClr values are passed through.
//
// Returns an empty slice when the file is absent, unreadable, or has no
// extractable color entries.
func extractChartColorStyle(f *zip.File, theme Theme) (palette []string, method string) {
	if f == nil {
		return nil, ""
	}
	rc, err := openZipFile(f)
	if err != nil {
		return nil, ""
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return palette, method
		}
		if err != nil {
			return palette, method
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "colorStyle":
			if v := attr(se, "meth"); v != "" {
				method = v
			}
		case "schemeClr":
			if v := attr(se, "val"); v != "" {
				if c, ok := theme.Colors[v]; ok && c != "" {
					palette = append(palette, strings.ToUpper(c))
				} else {
					// Surface the scheme name so the renderer can map
					// even when no theme part is present.
					palette = append(palette, "scheme:"+v)
				}
			}
			_ = dec.Skip()
		case "srgbClr":
			if v := attr(se, "val"); v != "" {
				palette = append(palette, strings.ToUpper(v))
			}
			_ = dec.Skip()
		}
	}
}

// extractChartStyleSummary reads a styleN.xml part and pulls coarse text
// metrics used by the chart renderer. Word's chartStyle XML is huge — we
// only extract title/axis default font sizes from <cs:title>/<cs:catAxis>/
// <cs:valAxis>/<cs:dataLabel>/<cs:plotArea> children's <a:defRPr sz="...">.
// Sizes are in hundredths of a point (the OOXML drawing convention), so
// we divide by 100 before storing.
func extractChartStyleSummary(f *zip.File) ChartStyleSummary {
	var out ChartStyleSummary
	if f == nil {
		return out
	}
	rc, err := openZipFile(f)
	if err != nil {
		return out
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	// Track which block we're in. cs:* element name selects which
	// ChartStyleSummary slot a nested defRPr@sz updates.
	var block string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "title":
				block = "title"
			case "categoryAxis", "catAxis":
				block = "catAxis"
			case "valueAxis", "valAxis":
				block = "valAxis"
			case "dataLabel":
				block = "dataLabel"
			case "axisTitle":
				block = "axisTitle"
			case "defRPr":
				if v := attr(t, "sz"); v != "" {
					if x, err := strconv.Atoi(v); err == nil {
						pt := float64(x) / 100.0
						switch block {
						case "title":
							out.TitleFontSizePt = pt
						case "catAxis":
							out.CatAxisFontSizePt = pt
						case "valAxis":
							out.ValAxisFontSizePt = pt
						case "dataLabel":
							out.DataLabelFontSizePt = pt
						case "axisTitle":
							out.AxisTitleFontSizePt = pt
						}
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "title", "categoryAxis", "catAxis", "valueAxis", "valAxis",
				"dataLabel", "axisTitle":
				block = ""
			}
		}
	}
}

// ChartStyleSummary holds the coarse text metrics the renderer pulls
// from a cs:chartStyle part. All fields default to zero — the renderer
// falls back to its hard-coded defaults when a slot is zero.
type ChartStyleSummary struct {
	TitleFontSizePt     float64
	CatAxisFontSizePt   float64
	ValAxisFontSizePt   float64
	AxisTitleFontSizePt float64
	DataLabelFontSizePt float64
}

// HasAny reports whether the summary carries any non-default metric.
func (c ChartStyleSummary) HasAny() bool {
	return c.TitleFontSizePt > 0 || c.CatAxisFontSizePt > 0 ||
		c.ValAxisFontSizePt > 0 || c.AxisTitleFontSizePt > 0 ||
		c.DataLabelFontSizePt > 0
}
