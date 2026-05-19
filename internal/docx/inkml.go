package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// InkStroke is one inkML trace: a polyline of (x, y) coordinates in
// the trace's local coordinate space. Pressure / orientation channels
// are dropped — gopdf only paints uniform-width strokes.
type InkStroke struct {
	Points []InkPoint
}

// InkPoint is one absolute (x, y) sample in the trace's coordinate space.
type InkPoint struct {
	X, Y float64
}

// extractInkStrokes parses an inkML / w14:ink content part and returns
// the flat list of polylines. The parser is forgiving: missing namespace
// declarations, mixed-namespace trace tags, and leading whitespace all
// tolerated. The coordinate space is whatever the source file used;
// callers normalize to a render bounding box.
func extractInkStrokes(f *zip.File) ([]InkStroke, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var strokes []InkStroke
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return strokes, nil
		}
		if err != nil {
			return strokes, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		// inkML <trace> and Office's <w14:trace> both end in "trace"; the
		// XAML <Stroke> path uses <Stroke><StylusPoints> — also rare in
		// .docx. Catch any tag whose local name suggests stroke data.
		name := se.Name.Local
		if name == "trace" || name == "Trace" {
			text, err := readCharData(dec, se)
			if err != nil {
				continue
			}
			if stroke := parseInkTrace(text); len(stroke.Points) > 0 {
				strokes = append(strokes, stroke)
			}
		}
	}
}

// readCharData drains the current element's character content into a
// single string. The decoder is positioned at start; on return it has
// consumed the matching end-element.
func readCharData(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return b.String(), err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return b.String(), nil
			}
		}
	}
}

// parseInkTrace decodes an inkML <trace> string body. Coordinates are
// either space-separated pairs ("x y, x y, …") or comma-then-space
// ("x,y x,y x,y"). Office's writer uses the comma-separated form with
// optional pressure as the third channel; we drop everything past Y.
func parseInkTrace(raw string) InkStroke {
	out := InkStroke{}
	// Normalize: split on comma first, then on whitespace.
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// Some Office files use "!x y" prefix for "draw a discontinuity";
	// strip the leading marker.
	raw = strings.TrimPrefix(raw, "!")
	tokens := splitInkTrace(raw)
	for _, tok := range tokens {
		fields := strings.Fields(tok)
		if len(fields) < 2 {
			continue
		}
		x, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		if err != nil {
			continue
		}
		y, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			continue
		}
		out.Points = append(out.Points, InkPoint{X: x, Y: y})
	}
	return out
}

// splitInkTrace splits an inkML trace body into per-sample chunks. The
// separator is comma. Trailing/leading whitespace inside each chunk is
// fine for downstream Fields parsing.
func splitInkTrace(raw string) []string {
	return strings.Split(raw, ",")
}

// inkStrokesToShape converts a parsed inkML stroke list into a VMLShape
// whose CustomPath is the union of all strokes in [0,1]² space (matching
// the renderer's expectation). The shape size is picked to match the
// inkML aspect ratio at a comfortable ~80pt width.
func inkStrokesToShape(strokes []InkStroke) *VMLShape {
	minX, minY, maxX, maxY, ok := InkStrokeBounds(strokes)
	if !ok {
		return nil
	}
	w := maxX - minX
	h := maxY - minY
	if w == 0 {
		w = 1
	}
	if h == 0 {
		h = 1
	}
	var path strings.Builder
	for _, s := range strokes {
		if len(s.Points) == 0 {
			continue
		}
		for i, p := range s.Points {
			nx := (p.X - minX) / w
			ny := (p.Y - minY) / h
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			if path.Len() > 0 {
				path.WriteByte(' ')
			}
			path.WriteString(cmd)
			path.WriteByte(' ')
			path.WriteString(strconv.FormatFloat(nx, 'f', 4, 64))
			path.WriteByte(' ')
			path.WriteString(strconv.FormatFloat(ny, 'f', 4, 64))
		}
	}
	if path.Len() == 0 {
		return nil
	}
	const targetW = 80.0
	ratio := h / w
	if ratio < 0.15 {
		ratio = 0.3
	}
	if ratio > 4 {
		ratio = 4
	}
	return &VMLShape{
		Kind:           "ink",
		WidthPt:        targetW,
		HeightPt:       targetW * ratio,
		StrokeColor:    "000000",
		StrokeWeightPt: 0.75,
		CustomPath:     path.String(),
	}
}

// InkStrokeBounds returns the min/max of all stroke points combined.
// Returns ok=false when no points were collected.
func InkStrokeBounds(strokes []InkStroke) (minX, minY, maxX, maxY float64, ok bool) {
	first := true
	for _, s := range strokes {
		for _, p := range s.Points {
			if first {
				minX, minY, maxX, maxY = p.X, p.Y, p.X, p.Y
				first = false
				continue
			}
			if p.X < minX {
				minX = p.X
			}
			if p.X > maxX {
				maxX = p.X
			}
			if p.Y < minY {
				minY = p.Y
			}
			if p.Y > maxY {
				maxY = p.Y
			}
		}
	}
	return minX, minY, maxX, maxY, !first
}
