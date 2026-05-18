package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// diagramSiblingDrawing returns the drawing*.xml zip entry that pairs
// with a given data*.xml target path, or nil when no match exists.
// Word writes them with matching numeric suffixes (data1.xml ↔
// drawing1.xml) in the same directory.
func diagramSiblingDrawing(files map[string]*zip.File, dataTarget string) *zip.File {
	return diagramSibling(files, dataTarget, "drawing")
}

// diagramSiblingLayout returns the layout*.xml zip entry that pairs with
// a given data*.xml target. Word's SmartArt parts always come as a set
// (data, layout, colors, quickStyle) with matching numeric suffixes.
func diagramSiblingLayout(files map[string]*zip.File, dataTarget string) *zip.File {
	return diagramSibling(files, dataTarget, "layout")
}

func diagramSibling(files map[string]*zip.File, dataTarget, prefix string) *zip.File {
	const dataPrefix = "data"
	full := "word/" + dataTarget
	slash := strings.LastIndex(full, "/")
	if slash < 0 {
		return nil
	}
	dir, fname := full[:slash+1], full[slash+1:]
	if !strings.HasPrefix(fname, dataPrefix) {
		return nil
	}
	guess := dir + prefix + fname[len(dataPrefix):]
	if zf, ok := files[guess]; ok {
		return zf
	}
	return nil
}

// extractDiagramLayoutKind reads a SmartArt layout part and returns a
// coarse layout family ("cycle", "hierarchy", "pyramid", "list",
// "matrix", "radial", "process") derived from <dgm:layoutDef uniqueId="…">.
// Returns "" when the part is missing or unparseable; callers default
// to "process".
func extractDiagramLayoutKind(f *zip.File) string {
	rc, err := openZipFile(f)
	if err != nil {
		return ""
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "layoutDef" {
			uid := attr(se, "uniqueId")
			return classifySmartArtUniqueID(uid)
		}
	}
	return ""
}

// classifySmartArtUniqueID maps Word's catalog of SmartArt layout
// uniqueIds (urn:microsoft.com/office/officeart/2005/8/layout/<name>) to
// a coarse layout family our renderer can synthesize.
func classifySmartArtUniqueID(uid string) string {
	low := strings.ToLower(uid)
	switch {
	case strings.Contains(low, "cycle"):
		return "cycle"
	case strings.Contains(low, "hierarchy"), strings.Contains(low, "orgchart"), strings.Contains(low, "hChart"):
		return "hierarchy"
	case strings.Contains(low, "pyramid"):
		return "pyramid"
	case strings.Contains(low, "matrix"):
		return "matrix"
	case strings.Contains(low, "radial"):
		return "radial"
	case strings.Contains(low, "list"), strings.Contains(low, "vlist"):
		return "list"
	case strings.Contains(low, "process"), strings.Contains(low, "chevron"), strings.Contains(low, "arrow"):
		return "process"
	}
	return ""
}

// extractDiagramDrawing parses word/diagrams/drawingN.xml — the
// pre-rendered DrawingML shape tree Word writes alongside the SmartArt
// data part. We translate each dsp:sp into a VMLShape positioned in the
// group's coordinate space; the renderer projects those into the
// outer bounding rect at paint time.
//
// Returns nil when the part contains no useful shapes — callers should
// fall back to the text-only diagram surface in Document.Diagrams.
func extractDiagramDrawing(f *zip.File) (*VMLShape, error) {
	rc, err := openZipFile(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var shapes []VMLShape
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "sp" {
			sh, ok := decodeDiagramSp(dec, se)
			if ok {
				shapes = append(shapes, sh)
			}
		}
	}
	if len(shapes) == 0 {
		return nil, nil
	}
	// Compute bounding box so the group's coord size matches the
	// child layout. Without this the renderer would have nothing to
	// project against.
	maxX, maxY := 0.0, 0.0
	for _, sh := range shapes {
		if sh.OffsetXPt+sh.WidthPt > maxX {
			maxX = sh.OffsetXPt + sh.WidthPt
		}
		if sh.OffsetYPt+sh.HeightPt > maxY {
			maxY = sh.OffsetYPt + sh.HeightPt
		}
	}
	if maxX <= 0 || maxY <= 0 {
		return nil, nil
	}
	return &VMLShape{
		Kind:       "group",
		Children:   shapes,
		CoordSizeW: maxX,
		CoordSizeH: maxY,
	}, nil
}

