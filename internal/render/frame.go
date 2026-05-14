package render

import (
	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

// drawFrame renders a paragraph that carries a w:framePr positioning
// block ("floating" frame in OOXML terms). The frame is drawn at its
// resolved absolute position and at the declared width; the renderer's
// flow cursor (cursorY) is left untouched so subsequent body paragraphs
// continue where they were.
//
// What this does NOT do (and the README says so): true text-wrap
// exclusion. Body text whose y-range overlaps the frame's y-range will
// draw at its normal x-position and may visually overlap the frame.
// Implementing real wrap requires per-line shape exclusion in
// layoutLine, which is a separate, larger piece of work. docx4j's FOP
// pipeline has the same limitation.
func (r *renderer) drawFrame(p docx.Paragraph) error {
	fr := p.Frame
	// Resolve frame geometry. Width is required for sensible rendering;
	// without it we fall back to the section's content width so at least
	// the text is visible.
	frameW := twipsToPt(fr.WidthTwips)
	if frameW <= 0 {
		frameW = r.contentW
	}
	frameX := resolveFrameX(r, fr, frameW)
	frameY := resolveFrameY(r, fr)

	// Save flow state; restore unconditionally so the frame is "out of flow".
	savedMarL, savedMarR := r.marL, r.marR
	savedContentW := r.contentW
	savedCursorY := r.cursorY
	savedNoBreak := r.noPageBreak
	defer func() {
		r.marL, r.marR = savedMarL, savedMarR
		r.contentW = savedContentW
		r.cursorY = savedCursorY
		r.noPageBreak = savedNoBreak
	}()

	r.marL = frameX
	r.contentW = frameW
	r.cursorY = frameY
	// Suppress page-break-on-overflow inside the frame: a positioned
	// frame should stay where it was anchored, not push to a new page.
	r.noPageBreak = true

	// Strip the Frame so the inner draw doesn't recurse.
	inner := p
	inner.Frame = nil
	return r.drawParagraph(inner)
}

// resolveFrameX returns the frame's absolute left edge in points.
//
//	HAnchor=margin (default): origin = left margin (sectionMarL).
//	HAnchor=page:             origin = page edge (0).
//	HAnchor=text:             origin = current text x — we approximate
//	                          using the live marL.
//
// XAlign overrides XTwips when set: "left"/"right"/"center" reposition
// the frame relative to the anchor's text region. "inside"/"outside" need
// odd/even page knowledge — we treat them as "left"/"right" respectively.
func resolveFrameX(r *renderer, fr *docx.FrameInfo, frameW float64) float64 {
	var origin, regionW float64
	switch fr.HAnchor {
	case "page":
		origin = 0
		regionW = r.pageW
	case "text":
		origin = r.marL
		regionW = r.contentW
	default: // "margin" or unset
		origin = r.marL
		regionW = r.pageW - r.marL - r.marR
	}
	if fr.XAlign != "" {
		switch fr.XAlign {
		case "left", "inside":
			return origin
		case "center":
			return origin + (regionW-frameW)/2
		case "right", "outside":
			return origin + regionW - frameW
		}
	}
	return origin + twipsToPt(fr.XTwips)
}

// resolveFrameY returns the frame's absolute top edge in points.
//
//	VAnchor=page (default): origin = top of page (0).
//	VAnchor=margin:         origin = top margin.
//	VAnchor=text:           origin = current cursor y.
//
// YAlign mirrors XAlign — "top"/"bottom"/"center" reposition relative to
// the anchor's vertical region; otherwise YTwips applies.
func resolveFrameY(r *renderer, fr *docx.FrameInfo) float64 {
	var origin, regionH float64
	switch fr.VAnchor {
	case "margin":
		origin = r.marT
		regionH = r.pageH - r.marT - r.marB
	case "text":
		origin = r.cursorY
		regionH = r.pageH - r.cursorY - r.marB
	default: // "page" or unset
		origin = 0
		regionH = r.pageH
	}
	if fr.YAlign != "" {
		// Without knowing the frame's content height up front we treat
		// "center"/"bottom" as anchor + half/full region. Good enough for
		// fixed-height boxes (HRule="exact"); for auto-sized frames the
		// reader sees the frame near the bottom edge.
		fh := twipsToPt(fr.HeightTwips)
		switch fr.YAlign {
		case "top", "inside":
			return origin
		case "center":
			return origin + (regionH-fh)/2
		case "bottom", "outside":
			return origin + regionH - fh
		}
	}
	return origin + twipsToPt(fr.YTwips)
}
