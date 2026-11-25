package verify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
	"golang.org/x/image/font/gofont/goregular"
)

func TestPDFOutlineFromParagraphStyles(t *testing.T) {
	dir := t.TempDir()
	input := newDocx().Styles(`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:style w:type="paragraph" w:styleId="CustomSection">
			<w:pPr><w:outlineLvl w:val="1"/></w:pPr>
		</w:style>
		<w:style w:type="paragraph" w:styleId="InheritedSection">
			<w:basedOn w:val="CustomSection"/>
		</w:style>
	</w:styles>`).Body(`
		<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Chapter</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="CustomSection"/></w:pPr><w:r><w:t>Custom</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="InheritedSection"/></w:pPr><w:r><w:t>Inherited</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="InheritedSection"/><w:outlineLvl w:val="2"/></w:pPr><w:r><w:t>Nested</w:t></w:r></w:p>
		<w:p><w:pPr><w:outlineLvl w:val="1"/></w:pPr><w:r><w:t>Direct</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="Heading2"/><w:outlineLvl w:val="9"/></w:pPr><w:r><w:t>Body</w:t></w:r></w:p>
		<w:p><w:pPr><w:spacing w:after="16000"/></w:pPr><w:r><w:t>End of first page</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Next page</w:t></w:r></w:p>
	`).Write(t, dir)
	font := filepath.Join(dir, "font.ttf")
	if err := os.WriteFile(font, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "test.pdf")
	if err := convert.Convert(input, output, convert.Options{FontRegular: font}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	// Outline titles are UTF-16BE and each child refers to its parent's PDF object.
	objects := regexp.MustCompile(`(?m)^(\d+) 0 obj\r?$\s*<<\s*/Parent (\d+) 0 R[^>]*?/Dest \[\s*(\d+) 0 R[^\]]*\][^>]*?/Title <FEFF([0-9A-F]+)>`)
	ids, parents, destinations := map[string]string{}, map[string]string{}, map[string]string{}
	for _, match := range objects.FindAllSubmatch(data, -1) {
		name := string(match[4])
		ids[name] = string(match[1])
		parents[name] = string(match[2])
		destinations[name] = string(match[3])
	}
	title := func(s string) string {
		var buf strings.Builder
		for _, r := range s {
			fmt.Fprintf(&buf, "%04X", r)
		}
		return buf.String()
	}
	for _, entry := range []struct{ name, parent string }{
		{"Custom", "Chapter"}, {"Inherited", "Chapter"}, {"Nested", "Inherited"}, {"Direct", "Chapter"},
	} {
		id := ids[title(entry.parent)]
		if id == "" || parents[title(entry.name)] != id {
			t.Errorf("outline %q does not belong to %q", entry.name, entry.parent)
		}
	}
	pages := regexp.MustCompile(`(?m)^(\d+) 0 obj?$\s*<<\s*/Type /Page\s`).FindAllSubmatch(data, -1)
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if got := destinations[title("Next page")]; got != string(pages[1][1]) {
		t.Errorf("last heading points to object %q, want its rendered page %q", got, pages[1][1])
	}
	if bytes.Contains(data, []byte("/Title <FEFF"+title("Body")+">")) {
		t.Error("body-text outline level produced a bookmark")
	}
}
