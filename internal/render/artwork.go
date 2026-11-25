package render

import (
	"image"

	"github.com/bobyeoh/docx2pdf-go/internal/docx"
)

type pageArtwork struct {
	drawing atom
	x, y    float64
	image   image.Image
}

// hasPageArtwork looks for page-sized coordinate anchors attached to body
// paragraphs. Header/footer drawings are laid out by stampPageDecorations.
func hasPageArtwork(doc *docx.Document) bool {
	has := func(blocks []docx.Block) bool {
		for _, block := range blocks {
			if p, ok := block.(docx.Paragraph); ok {
				for _, run := range p.Runs {
					if run.ImageAnchored && run.AnchorWrap == "behind" &&
						run.AnchorRelativeH == "page" && run.AnchorRelativeV == "page" {
						return true
					}
				}
			}
		}
		return false
	}
	if len(doc.Sections) == 0 {
		return has(doc.Body)
	}
	for _, section := range doc.Sections {
		if has(section.Blocks) {
			return true
		}
	}
	return false
}

func (r *renderer) paintPageArtwork() {
	if !r.opts.collectPageArtwork {
		for _, artwork := range r.opts.pageArtwork[r.pdf.GetNumberOfPages()] {
			r.paintFloatingArtwork(artwork)
		}
	}
}

// A solid sheet covering the page sits in front of running headers and
// footers in Word. The decoration pass runs after body layout, so skip
// decorations that are fully hidden by this sheet. Allow for the rounding
// difference between DrawingML coordinates and the declared page size.
func (r *renderer) artworkCoversPage(page int) bool {
	for _, artwork := range r.opts.pageArtwork[page] {
		a := artwork.drawing
		s := a.shape
		if s != nil && s.Kind == "rect" && s.FillColor != "" && s.RotationDeg == 0 &&
			artwork.x <= 0.5 && artwork.y <= 0.5 &&
			artwork.x+a.width >= r.pageW-1 && artwork.y+a.height >= r.pageH-1 {
			return true
		}
	}
	return false
}

func (r *renderer) paintFloatingArtwork(artwork pageArtwork) {
	a, x, y := artwork.drawing, artwork.x, artwork.y
	if artwork.image != nil {
		_ = r.drawTransformedImage(artwork.image, x, y, a.width, a.height, a.imageRotationDeg, a.imageFlipH, a.imageFlipV)
	} else if a.shape != nil {
		drawVMLShape(r, a.shape, x, y, a.width, a.height)
	}
}