// decodeDiagramSp parses one dsp:sp element. Returns (shape, true) when
// the element produced something we can draw; (_, false) when the entry
// only carried setup data (typically a missing prstGeom).
func decodeDiagramSp(dec *xml.Decoder, start xml.StartElement) (VMLShape, bool) {
	var sh VMLShape
	var hasGeom bool
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return sh, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "xfrm":
				ox, oy, cx, cy := decodeDiagramXfrm(dec, t)
				sh.OffsetXPt = ox
				sh.OffsetYPt = oy
				sh.WidthPt = cx
				sh.HeightPt = cy
				depth--
			case "prstGeom":
				prst := attr(t, "prst")
				sh.Kind = shapeKindForPrst(prst)
				hasGeom = true
				_ = dec.Skip()
				depth--
			case "solidFill":
				c := scanSolidFillColor(dec, t)
				if c != "" && sh.FillColor == "" {
					sh.FillColor = c
				}
				depth--
			case "gradFill":
				stops, angle, kind, err := parseGradFill(dec, t)
				if err == nil && len(stops) > 0 {
					sh.GradientKind = kind
					sh.GradientAngle = angle
					sh.GradientStops = stops
				}
				depth--
			case "ln":
				// Stroke width + color. <a:ln w="N"> N is in EMU.
				if v := attr(t, "w"); v != "" {
					if w, err := strconv.ParseInt(v, 10, 64); err == nil {
						sh.StrokeWeightPt = float64(w) / emuPerPt
					}
				}
				if c := scanSolidFillColor(dec, t); c != "" {
					sh.StrokeColor = c
				}
				depth--
			case "txBody":
				txt := extractTxBodyText(dec, t)
				if txt != "" {
					sh.TextBox = txt
				}
				depth--
			default:
				// nothing — fall through to depth handling
			}
		case xml.EndElement:
			depth--
		}
	}
	if !hasGeom && sh.WidthPt <= 0 && sh.HeightPt <= 0 {
		return sh, false
	}
	if !hasGeom {
		sh.Kind = "rect"
	}
	return sh, true
}

// decodeDiagramXfrm reads <a:xfrm> → <a:off x= y=>/<a:ext cx= cy=> and
// returns offset+extent in points. Returns zeros when the element didn't
// contain explicit values.
func decodeDiagramXfrm(dec *xml.Decoder, start xml.StartElement) (offX, offY, cx, cy float64) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "off":
				if v := attr(t, "x"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						offX = float64(x) / emuPerPt
					}
				}
				if v := attr(t, "y"); v != "" {
					if y, err := strconv.ParseInt(v, 10, 64); err == nil {
						offY = float64(y) / emuPerPt
					}
				}
			case "ext":
				if v := attr(t, "cx"); v != "" {
					if x, err := strconv.ParseInt(v, 10, 64); err == nil {
						cx = float64(x) / emuPerPt
					}
				}
				if v := attr(t, "cy"); v != "" {
					if y, err := strconv.ParseInt(v, 10, 64); err == nil {
						cy = float64(y) / emuPerPt
					}
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return
}

// synthesizeSmartArtLayout builds a horizontal "process" diagram out of a
// flat node-text string when Word didn't pre-render the SmartArt visuals.
// The text comes in the form "Node1 → Node2 → Node3" from
// extractDiagramText; we split it on the arrow, draw one rounded rect per
// node, and connect them with thin arrow lines. Returns nil when the
// text doesn't split into >=2 nodes — single-node diagrams aren't worth
// the synthetic frame and fall back to the placeholder rect.
//
// We deliberately ignore the underlying SmartArt layout type (cycle vs.
// hierarchy vs. matrix) — every preset reduces to a recognizable boxes-
// with-arrows render at this density, and inferring the right layout
// algorithm from data.xml alone is unreliable without colors/style/quick-
// style parts.
func synthesizeSmartArtLayout(text string, widthPt, heightPt float64) *VMLShape {
	return synthesizeSmartArtLayoutKind(text, "process", widthPt, heightPt)
}

