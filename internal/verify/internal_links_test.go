package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

func TestUnresolvedInternalLink(t *testing.T) {
	dir := t.TempDir()
	in := newDocx().Body(`
    <w:p>
      <w:hyperlink w:anchor="target"><w:r><w:t>Forward</w:t></w:r></w:hyperlink>
    </w:p>
    <w:p>
      <w:hyperlink w:anchor="missing"><w:r><w:t>Missing target</w:t></w:r></w:hyperlink>
    </w:p>
    <w:p>
      <w:hyperlink r:id="external"><w:r><w:t>External</w:t></w:r></w:hyperlink>
    </w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p>
      <w:bookmarkStart w:id="1" w:name="target"/><w:bookmarkEnd w:id="1"/>
      <w:r><w:t>Second page</w:t></w:r>
    </w:p>`).Rels(`
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="external" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
    Target="https://example.com/" TargetMode="External"/>
</Relationships>`).Write(t, dir)

	out := filepath.Join(dir, "links.pdf")
	if err := docx2pdf.Convert(in, out, docx2pdf.Options{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Follow the first page's annotation references, including objects
	// whose missing destination leaves their entire body empty.
	object := regexp.MustCompile(`(?ms)^(\d+) 0 obj\n(.*?)^endobj$`)
	annots := regexp.MustCompile(`/Annots \[([^\]]*)\]`)
	ref := regexp.MustCompile(`(\d+) 0 R`)
	var pages []string
	objects := map[string]string{}
	for _, match := range object.FindAllStringSubmatch(string(raw), -1) {
		objects[match[1]] = match[2]
		if strings.Contains(match[2], "/Type /Page\n") {
			pages = append(pages, match[1])
		}
	}
	if len(pages) != 2 {
		t.Fatalf("found %d page objects, want 2", len(pages))
	}
	match := annots.FindStringSubmatch(objects[pages[0]])
	if match == nil {
		t.Fatal("first page has no annotations")
	}
	links := ref.FindAllStringSubmatch(match[1], -1)
	if len(links) != 2 {
		t.Errorf("first page has %d annotations, want one internal and one external", len(links))
	}
	var hasForward, hasExternal bool
	for _, link := range links {
		body := objects[link[1]]
		if !strings.Contains(body, "/Subtype /Link") {
			t.Errorf("annotation %s does not resolve to a link dictionary", link[1])
		}
		hasForward = hasForward || strings.Contains(body, fmt.Sprintf("/Dest [%s 0 R /XYZ", pages[1]))
		hasExternal = hasExternal || strings.Contains(body, "/URI (https://example.com/)")
	}
	if !hasForward || !hasExternal {
		t.Errorf("first page links: forward to page 2 = %t, external URL = %t", hasForward, hasExternal)
	}

	t.Run("visible text", func(t *testing.T) {
		requireTool(t, "pdftotext")
		txt := pdftotext(t, out)
		for _, want := range []string{"Forward", "Missing target", "External", "Second page"} {
			if !strings.Contains(txt, want) {
				t.Errorf("missing visible text %q", want)
			}
		}
	})
}
