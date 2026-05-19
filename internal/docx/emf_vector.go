package docx

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"strings"
)

// emf_vector.go extracts geometric primitives from an EMF stream into a
// VMLShape group so the renderer can paint a faithful approximation of
// the original metafile content without a full GDI replay engine.
//
// Supported records (MS-EMF 2.3.5):
//
//	EMR_HEADER(1)         — bounds rectangle (used for the group's
//	                         coordinate space)
//	EMR_RECTANGLE(43)     — outline rectangle
//	EMR_ELLIPSE(42)       — outline ellipse
//	EMR_POLYGON(3)        — closed polygon
//	EMR_POLYLINE(4)       — open polyline
//	EMR_POLYGON16(86)     — packed-int16 polygon
//	EMR_POLYLINE16(87)    — packed-int16 polyline
//	EMR_LINETO(54)        — start→end line segment (current pos tracked)
//	EMR_MOVETOEX(27)      — update current pos
//
// Color and stroke width come from the most recently selected pen via
// EMR_CREATEPEN(38) + EMR_SELECTOBJECT(37). When no pen has been
// selected we use a 0.75pt black stroke as a reasonable default.

// emfToVMLShape walks data for vector records and returns a VMLShape
// "group" carrying each primitive as a child. Returns nil when no
// drawable primitives were found. The caller uses this to upgrade the
// placeholder rendering for EMF metafiles to actual vector output.
func emfToVMLShape(f *zip.File) *VMLShape {
	rc, err := openZipFile(f)
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return emfBytesToVMLShape(data)
}