// synthesizeSmartArtLayoutKind builds a synthetic SmartArt layout in the
// requested family. Supported kinds:
//
//   - "process"   (default): boxes left→right with arrows
//   - "list":     boxes stacked top→bottom, no arrows
//   - "cycle":    boxes around a circle with arrows tracing the cycle
//   - "hierarchy": tree with first node at top, the rest as children
//   - "pyramid":  stacked trapezoids widest at base
//   - "matrix":   2×2 (or n×ceil(n/2)) grid
//   - "radial":   first node at center, the rest on a circle around it
//
// Unknown kinds fall through to "process".
func synthesizeSmartArtLayoutKind(text, kind string, widthPt, heightPt float64) *VMLShape {
	if text == "" {
		return nil
	}
	nodes := splitDiagramNodes(text)
	if len(nodes) < 2 {
		return nil
	}
	if widthPt <= 0 {
		widthPt = 480
	}
	if heightPt <= 0 {
		heightPt = 96
	}
	switch kind {
	case "list":
		return synthList(nodes, widthPt, heightPt)
	case "cycle":
		return synthCycle(nodes, widthPt, heightPt)
	case "hierarchy":
		return synthHierarchy(nodes, widthPt, heightPt)
	case "pyramid":
		return synthPyramid(nodes, widthPt, heightPt)
	case "matrix":
		return synthMatrix(nodes, widthPt, heightPt)
	case "radial":
		return synthRadial(nodes, widthPt, heightPt)
	}
	const (
		boxPad     = 8.0  // horizontal gap between boxes (also arrow span)
		minBoxW    = 64.0 // smallest acceptable box width
		strokeColr = "808080"
		fillColr   = "EEEEEE"
		arrowColr  = "606060"
	)
	n := float64(len(nodes))
	// Geometry: boxes share the row, with boxPad between them. Reserve
	// half a box-pad on each side as the outer margin so the leftmost
	// arrow has room to land on the box edge.
	boxW := (widthPt - (n-1)*boxPad) / n
	if boxW < minBoxW {
		// Too many nodes to fit horizontally — clip to fit. Caller can
		// still consume the synthetic group; nodes that overflow will
		// just be drawn at the actual computed width.
		boxW = minBoxW
	}
	boxH := heightPt * 0.7
	if boxH < 24 {
		boxH = 24
	}
	boxY := (heightPt - boxH) / 2
	var children []VMLShape
	for i, name := range nodes {
		x := float64(i) * (boxW + boxPad)
		children = append(children, VMLShape{
			Kind:           "roundrect",
			WidthPt:        boxW,
			HeightPt:       boxH,
			OffsetXPt:      x,
			OffsetYPt:      boxY,
			FillColor:      fillColr,
			StrokeColor:    strokeColr,
			StrokeWeightPt: 0.75,
			CornerArc:      6,
			TextBox:        name,
		})
		if i < len(nodes)-1 {
			// Connector arrow: a 1pt line from this box's right edge to
			// the next box's left edge. We render this as a v:polyline
			// with two points so the existing VML painter draws it.
			arrowFromX := x + boxW
			arrowToX := arrowFromX + boxPad
			arrowY := heightPt / 2
			children = append(children, VMLShape{
				Kind:           "polyline",
				StrokeColor:    arrowColr,
				StrokeWeightPt: 0.75,
				Points:         formatPolyPoints(arrowFromX, arrowY, arrowToX, arrowY),
			})
			// Small arrow-head: short two-segment polyline forming a
			// chevron at the destination end.
			children = append(children, VMLShape{
				Kind:           "polyline",
				StrokeColor:    arrowColr,
				StrokeWeightPt: 0.75,
				Points: formatPolyPoints(
					arrowToX-3, arrowY-3,
					arrowToX, arrowY,
					arrowToX-3, arrowY+3,
				),
			})
		}
	}
	return &VMLShape{
		Kind:       "group",
		WidthPt:    widthPt,
		HeightPt:   heightPt,
		Children:   children,
		CoordSizeW: widthPt,
		CoordSizeH: heightPt,
	}
}

