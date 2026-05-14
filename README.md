# docx2pdf-go

> Pure-Go library and CLI that converts Microsoft Word `.docx` files to PDF.
> **No JVM. No LibreOffice. No MS Word. No CGO.** Just a single static binary
> and a `go get`-able package.

```go
import docx2pdf "github.com/bobyeoh/docx2pdf-go"

// Simplest call — picks up a common system font automatically
// (Arial / Helvetica on macOS, DejaVu / Liberation / Noto on Linux).
err := docx2pdf.Convert("report.docx", "report.pdf", docx2pdf.Options{})

// Or be explicit (recommended for reproducible cross-machine output):
err = docx2pdf.Convert("report.docx", "report.pdf", docx2pdf.Options{
    FontRegular:  "/usr/share/fonts/noto/NotoSans-Regular.ttf",
    FontFallback: "/usr/share/fonts/noto/NotoSansCJK-Regular.ttc", // CJK
    PageNumbers:  true,
})
```

---

## Why docx2pdf-go

Pure-Go `.docx` → PDF is harder than it looks. Real-world options today:

| Approach | Trade-off |
|---|---|
| Shell out to **LibreOffice** | 500 MB+ container, slow fork-per-conversion, headless quirks |
| **unioffice** | Commercial license required for closed-source apps |
| **Pandoc + LaTeX** | Heavy toolchain, fragile layout for complex Office docs |
| Hand-roll it | Months of XML wrangling, font metrics, table layout… |

**docx2pdf-go** sits in the gap: a focused, MIT-licensed, pure-Go library that
covers the 90% of WordprocessingML real documents actually use, without
external runtimes.

### What you get

- **Deploys as a ~3.5 MB static binary.** `CGO_ENABLED=0`, single file,
  runs anywhere Go runs — including `scratch` / `distroless` / Alpine
  with no fonts installed. A small Latin font (Go fonts, MIT-licensed,
  ~150 KB) is embedded in the binary as a final fallback.
- **Ships as a ~70 MB Docker image** with Noto Sans + WenQuanYi Zen Hei
  fonts baked in (Latin + CJK fallback, no Word installation needed).
- **`go get`-able library**, with a stable public surface and a streaming
  `io.Reader` → `io.Writer` API (perfect for HTTP handlers).
- **CLI** for batch processing — point it at a directory, get a mirrored
  tree of PDFs out.
- **CJK first-class**: font fallback for CJK glyphs, per-character break
  opportunities so Chinese/Japanese/Korean paragraphs wrap correctly even
  without whitespace.

---

## Quick start

### As a library

```bash
go get github.com/bobyeoh/docx2pdf-go
```

```go
import (
    "bytes"
    "io"
    "net/http"

    docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

// 1) File paths — the simplest case. Empty Options auto-detects a
//    system font; pass FontRegular for reproducible output.
err := docx2pdf.Convert("in.docx", "out.pdf", docx2pdf.Options{})

// 2) Streaming — perfect for HTTP handlers.
func handle(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    w.Header().Set("Content-Type", "application/pdf")
    _ = docx2pdf.ConvertReader(
        bytes.NewReader(body), int64(len(body)),
        w,
        docx2pdf.Options{FontRegular: fontPath},
    )
}

// 3) Parse → inspect / modify → render.
doc, _ := docx2pdf.Open("in.docx")
for _, b := range doc.Body {
    if p, ok := b.(docx2pdf.Paragraph); ok && len(p.Runs) > 0 {
        // walk the AST, redact, translate, reformat, ...
    }
}
_ = docx2pdf.Render(doc, "out.pdf", docx2pdf.Options{FontRegular: fontPath})
```

### As a CLI

```bash
# Simplest form — system font is auto-detected (Arial on macOS,
# DejaVu / Liberation / Noto on Linux).
docx2pdf -in input.docx -out output.pdf

# Explicit font (recommended for reproducibility):
docx2pdf -in input.docx -out output.pdf -font Regular.ttf

# Batch — walks a directory tree, mirrors structure to -out
docx2pdf -in indir/ -out outdir/ -font Regular.ttf \
         -font-fallback NotoSansCJK.ttc \
         -recursive -keep-going -page-numbers -v
```