func emfBytesToVMLShape(data []byte) *VMLShape {
	const (
		emrHeader       = 1
		emrPolygon      = 3
		emrPolyline     = 4
		emrSetTextColor = 24
		emrMoveToEx     = 27
		emrCreatePen    = 38
		emrSelectObject = 37
		emrEllipse      = 42
		emrRectangle    = 43
		emrRoundRect    = 44
		emrArc          = 45
		emrChord        = 46
		emrPie          = 47
		emrLineTo       = 54
		emrPolyBezier16 = 85
		emrPolygon16    = 86
		emrPolyline16   = 87
		emrPolyBezier   = 2
		emrExtTextOutW  = 84
	)
	var children []VMLShape
	bounds := [4]int32{}
	curX, curY := int32(0), int32(0)
	curStroke := "000000"
	curWeight := 0.75
	// Object table — EMR_CREATEPEN sticks at index, EMR_SELECTOBJECT
	// activates it.
	type pen struct {
		color  string
		weight float64
	}
	pens := map[uint32]pen{}
	for off := 0; off+8 <= len(data); {
		recType := binary.LittleEndian.Uint32(data[off:])
		recSize := binary.LittleEndian.Uint32(data[off+4:])
		if recSize < 8 || int(recSize) > len(data)-off {
			break
		}
		end := off + int(recSize)
		body := data[off+8 : end]
		switch recType {
		case emrHeader:
			if len(body) >= 16 {
				bounds[0] = readI32(body, 0)
				bounds[1] = readI32(body, 4)
				bounds[2] = readI32(body, 8)
				bounds[3] = readI32(body, 12)
			}
		case emrCreatePen:
			// 4-byte index | LOGPEN (4 style + 8 width point + 4 color)
			if len(body) >= 20 {
				idx := binary.LittleEndian.Uint32(body[0:])
				// width.x is int32 at body[8..12]
				w := float64(readI32(body, 8))
				if w <= 0 {
					w = 0.75
				}
				colDword := binary.LittleEndian.Uint32(body[16:])
				pens[idx] = pen{
					color:  cdRGBToHex(colDword),
					weight: w,
				}
			}
		case emrSelectObject:
			if len(body) >= 4 {
				idx := binary.LittleEndian.Uint32(body[0:])
				if p, ok := pens[idx]; ok {
					curStroke = p.color
					if p.weight > 0 {
						curWeight = p.weight
					}
				}
			}
		case emrMoveToEx:
			if len(body) >= 8 {
				curX = readI32(body, 0)
				curY = readI32(body, 4)
			}
		case emrLineTo:
			if len(body) >= 8 {
				x := readI32(body, 0)
				y := readI32(body, 4)
				children = append(children, lineShape(curX, curY, x, y, curStroke, curWeight))
				curX, curY = x, y
			}
		case emrRectangle:
			if len(body) >= 16 {
				l := readI32(body, 0)
				t := readI32(body, 4)
				r := readI32(body, 8)
				bt := readI32(body, 12)
				children = append(children, VMLShape{
					Kind:           "rect",
					OffsetXPt:      float64(l),
					OffsetYPt:      float64(t),
					WidthPt:        float64(r - l),
					HeightPt:       float64(bt - t),
					StrokeColor:    curStroke,
					StrokeWeightPt: curWeight,
				})
			}
		case emrEllipse:
			if len(body) >= 16 {
				l := readI32(body, 0)
				t := readI32(body, 4)
				r := readI32(body, 8)
				bt := readI32(body, 12)
				children = append(children, VMLShape{
					Kind:           "oval",
					OffsetXPt:      float64(l),
					OffsetYPt:      float64(t),
					WidthPt:        float64(r - l),
					HeightPt:       float64(bt - t),
					StrokeColor:    curStroke,
					StrokeWeightPt: curWeight,
				})
			}
		case emrPolygon, emrPolyline:
			if len(body) >= 20 {
				n := binary.LittleEndian.Uint32(body[16:])
				pts := body[20:]
				kind := "polyline"
				if recType == emrPolygon {
					kind = "polygon"
				}
				children = append(children, makePolyShape(kind, pts, int(n), false, curStroke, curWeight))
			}
		case emrPolygon16, emrPolyline16:
			if len(body) >= 20 {
				n := binary.LittleEndian.Uint32(body[16:])
				pts := body[20:]
				kind := "polyline"
				if recType == emrPolygon16 {
					kind = "polygon"
				}
				children = append(children, makePolyShape(kind, pts, int(n), true, curStroke, curWeight))
			}
		case emrRoundRect:
			// EMR_ROUNDRECT: 16 bytes for the bounding rect (l,t,r,b) +
			// 8 bytes for the corner ellipse (cornerWidth, cornerHeight).
			// The corners are quarter-ellipses; we collapse to roundrect.
			if len(body) >= 16 {
				l := readI32(body, 0)
				t := readI32(body, 4)
				r := readI32(body, 8)
				bt := readI32(body, 12)
				children = append(children, VMLShape{
					Kind:           "roundrect",
					OffsetXPt:      float64(l),
					OffsetYPt:      float64(t),
					WidthPt:        float64(r - l),
					HeightPt:       float64(bt - t),
					StrokeColor:    curStroke,
					StrokeWeightPt: curWeight,
				})
			}
		case emrArc, emrChord, emrPie:
			// EMR_ARC / EMR_CHORD / EMR_PIE: 16 bytes for the bounding
			// rect + 16 bytes for two control points (start/end of the
			// arc). For PDF we can't easily draw a partial ellipse with
			// our primitives, so we degrade to the full oval bounded by
			// the same rect. PIE further degrades to oval (the chord
			// segments would need path support to draw correctly).
			if len(body) >= 16 {
				l := readI32(body, 0)
				t := readI32(body, 4)
				r := readI32(body, 8)
				bt := readI32(body, 12)
				children = append(children, VMLShape{
					Kind:           "oval",
					OffsetXPt:      float64(l),
					OffsetYPt:      float64(t),
					WidthPt:        float64(r - l),
					HeightPt:       float64(bt - t),
					StrokeColor:    curStroke,
					StrokeWeightPt: curWeight,
				})
			}
		case emrPolyBezier, emrPolyBezier16:
			// EMR_POLYBEZIER(16): bounds rect (16) + count (4) + (count*8 or 4)
			// bytes of points. A polybezier is N cubic Bezier segments;
			// we don't have cubic-path primitives, so we collapse to a
			// polyline through the anchor points (every 3rd vertex
			// starting from the first). This loses the curvature but
			// preserves the path's gross outline and footprint.
			if len(body) >= 20 {
				n := binary.LittleEndian.Uint32(body[16:])
				pts := body[20:]
				packed := recType == emrPolyBezier16
				stride := 8
				if packed {
					stride = 4
				}
				// Sample every 3rd point (the anchors); leave control
				// points out so the polyline approximation hugs the curve.
				var sampled []float64
				for i := uint32(0); i < n && int(i)*stride+stride <= len(pts); i += 3 {
					var x, y int32
					if packed {
						x = int32(readI16(pts, int(i)*stride))
						y = int32(readI16(pts, int(i)*stride+2))
					} else {
						x = readI32(pts, int(i)*stride)
						y = readI32(pts, int(i)*stride+4)
					}
					sampled = append(sampled, float64(x), float64(y))
				}
				if len(sampled) >= 4 {
					children = append(children, VMLShape{
						Kind:           "polyline",
						Points:         emfPolyPoints(sampled),
						StrokeColor:    curStroke,
						StrokeWeightPt: curWeight,
					})
				}
			}
		case emrExtTextOutW:
			// Already handled by extractEMFText. Skip here so the
			// vector pass doesn't double-emit.
		}
		off = end
	}
	if len(children) == 0 {
		return nil
	}
	w := float64(bounds[2] - bounds[0])
	h := float64(bounds[3] - bounds[1])
	if w <= 0 {
		w = 320
	}
	if h <= 0 {
		h = 240
	}
	return &VMLShape{
		Kind:       "group",
		WidthPt:    w,
		HeightPt:   h,
		CoordSizeW: w,
		CoordSizeH: h,
		Children:   children,
	}
}

