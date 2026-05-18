package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"io"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// rasterizeSVGAsset reads an SVG part from the docx zip and renders it to
// a raster image.Image so the existing image pipeline (cropping, effects,
// gopdf drawing) can consume it without further changes. We render at 2x
// the SVG's intrinsic viewBox so subsequent down-scaling stays sharp on
// hi-dpi viewers; absent an intrinsic size we fall back to 512x512.
//
// The point of doing this is that Office 365 emits both a raster <a:blip>
// preview and an <asvg:svgBlip> vector source for every inserted SVG. We
// used to silently take the (low-resolution) raster preview; this path
// lets us prefer the vector and produce sharp output.
func rasterizeSVGAsset(zf *zip.File) (image.Image, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("oksvg: %w", err)
	}
	w := int(icon.ViewBox.W * 2)
	h := int(icon.ViewBox.H * 2)
	if w <= 0 || h <= 0 {
		w, h = 512, 512
	}
	if w > 4096 {
		w = 4096
	}
	if h > 4096 {
		h = 4096
	}
	icon.SetTarget(0, 0, float64(w), float64(h))
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)
	return rgba, nil
}
