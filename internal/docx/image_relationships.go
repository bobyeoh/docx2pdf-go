package docx

import (
	"archive/zip"
	"bytes"
	"image"
	"io/fs"
	"net/url"
	"path"
	"strings"
)

const mainDocumentPart = "word/document.xml"

// relatedPart resolves an internal relationship against its owning part.
// Package-absolute targets begin with /, while ZIP entry names do not.
func relatedPart(files map[string]*zip.File, source string, rel relEntry) *zip.File {
	if rel.External || rel.Target == "" {
		return nil
	}
	target, err := url.Parse(rel.Target)
	if err != nil || target.IsAbs() || target.Host != "" || target.RawQuery != "" ||
		target.ForceQuery || strings.ContainsAny(rel.Target, `#\`) || strings.HasPrefix(rel.Target, "//") {
		return nil
	}
	base := path.Dir(source) + "/"
	if strings.HasPrefix(rel.Target, "/") {
		base = ""
	}
	name := path.Clean(resolveRelTarget(base, rel.Target))
	if !fs.ValidPath(name) || name == "." {
		return nil
	}
	return files[name]
}

func isImageRel(t string) bool {
	return strings.HasSuffix(t, "/image")
}

func loadPartImages(files map[string]*zip.File, pctx *parseDocContext) {
	part := pctx.imagePart
	relsPart := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	f := files[relsPart]
	if f == nil {
		return
	}
	rels := map[string]relEntry{}
	// Image relationships in a header or footer are optional. Continue
	// parsing its text and tables even if the relationship part is unreadable.
	if err := parseRels(f, rels); err != nil {
		return
	}
	for rid, rel := range rels {
		if !isImageRel(rel.Type) {
			continue
		}
		if zf := relatedPart(files, part, rel); zf != nil {
			loadImagePart(zf, pctx.doc, pctx.imageID(rid))
		}
	}
}

func loadImagePart(zf *zip.File, doc *Document, id string) {
	extension := strings.ToLower(path.Ext(zf.Name))
	switch extension {
	case ".svg":
		if img, err := rasterizeSVGAsset(zf); err == nil {
			doc.Images[id] = img
		} else {
			markUnsupportedMedia(doc, id, "SVG")
		}
		return
	case ".eps":
		// A DOS EPS may contain a raster preview suitable for inline
		// display. Without one, the renderer shows an EPS placeholder.
		if img, ok := extractEPSPreview(zf); ok {
			doc.Images[id] = img
		} else {
			markUnsupportedMedia(doc, id, "EPS")
		}
		return
	}

	if label, ok := vectorMediaFormat(zf.Name, zf); ok {
		markUnsupportedMedia(doc, id, label)
		if extension == ".emf" {
			// EMF may wrap a bitmap. Try that before replaying its vector
			// records into a shape. The renderer also has a placeholder.
			if pngBytes, ok := emfToDIBPng(zf); ok {
				if img, _, err := image.Decode(bytes.NewReader(pngBytes)); err == nil {
					doc.Images[id] = img
					delete(doc.UnsupportedMedia, id)
					return
				}
			}
			if shape := emfToVMLShape(zf); shape != nil {
				if doc.EMFShapes == nil {
					doc.EMFShapes = map[string]*VMLShape{}
				}
				doc.EMFShapes[id] = shape
			}
		}
		return
	}
	if img, err := loadImage(zf); err == nil {
		doc.Images[id] = img
	}
}

func markUnsupportedMedia(doc *Document, id, label string) {
	if doc.UnsupportedMedia == nil {
		doc.UnsupportedMedia = map[string]string{}
	}
	doc.UnsupportedMedia[id] = label
}