### Docker

```bash
# Simplest — fonts auto-detected from $DOCX2PDF_FONT and
# $DOCX2PDF_FONT_CJK baked into the image.
docker run --rm -v "$PWD":/work bobyeoh/docx2pdf-go \
    -in /work/in.docx -out /work/out.pdf -page-numbers
```

The official image (~70 MB) ships **Noto Sans** for Latin text and
**WenQuanYi Zen Hei** for CJK fallback. Noto Sans CJK is *not*
bundled because it uses CFF/PostScript outlines (`.ttc` with `OTTO`
faces) which gopdf's TrueType-only parser can't render; WQY Zen Hei
is a TrueType TTC that the runtime extracts face 0 from automatically.

The env vars `DOCX2PDF_FONT` and `DOCX2PDF_FONT_CJK` are honored by
the binary when no `-font` / `-font-fallback` flag is given, so the
container works out of the box.

#### Running in *any* container — including `scratch` / `distroless`

The binary embeds a small Latin font as a last-resort fallback, so
even minimal images with no fonts at all produce a valid PDF for
Latin-only documents:

```dockerfile
# Multi-stage build into distroless: total size ~5 MB.
FROM golang:alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /docx2pdf ./cmd/docx2pdf

FROM gcr.io/distroless/static-debian12
COPY --from=build /docx2pdf /docx2pdf
ENTRYPOINT ["/docx2pdf"]
```

```bash
docker run --rm -v "$PWD":/work my-image \
    -in /work/in.docx -out /work/out.pdf  # no -font needed
```

CJK content in a fontless image still needs a CJK TTF — mount it and
point `$DOCX2PDF_FONT_CJK` at it:

```bash
docker run --rm -v "$PWD":/work \
    -e DOCX2PDF_FONT_CJK=/work/SimSun.ttf \
    my-image -in /work/in.docx -out /work/out.pdf
```

---

## What it actually renders

| Feature | Status |
|---|---|
| Paragraphs: alignment, indent (left + first-line + hanging), line spacing | ✅ |
| Runs: bold / italic / underline / strikethrough / color / font size | ✅ |
| Lists: decimal / bullet / lower-upper letter / lower-upper roman, multi-level, custom `start` | ✅ |
| Tables: column widths, `gridSpan` column merging, `vMerge` row merging | ✅ |
| Multi-page tables (crosses page boundaries cleanly) | ✅ |
| Inline images: PNG / JPEG / GIF | ✅ |
| Anchored images (`wp:anchor`) — rendered as inline best-effort | ✅ |
| Legacy VML images (`w:pict` / `v:imagedata`) — older Word docs, pasted content | ✅ |
| Text boxes (`wps:txbx` / `w:txbxContent`) — content extracted as inline italic; box geometry not preserved | ⚠️ |
| Paragraph styles with `basedOn` chains + `docDefaults` (rPr + pPr) | ✅ |
| Multi-section documents — different page sizes / orientations per section | ✅ |
| Headers and footers — per-section, with full block content | ✅ |
| Fields: `PAGE` / `NUMPAGES` (substituted per page); other fields fall through to their cached value so they still look right | ✅ |
| Clickable external hyperlinks (real PDF annotations) | ✅ |
| CJK font fallback + per-character line breaking for whitespace-less scripts | ✅ |
| Explicit page breaks (`w:br w:type="page"` and `w:pageBreakBefore`) | ✅ |
| Custom page size and margins from `w:sectPr` | ✅ |
| Hidden text (`w:vanish`) — suppressed from output | ✅ |
| Nested tables (table inside a cell) | ✅ |
| Cell shading (`w:shd`) + per-edge borders (single/double/dashed/dotted) | ✅ |
| Footnotes & endnotes: refs as `[N]` superscript, bodies at page bottom; endnotes as document trailer | ✅ |
| Comments (`w:commentReference` / comments.xml) — surfaced as a trailing "Comments" section | ✅ |
| PDF outline / clickable sidebar bookmarks from `Heading1..Heading9` + `Title` styles | ✅ |
| Content controls (`w:sdt`) — block and inline; transparent wrapper, text preserved | ✅ |
| Math equations (`m:oMath` / `m:oMathPara`) — text extracted as italic, structure lost | ⚠️ |
| Charts (`c:chart`) — title, axis labels, series names extracted as `[Chart: …]` text; data graphic not drawn | ⚠️ |
| Multi-column layout (`w:cols`) | ✅ |
| Tracked changes (`w:ins` / `w:del` / `w:moveFrom` / `w:moveTo`) — accept-all mode | ✅ |
| Markup compatibility wrapper (`mc:AlternateContent`) — Choice over Fallback | ✅ |
| Embedded text boxes (`wps:txbx`) — content extracted as inline italic; box geometry not preserved | ⚠️ |
| Floating frames (`w:framePr` placement) — anchored at the right page position; body text does NOT wrap around | ⚠️ |
| RTL scripts (Hebrew / Arabic) — word order reversed, right-aligned; no full UAX#9 bidi for mixed-direction lines | ⚠️ |
| Text wrap around floating images (`wp:anchor` with wrap geometry) | ❌ — anchor falls back to inline |
| SmartArt diagrams | ❌ |
| Form controls' interactive behavior (`w:sdt` is transparent, but inputs aren't interactive in the PDF) | ❌ |
| Arabic letter shaping (initial/medial/final connected forms) | ❌ |
| Embedded fonts (`w:embedRegular`) | ❌ |

