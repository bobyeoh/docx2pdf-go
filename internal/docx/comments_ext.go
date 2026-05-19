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

// parseThreadedComments reads word/threadedComments.xml (Office 2016+
// w15 namespace). Each w15:threadedComment carries:
//   - w15:id     — comment id (matches the w:comment/@w:id in comments.xml)
//   - w15:done   — resolved flag ("1"/"true")
//   - parent     — id of the comment this is a reply to
//   - author / personId / dT — author metadata
//
// We fold this back into doc.CommentsExtended (keyed by paraId, which we
// recover from doc.CommentMeta) so the renderer's existing thread sort and
// resolved-state rendering keep working without duplicating logic.
func parseThreadedComments(f *zip.File, doc *Document) error {
	rc, err := openZipFile(f)
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	if doc.CommentsExtended == nil {
		doc.CommentsExtended = map[string]CommentExtended{}
	}
	// Build a reverse map commentID → paraID so we can join with the
	// existing keying.
	commentIDToPara := map[string]string{}
	for cid, meta := range doc.CommentMeta {
		if meta.ParaID != "" {
			commentIDToPara[cid] = meta.ParaID
		}
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
		if se.Name.Local != "threadedComment" {
			continue
		}
		id := attr(se, "id")
		parent := attr(se, "parent")
		done := attr(se, "done")
		paraID := commentIDToPara[id]
		// If we don't have a paraId mapping yet (threaded-only docs that
		// skip the w:comment surface), keyed by the threaded id directly.
		if paraID == "" {
			paraID = id
		}
		ex := doc.CommentsExtended[paraID]
		ex.ParaID = paraID
		if parent != "" {
			// Convert parent comment id → parent paraId when known.
			if pp := commentIDToPara[parent]; pp != "" {
				ex.ParentParaID = pp
			} else if ex.ParentParaID == "" {
				ex.ParentParaID = parent
			}
		}
		if done == "1" || done == "true" {
			ex.Done = true
		}
		doc.CommentsExtended[paraID] = ex
		_ = dec.Skip()
	}
}

// parseCommentsExtensible reads word/commentsExtensible.xml (Office 365
// w16cex namespace). Each w16cex:commentExtensible declares:
//   - w16cex:durableId  — yet another stable id
//   - w16cex:dateUtc    — round-trippable timestamp
//
// We store the durable id alongside the existing CommentsExtended record
// (keyed by paraId via the comments.xml mapping). When both w16cid and
// w16cex declare the same comment, the w16cex value wins because it's the
// newer format.
func parseCommentsExtensible(f *zip.File, doc *Document) error {
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
		if se.Name.Local != "commentExtensible" {
			continue
		}
		// The element keys itself by durableId (w16cex's primary id) and
		// optionally carries paraId. Match either form.
		paraID := attr(se, "paraId")
		durable := attr(se, "durableId")
		if paraID == "" {
			paraID = durable
		}
		if paraID != "" {
			ex := doc.CommentsExtended[paraID]
			ex.ParaID = paraID
			if durable != "" {
				ex.DurableID = durable
			}
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