// splitDiagramNodes splits an extractDiagramText output on the " → "
// separator. We also tolerate a comma fallback for diagrams whose flat
// text lacks the arrow (shouldn't happen with our extractor, but be
// defensive against future writers).
func splitDiagramNodes(text string) []string {
	sep := " → "
	if !strings.Contains(text, sep) {
		if strings.Contains(text, ", ") {
			sep = ", "
		} else {
			return []string{text}
		}
	}
	parts := strings.Split(text, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// synthList stacks boxes vertically with no connectors. Used for the
// "Vertical Box List" / "Vertical Bullet List" SmartArt families.
func synthList(nodes []string, widthPt, heightPt float64) *VMLShape {
	if heightPt < float64(len(nodes))*22 {
		heightPt = float64(len(nodes)) * 22
	}
	const gap = 6.0
	n := float64(len(nodes))
	boxH := (heightPt - (n-1)*gap) / n
	if boxH < 20 {
		boxH = 20
	}
	var children []VMLShape
	for i, name := range nodes {
		y := float64(i) * (boxH + gap)
		children = append(children, VMLShape{
			Kind:           "roundrect",
			WidthPt:        widthPt,
			HeightPt:       boxH,
			OffsetXPt:      0,
			OffsetYPt:      y,
			FillColor:      "EEEEEE",
			StrokeColor:    "808080",
			StrokeWeightPt: 0.75,
			CornerArc:      4,
			TextBox:        name,
		})
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// synthCycle distributes nodes evenly around a circle, with arrows
// tracing the boundary clockwise from each node to the next.
func synthCycle(nodes []string, widthPt, heightPt float64) *VMLShape {
	side := widthPt
	if heightPt < side {
		side = heightPt
	}
	cx := widthPt / 2
	cy := heightPt / 2
	radius := side*0.4 - 12
	if radius < 24 {
		radius = 24
	}
	boxW := 80.0
	boxH := 28.0
	if boxW > side*0.35 {
		boxW = side * 0.35
	}
	var children []VMLShape
	type pt struct{ x, y float64 }
	centers := make([]pt, len(nodes))
	for i, name := range nodes {
		angle := 2*pi*float64(i)/float64(len(nodes)) - pi/2 // start at top
		px := cx + radius*cosApprox(angle)
		py := cy + radius*sinApprox(angle)
		centers[i] = pt{px, py}
		children = append(children, VMLShape{
			Kind:           "roundrect",
			WidthPt:        boxW,
			HeightPt:       boxH,
			OffsetXPt:      px - boxW/2,
			OffsetYPt:      py - boxH/2,
			FillColor:      "EEEEEE",
			StrokeColor:    "808080",
			StrokeWeightPt: 0.75,
			CornerArc:      4,
			TextBox:        name,
		})
	}
	// Arrows along the cycle.
	for i := range nodes {
		from := centers[i]
		to := centers[(i+1)%len(nodes)]
		children = append(children, VMLShape{
			Kind:           "polyline",
			StrokeColor:    "606060",
			StrokeWeightPt: 0.75,
			Points:         formatPolyPoints(from.x, from.y, to.x, to.y),
		})
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// synthHierarchy places the first node centered at the top and the
// remaining nodes as horizontally-distributed children below, with a
// vertical connector from each child up to the parent.
func synthHierarchy(nodes []string, widthPt, heightPt float64) *VMLShape {
	if heightPt < 120 {
		heightPt = 120
	}
	root := nodes[0]
	kids := nodes[1:]
	rootW := 100.0
	if rootW > widthPt*0.5 {
		rootW = widthPt * 0.5
	}
	rootH := 28.0
	rootX := (widthPt - rootW) / 2
	rootY := 4.0
	rootCx := rootX + rootW/2
	rootCy := rootY + rootH
	var children []VMLShape
	children = append(children, VMLShape{
		Kind:           "roundrect",
		WidthPt:        rootW,
		HeightPt:       rootH,
		OffsetXPt:      rootX,
		OffsetYPt:      rootY,
		FillColor:      "D6E4FF",
		StrokeColor:    "808080",
		StrokeWeightPt: 0.75,
		CornerArc:      4,
		TextBox:        root,
	})
	if len(kids) == 0 {
		return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
	}
	gap := 6.0
	k := float64(len(kids))
	kidW := (widthPt - (k+1)*gap) / k
	if kidW < 40 {
		kidW = 40
	}
	kidH := 26.0
	kidY := heightPt - kidH - 8
	for i, name := range kids {
		x := gap + float64(i)*(kidW+gap)
		children = append(children, VMLShape{
			Kind:           "roundrect",
			WidthPt:        kidW,
			HeightPt:       kidH,
			OffsetXPt:      x,
			OffsetYPt:      kidY,
			FillColor:      "EEEEEE",
			StrokeColor:    "808080",
			StrokeWeightPt: 0.75,
			CornerArc:      4,
			TextBox:        name,
		})
		// L-shaped connector: down from root, across, up into child.
		kx := x + kidW/2
		ky := kidY
		midY := (rootCy + ky) / 2
		children = append(children,
			VMLShape{Kind: "polyline", StrokeColor: "606060", StrokeWeightPt: 0.75, Points: formatPolyPoints(rootCx, rootCy, rootCx, midY)},
			VMLShape{Kind: "polyline", StrokeColor: "606060", StrokeWeightPt: 0.75, Points: formatPolyPoints(rootCx, midY, kx, midY)},
			VMLShape{Kind: "polyline", StrokeColor: "606060", StrokeWeightPt: 0.75, Points: formatPolyPoints(kx, midY, kx, ky)},
		)
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// synthPyramid stacks trapezoidal bands widest at the base. Number of
// nodes = number of bands. We approximate the trapezoid with a polyline
// since the VML renderer doesn't have a "trapezoid" geometry primitive.
func synthPyramid(nodes []string, widthPt, heightPt float64) *VMLShape {
	if heightPt < float64(len(nodes))*22 {
		heightPt = float64(len(nodes)) * 22
	}
	n := len(nodes)
	bandH := heightPt / float64(n)
	var children []VMLShape
	for i, name := range nodes {
		topW := widthPt * float64(i+1) / float64(n) * 0.5
		botW := widthPt * float64(i+2) / float64(n) * 0.5
		if i == 0 {
			topW = widthPt * 0.05
		}
		if i == n-1 {
			botW = widthPt * 0.9
		}
		yTop := float64(i) * bandH
		yBot := yTop + bandH
		cx := widthPt / 2
		// Polyline trapezoid (closed).
		children = append(children, VMLShape{
			Kind:           "polyline",
			StrokeColor:    "808080",
			StrokeWeightPt: 0.75,
			FillColor:      paletteForIndex(i),
			Points: formatPolyPoints(
				cx-topW/2, yTop,
				cx+topW/2, yTop,
				cx+botW/2, yBot,
				cx-botW/2, yBot,
				cx-topW/2, yTop,
			),
		})
		// Centered label.
		children = append(children, VMLShape{
			Kind:           "rect",
			WidthPt:        botW,
			HeightPt:       bandH,
			OffsetXPt:      cx - botW/2,
			OffsetYPt:      yTop,
			StrokeColor:    "",
			StrokeWeightPt: 0,
			TextBox:        name,
		})
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// synthMatrix lays nodes in a grid with ceil(sqrt(N)) columns.
func synthMatrix(nodes []string, widthPt, heightPt float64) *VMLShape {
	n := len(nodes)
	cols := 1
	for cols*cols < n {
		cols++
	}
	rows := (n + cols - 1) / cols
	cellW := widthPt / float64(cols)
	cellH := heightPt / float64(rows)
	var children []VMLShape
	for i, name := range nodes {
		col := i % cols
		row := i / cols
		children = append(children, VMLShape{
			Kind:           "rect",
			WidthPt:        cellW - 2,
			HeightPt:       cellH - 2,
			OffsetXPt:      float64(col)*cellW + 1,
			OffsetYPt:      float64(row)*cellH + 1,
			FillColor:      paletteForIndex(i),
			StrokeColor:    "808080",
			StrokeWeightPt: 0.5,
			TextBox:        name,
		})
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// synthRadial places the first node at the center and the rest evenly
// around a ring, with a connector from center to each surrounding node.
func synthRadial(nodes []string, widthPt, heightPt float64) *VMLShape {
	side := widthPt
	if heightPt < side {
		side = heightPt
	}
	cx := widthPt / 2
	cy := heightPt / 2
	radius := side*0.4 - 12
	if radius < 24 {
		radius = 24
	}
	centerW := 90.0
	centerH := 32.0
	var children []VMLShape
	children = append(children, VMLShape{
		Kind:           "roundrect",
		WidthPt:        centerW,
		HeightPt:       centerH,
		OffsetXPt:      cx - centerW/2,
		OffsetYPt:      cy - centerH/2,
		FillColor:      "D6E4FF",
		StrokeColor:    "808080",
		StrokeWeightPt: 0.75,
		CornerArc:      6,
		TextBox:        nodes[0],
	})
	rest := nodes[1:]
	if len(rest) == 0 {
		return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
	}
	boxW := 70.0
	boxH := 24.0
	for i, name := range rest {
		angle := 2*pi*float64(i)/float64(len(rest)) - pi/2
		px := cx + radius*cosApprox(angle)
		py := cy + radius*sinApprox(angle)
		children = append(children,
			VMLShape{Kind: "polyline", StrokeColor: "A0A0A0", StrokeWeightPt: 0.5, Points: formatPolyPoints(cx, cy, px, py)},
			VMLShape{
				Kind:           "roundrect",
				WidthPt:        boxW,
				HeightPt:       boxH,
				OffsetXPt:      px - boxW/2,
				OffsetYPt:      py - boxH/2,
				FillColor:      "EEEEEE",
				StrokeColor:    "808080",
				StrokeWeightPt: 0.75,
				CornerArc:      4,
				TextBox:        name,
			},
		)
	}
	return &VMLShape{Kind: "group", WidthPt: widthPt, HeightPt: heightPt, Children: children, CoordSizeW: widthPt, CoordSizeH: heightPt}
}

// paletteForIndex returns a soft fill color so adjacent shapes stand
// apart visually. Cycles through a small palette so a 12-band pyramid
// doesn't run out of colors.
func paletteForIndex(i int) string {
	palette := []string{"E8F1FF", "FFEFE0", "EEF6E2", "F4E2F4", "E0F4F4", "FFF4D6"}
	if i < 0 {
		i = 0
	}
	return palette[i%len(palette)]
}

// Math approximations: avoid pulling in math just for trig at the
// SmartArt synthesis layer. cosApprox / sinApprox use Taylor truncation
// with a domain-reduce to [-π, π]. Adequate precision for visual layout.
const pi = 3.141592653589793

func cosApprox(x float64) float64 {
	for x > pi {
		x -= 2 * pi
	}
	for x < -pi {
		x += 2 * pi
	}
	x2 := x * x
	return 1 - x2/2 + x2*x2/24 - x2*x2*x2/720
}

func sinApprox(x float64) float64 {
	for x > pi {
		x -= 2 * pi
	}
	for x < -pi {
		x += 2 * pi
	}
	x2 := x * x
	return x - x*x2/6 + x*x2*x2/120 - x*x2*x2*x2/5040
}

// formatPolyPoints formats a flat float list as the "x,y x,y …" string
// shape the VML painter expects for polyline Points.
func formatPolyPoints(coords ...float64) string {
	var b strings.Builder
	for i := 0; i+1 < len(coords); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatFloat(coords[i], 'f', -1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(coords[i+1], 'f', -1, 64))
	}
	return b.String()
}

// extractTxBodyText concatenates the text inside an <a:txBody> element —
// the same shape Word uses for chart titles and dsp:sp captions. We
// preserve paragraph breaks as single spaces.
func extractTxBodyText(dec *xml.Decoder, start xml.StartElement) string {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return strings.TrimSpace(sb.String())
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "p" && sb.Len() > 0 {
				sb.WriteByte(' ')
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			s := string(t)
			if s != "" {
				sb.WriteString(s)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}