If your document hinges on the "❌" rows and you need pixel-perfect
rendering, **fall back to a LibreOffice-backed service**. The "⚠️" rows
preserve content but lose some structural fidelity; check whether that's
acceptable for your use case.

---

## Architecture

```
.docx (zip)
  │
  ├─ word/styles.xml         ─▶ ParagraphStyle map + docDefaults
  ├─ word/numbering.xml      ─▶ list definitions
  ├─ word/header*.xml        ─▶ block-level header content
  ├─ word/footer*.xml        ─▶ block-level footer content
  ├─ word/_rels/...          ─▶ rId → media | hyperlink | part
  ├─ word/media/*            ─▶ image.Image objects
  └─ word/document.xml       ─▶ Section[] each carrying its own
                                Body, PageSize, Margins, H/F
                                          │
                                          ▼
                                  renderer (gopdf)
                                          │
                                          ▼
                                       .pdf
```

The pipeline mirrors **[docx4j](https://github.com/plutext/docx4j)**'s
load-then-visit architecture, but collapses the intermediate XSL-FO step:
we draw directly to PDF via [signintech/gopdf](https://github.com/signintech/gopdf).
That's why it stays small — no FOP, no JAXB, no schema binding.

### Source layout

```
docx2pdf.go                  ← public API (re-exports via type aliases)
example_test.go             ← external-package smoke tests
cmd/docx2pdf/main.go        ← CLI
internal/docx/              ← OOXML parser
internal/render/            ← PDF renderer (one file per concern):
                                pdf.go        — entry points + state
                                page.go       — H/F, page breaks, footnotes
                                paragraph.go  — paragraph + list markers
                                frame.go      — positioned (w:framePr) frames
                                text.go       — atom model + line layout
                                table.go      — drawTable, drawRow, borders
                                image.go      — fit / crop / draw
                                fonts.go      — font registration + CJK + RTL
                                fields.go     — w:fldChar / w:instrText
                                util.go       — twips / hex helpers
internal/convert/           ← thin orchestrator (parse → render)
internal/verify/            ← test harness (see below)
```

Everything under `internal/` stays private — the package boundary lets us
refactor freely. The root `docx2pdf` package re-exports types via aliases so
consumers can still `type-assert` against `docx2pdf.Paragraph`,
`docx2pdf.Table`, etc. without reaching into internals.

---

## Tested seriously

| Layer | Tests | Notes |
|---|---|---|
| Unit (parser + render) | 30+ | XML decoding, style resolution, list numbering, field codes, settings, VML, theme |
| Unit (CLI) | 2 | Directory walking, extension handling |
| Public API smoke | 3 | Library is importable from outside the module |
| End-to-end | 127 | `docx → PDF → pdftotext + pdfinfo + PNG` per case |
| Comprehensive integration | 1 | Single 30+ feature docx validated with pdftotext, bbox, PDF byte structure, and PNG pixel sampling |
| Real-world corpus | 6 | Real Word docs from the docx4j project |
| Crash resistance | 6 | Empty zip, malformed XML, circular `basedOn`, corrupt images, 500-deep nesting |
| Golden image diff | all cases | Opt-in (`GOLDEN=1`); detects visual regressions via mean L1 pixel distance |
| Fuzz | 2 | `FuzzDocxOpen` + `FuzzInMemoryDocx`, run via `-fuzz` flag |
| Benchmarks | 4 | Parser + full pipeline at small/large scale |

The end-to-end harness builds synthetic `.docx` files in memory, runs them
through `Convert`, extracts text with `pdftotext`, asserts both content
substrings and page geometry (via `pdfinfo`), and saves PNG snapshots for
visual review (rendered with `pdftoppm`).

```bash
# All deterministic tests (~17 s with the comprehensive case)
go test ./...

# All tests including golden image diff
GOLDEN=1 go test ./...

# Stress
go test ./... -race           # race-clean
go test ./internal/verify/... -bench=. -benchtime=2s

# Coverage (~78 % total, ~76 % in the verify package — exercises body
# render via the full pipeline)
go test -coverpkg=./... -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

The verify suite has caught real bugs other tests missed — `w:br` page
breaks not advancing pages, JPEG images failing the PNG re-encode path,
footnotes being enqueued twice inside table cells, list markers
overlapping their text when numbering.xml omitted indent.

---

## Performance

Apple M5, `go test -bench=.`:

| Benchmark | Time per op |
|---|---|
| Parse 2-paragraph docx | **21 µs** |
| Parse 500-paragraph docx | **468 µs** |
| Convert 2-paragraph → PDF | **14 ms** (font load dominates) |
| Convert 500-paragraph → PDF | **18.5 ms** |

The 500-paragraph render is only ~4 ms slower than the small one — the line
breaker and table layout scale linearly with cheap constants.

---

## Status & non-goals

docx2pdf-go aims to be **good enough for content-driven documents**: reports,
contracts, generated paperwork, internal tooling. It is **not** trying to
become a pixel-perfect Word replacement.

If you need complex DTP, real shape layout, SmartArt diagrams, full bidi
shaping, or embedded-font support — use LibreOffice as a backend. This
library exists for everyone who would rather not ship a 500 MB office
suite next to their Go service.

---

## Contributing

Issues and PRs welcome. Highest-impact missing features (in roughly that
order):

1. Text wrap around floating images / frames — needs per-line shape
   exclusion in the layout pass (`wp:anchor` with wrap geometry, `w:framePr`
   currently positions but doesn't wrap)
2. Full UAX#9 bidi for mixed-direction lines (Latin embedded in Arabic)
3. Arabic letter shaping (initial / medial / final connected forms)
4. SmartArt rendering
5. Embedded fonts (`w:embedRegular`) loaded from the package

---

## Acknowledgements

Heavily indebted to **[plutext/docx4j](https://github.com/plutext/docx4j)**
for the OOXML knowledge it has codified over more than a decade — the
parser layout, style-resolution model, and many of the edge cases
docx2pdf-go handles were figured out by reading its source first.

PDF rendering is provided by **[signintech/gopdf](https://github.com/signintech/gopdf)**.

---

## License

MIT.