func readI32(b []byte, off int) int32 {
	if off+4 > len(b) {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(b[off:]))
}

func readI16(b []byte, off int) int16 {
	if off+2 > len(b) {
		return 0
	}
	return int16(binary.LittleEndian.Uint16(b[off:]))
}

// cdRGBToHex converts a Win32 COLORREF (0x00BBGGRR) to a 6-hex RGB.
func cdRGBToHex(c uint32) string {
	r := c & 0xFF
	g := (c >> 8) & 0xFF
	b := (c >> 16) & 0xFF
	hex := "0123456789ABCDEF"
	out := []byte{
		hex[(r>>4)&0xF], hex[r&0xF],
		hex[(g>>4)&0xF], hex[g&0xF],
		hex[(b>>4)&0xF], hex[b&0xF],
	}
	return string(out)
}

// lineShape builds a 2-point polyline.
func lineShape(x0, y0, x1, y1 int32, stroke string, weight float64) VMLShape {
	return VMLShape{
		Kind:           "polyline",
		Points:         emfPolyPoints([]float64{float64(x0), float64(y0), float64(x1), float64(y1)}),
		StrokeColor:    stroke,
		StrokeWeightPt: weight,
	}
}

// makePolyShape walks an int32 (or int16 when packed=true) point buffer
// and returns a polyline/polygon VMLShape.
func makePolyShape(kind string, raw []byte, n int, packed bool, stroke string, weight float64) VMLShape {
	pts := make([]float64, 0, n*2)
	stride := 8
	if packed {
		stride = 4
	}
	for i := 0; i < n && i*stride+stride <= len(raw); i++ {
		var x, y int32
		if packed {
			x = int32(readI16(raw, i*stride))
			y = int32(readI16(raw, i*stride+2))
		} else {
			x = readI32(raw, i*stride)
			y = readI32(raw, i*stride+4)
		}
		pts = append(pts, float64(x), float64(y))
	}
	return VMLShape{
		Kind:           kind,
		Points:         emfPolyPoints(pts),
		StrokeColor:    stroke,
		StrokeWeightPt: weight,
	}
}

// emfPolyPoints formats a list of x,y pairs in the "x1,y1 x2,y2 …"
// shape VMLShape.Points uses.
func emfPolyPoints(coords []float64) string {
	var b strings.Builder
	for i := 0; i+1 < len(coords); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeFloat(&b, coords[i])
		b.WriteByte(',')
		writeFloat(&b, coords[i+1])
	}
	return b.String()
}

// writeFloat appends a compact decimal representation to b.
func writeFloat(b *strings.Builder, v float64) {
	if v == float64(int64(v)) {
		// Integer-valued — skip the decimal point.
		intToBuilder(b, int64(v))
		return
	}
	// One decimal place is enough for the device units we encode.
	whole := int64(v)
	frac := int64((v - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	intToBuilder(b, whole)
	b.WriteByte('.')
	intToBuilder(b, frac)
}

func intToBuilder(b *strings.Builder, n int64) {
	if n == 0 {
		b.WriteByte('0')
		return
	}
	if n < 0 {
		b.WriteByte('-')
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(buf[i:])
}
