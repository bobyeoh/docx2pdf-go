package docx

import (
	"archive/zip"
	"encoding/xml"
	"io"
)

func parsePeople(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	if doc.PeopleByID == nil {
		doc.PeopleByID = map[string]Person{}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "person" {
			continue
		}
		p := Person{Name: attr(se, "author")}
		depth := 1
		for depth > 0 {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				depth++
				if t.Name.Local == "presenceInfo" {
					p.ProviderID = attr(t, "providerId")
					p.Email = attr(t, "userId")
				}
			case xml.EndElement:
				depth--
			}
		}
		if p.Name != "" {
			p.ID = p.Name
			doc.PeopleByID[p.Name] = p
		}
	}
}

// parseCommentsIds reads word/commentsIds.xml (Office 2016 w16cid namespace).
// Maps each comment's paraId to a stable durableId. The structure is:
//
//	<w16cid:commentsIds>
//	  <w16cid:commentId w16cid:paraId="…" w16cid:durableId="…"/>
//	  …
//	</w16cid:commentsIds>
//
// The durableId survives copy/paste and revision merges, so consumers that
// build cross-document threading should prefer it over the volatile paraId.
func parseCommentsIds(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	if doc.CommentsExtended == nil {
		doc.CommentsExtended = map[string]CommentExtended{}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "commentId" {
			continue
		}
		paraID := attr(se, "paraId")
		durable := attr(se, "durableId")
		if paraID != "" {
			ex := doc.CommentsExtended[paraID]
			ex.ParaID = paraID
			ex.DurableID = durable
			doc.CommentsExtended[paraID] = ex
		}
		_ = dec.Skip()
	}
}

func parseCommentsExtended(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	if doc.CommentsExtended == nil {
		doc.CommentsExtended = map[string]CommentExtended{}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "commentEx" {
			continue
		}
		paraID := attr(se, "paraId")
		if paraID == "" {
			_ = dec.Skip()
			continue
		}
		doc.CommentsExtended[paraID] = CommentExtended{
			ParaID:       paraID,
			ParentParaID: attr(se, "paraIdParent"),
			Done:         attr(se, "done") == "1" || attr(se, "done") == "true",
		}
		_ = dec.Skip()
	}
}

func parseComments(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	pctx := &parseDocContext{doc: doc}
	if doc.Comments == nil {
		doc.Comments = map[string][]Block{}
	}
	if doc.CommentMeta == nil {
		doc.CommentMeta = map[string]CommentMeta{}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "comment" {
			continue
		}
		id := attr(se, "id")
		meta := CommentMeta{
			Author:   attr(se, "author"),
			Date:     attr(se, "date"),
			Initials: attr(se, "initials"),
		}
		blocks, paraID, err := parseCommentBody(dec, se, pctx)
		if err != nil {
			return err
		}
		meta.ParaID = paraID
		if id != "" {
			doc.Comments[id] = blocks
			doc.CommentMeta[id] = meta
		}
	}
}

func parseCommentBody(dec *xml.Decoder, start xml.StartElement, pctx *parseDocContext) ([]Block, string, error) {
	var blocks []Block
	paraID := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				if paraID == "" {
					paraID = attr(t, "paraId")
				}
				p, err := decodeParagraph(dec, t, pctx)
				if err != nil {
					return nil, "", err
				}
				blocks = append(blocks, p)
			case "tbl":
				tbl, err := decodeTable(dec, t, pctx)
				if err != nil {
					return nil, "", err
				}
				blocks = append(blocks, tbl)
			default:
				_ = dec.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return blocks, paraID, nil
			}
		}
	}
}
