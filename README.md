# docx2pdf-go

[![test](https://github.com/bobyeoh/docx2pdf-go/actions/workflows/test.yml/badge.svg)](https://github.com/bobyeoh/docx2pdf-go/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/bobyeoh/docx2pdf-go.svg)](https://pkg.go.dev/github.com/bobyeoh/docx2pdf-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/bobyeoh/docx2pdf-go)](https://goreportcard.com/report/github.com/bobyeoh/docx2pdf-go)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

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
    FontFallback: "/usr/share/fonts/wqy/wqy-zenhei.ttc", // CJK (TrueType)
    PageNumbers:  true,
})
```

> ℹ️ **CJK fallback note**: pick a **TrueType** CJK font (e.g. WenQuanYi
> Zen Hei, Source Han Sans's TTF distribution, SimSun). gopdf can't
> render CFF/PostScript outlines, so Noto Sans CJK's `.ttc` is rejected
> with a clear error. See the [FAQ](#faq).

---

## Why docx2pdf-go

Pure-Go `.docx` → PDF is harder than it looks. Real-world options today:

| Approach | Trade-off |
|---|---|
| Shell out to **LibreOffice** | 500 MB+ container, slow fork-per-conversion, headless quirks |
| **unioffice** | Commercial license required for closed-source apps |
| **Pandoc + LaTeX** | Heavy toolchain, fragile layout for complex Office docs |
| Hand-roll it | Months of XML wrangling, font metrics, table layout… |

**docx2pdf-go** sits in the gap: a focused, MIT-licensed, pure-Go library
that covers the 90 % of WordprocessingML that real documents actually
use, without external runtimes.

### What you get

- **~3.5 MB static binary.** Runs in `scratch` / `distroless` / Alpine
  with no fonts installed — a 150 KB Latin face (Go fonts, MIT) is
  embedded as a last-resort fallback so the binary always has
  something to draw with.
- **Trivial to containerize** — multi-stage build into `distroless`
  yields a ~5 MB image; add a TTF/TTC for CJK if needed. Recipe in
  the Docker section below.
- **Streaming `io.Reader` → `io.Writer` API** for HTTP handlers and
  in-memory pipelines.
- **CLI for batch processing** — point it at a directory tree, get a
  mirrored tree of PDFs.
- **CJK supported via fallback font** with per-character line-break
  opportunities, so Chinese/Japanese/Korean paragraphs wrap mid-text
  even without whitespace. Latin and CJK glyph shaping work; Arabic
  letter shaping (initial/medial/final) is not yet implemented.

---

## Quick start

### As a library

```bash
go get github.com/bobyeoh/docx2pdf-go
```

**1. File paths — the simplest case.** Empty Options auto-detects a
system font; pass `FontRegular` for reproducible output.

```go
import docx2pdf "github.com/bobyeoh/docx2pdf-go"

func writeReport() error {
    return docx2pdf.Convert("in.docx", "out.pdf", docx2pdf.Options{})
}
```

**2. Streaming — perfect for HTTP handlers.**

```go
import (
    "bytes"
    "io"
    "net/http"

    docx2pdf "github.com/bobyeoh/docx2pdf-go"
)

func handle(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    w.Header().Set("Content-Type", "application/pdf")
    _ = docx2pdf.ConvertReader(
        bytes.NewReader(body), int64(len(body)), w,
        docx2pdf.Options{},
    )
}
```

**3. Parse → inspect / modify → render.**

```go
doc, err := docx2pdf.Open("in.docx")
if err != nil {
    return err
}
for _, b := range doc.Body {
    if p, ok := b.(docx2pdf.Paragraph); ok && len(p.Runs) > 0 {
        // walk the AST: redact, translate, reformat, ...
    }
}
return docx2pdf.Render(doc, "out.pdf", docx2pdf.Options{})
```

### As a CLI

#### Install

```bash
# From source (Go 1.26+). Drops a single static binary into $GOBIN /
# $GOPATH/bin — make sure that's on your $PATH.
go install github.com/bobyeoh/docx2pdf-go/cmd/docx2pdf@latest

# Or build locally from a checkout — useful for cross-compiling.
git clone https://github.com/bobyeoh/docx2pdf-go.git
cd docx2pdf-go
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o docx2pdf ./cmd/docx2pdf
```

The binary is ~3.5 MB, statically linked, and embeds a Latin fallback
font so it works on any machine — no system fonts required.

#### Convert a single file

```bash
# System font auto-detected (Arial / Helvetica / DejaVu / Liberation / Noto).
docx2pdf -in report.docx -out report.pdf

# Explicit font (recommended for cross-machine reproducibility).
docx2pdf -in report.docx -out report.pdf \
    -font /usr/share/fonts/noto/NotoSans-Regular.ttf \
    -font-fallback /usr/share/fonts/wqy/wqy-zenhei.ttc

# Add page numbers ("3 / 12" at the bottom of every page).
docx2pdf -in report.docx -out report.pdf -page-numbers
```

#### Pipe via stdin / stdout

`-in -` reads the docx from stdin; `-out -` writes the PDF to stdout.
Useful for HTTP gateways, shell pipelines, and serverless functions
that don't want temp files:

```bash
# stdin → file
cat report.docx | docx2pdf -in - -out report.pdf

# file → stdout (pipe straight to a viewer or another tool)
docx2pdf -in report.docx -out - | pdftotext - -

# stdin → stdout (full pipeline, no disk hit)
curl -s https://example.com/report.docx | docx2pdf -in - -out - > report.pdf
```

#### Batch convert a directory

```bash
# Recurse the input tree, mirror its structure into the output tree.
# -keep-going won't abort the whole run on one bad file (exit code
# is still non-zero so CI sees the failure).
docx2pdf -in ./input -out ./output -recursive -keep-going

# 8 parallel workers — handy for hundreds of files. The renderer is
# CPU-bound (XML parse + PDF layout), so workers ≈ NumCPU is usually
# right.
docx2pdf -in ./input -out ./output -recursive -workers 8
```

#### All flags

| Flag | Meaning |
|---|---|
| `-in PATH \| -` | Input `.docx`, directory, or `-` for stdin (required) |
| `-out PATH \| -` | Output `.pdf`, directory, or `-` for stdout (required) |
| `-font PATH` | Regular TTF; empty → auto-detect a system font |
| `-font-bold PATH` | Real bold face (otherwise faux-bolded by double-stroke) |
| `-font-italic PATH` | Real italic face |
| `-font-heading PATH` | Heading-role font (e.g. Cambria) for theme-major runs |
| `-font-fallback PATH` | CJK / symbol fallback (TrueType only) |
| `-size N` | Default font size in points (default 11) |
| `-page-numbers` | Draw "X / N" at the bottom of every page |
| `-author NAME` | Override `AUTHOR` field + PDF metadata `Author` |
| `-recursive` | In batch mode, descend into subdirectories |
| `-workers N` | Parallel batch workers (default 1) |
| `-keep-going` | Don't abort the batch on per-file errors |
| `-lenient` | Skip broken paragraphs/tables, log and continue |
| `-v` | Verbose logging |

Run `docx2pdf -help` to see them in your terminal with current defaults.

#### Environment variables

Two env vars are consulted when the matching flag is not given —
convenient in containers where fonts are baked into the image:

| Variable | Equivalent flag |
|---|---|
| `DOCX2PDF_FONT` | `-font` |
| `DOCX2PDF_FONT_CJK` | `-font-fallback` |

### Docker

No pre-built image is published — pick the recipe that matches your
needs.

#### Minimal (~5 MB, Latin only)

The binary embeds a small Latin fallback font, so a `distroless`
image with no system fonts produces a valid PDF for Latin documents:

```dockerfile
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
docker build -t docx2pdf .
docker run --rm -v "$PWD":/work docx2pdf \
    -in /work/in.docx -out /work/out.pdf  # no -font needed
```

#### Batteries-included (~70 MB, Latin + CJK)

The repo ships a [`Dockerfile`](Dockerfile) that bakes Noto Sans
(Latin) plus WenQuanYi Zen Hei (CJK fallback) and sets
`DOCX2PDF_FONT` / `DOCX2PDF_FONT_CJK` so it works on any docx
without flags:

```bash
docker build -t docx2pdf .
docker run --rm -v "$PWD":/work docx2pdf \
    -in /work/report.docx -out /work/report.pdf -page-numbers
```

> ℹ️ Avoid Noto Sans CJK — its `.ttc` uses CFF/PostScript outlines
> which gopdf can't render. Use a TrueType CJK font (WQY Zen Hei,
> Source Han Sans TTF distribution, SimSun). See the FAQ.

#### Mounting a font at run time

If you don't want to bake fonts into the image, mount them at run
time instead:

```bash
docker run --rm -v "$PWD":/work \
    -e DOCX2PDF_FONT_CJK=/work/wqy-zenhei.ttc \
    docx2pdf -in /work/in.docx -out /work/out.pdf
```

---

## What it actually renders

> **TL;DR**: solid for reports / contracts / generated paperwork in
> Latin + CJK. Loses fidelity on floating-image wrap, math typesetting,
> and SmartArt. See the ⚠️ / ❌ rows for the precise list.

### Text & inline formatting

| Feature | Status |
|---|---|
| Bold / italic / underline / strikethrough / color / size / sub-super-script | ✅ |
| Caps / small-caps / vertical position (`w:position`) / character scale (`w:w`) | ✅ |
| Letter spacing (`w:spacing` in rPr) | ✅ |
| Highlight + shading background fills | ✅ |
| Hidden text (`w:vanish`) — suppressed from output | ✅ |
| Theme colors / theme fonts (Heading / Body roles) | ✅ |
| Hyperlinks (external URL + internal anchors) — real PDF annotations | ✅ |
| Soft-hyphen / non-breaking hyphen / `w:sym` symbol references | ✅ |

### Paragraph & page layout

| Feature | Status |
|---|---|
| Alignment: left / center / right / justify | ✅ |
| Indent: left / first-line / hanging | ✅ |
| Line spacing (single / 1.5x / double / exact / atLeast) | ✅ |
| Tab stops + tab leaders (dot / hyphen / underscore) | ✅ |
| Drop caps (`w:framePr w:dropCap`) | ✅ |
| Multi-column layout (`w:cols`) | ✅ |
| Multi-section docs (per-section page size / orientation / margins) | ✅ |
| Headers & footers (default / first / even) | ✅ |
| Mirror margins, gutter, page borders, line numbers | ✅ |
| Page background color (`w:background` gated by `displayBackgroundShape`) | ✅ |
| Explicit page breaks (`w:br type="page"` / `w:pageBreakBefore`) | ✅ |

### Lists & tables

| Feature | Status |
|---|---|
| Numbered lists: decimal / lower-upper letter / lower-upper roman | ✅ |
| Bullet lists with custom text or picture bullets | ✅ |
| Multi-level lists + legal numbering (`w:isLgl`) + custom `start` | ✅ |
| Tables: column widths, `gridSpan` merge, `vMerge` merge | ✅ |
| Multi-page tables with header-row repeat | ✅ |
| Nested tables (table inside a cell) | ✅ |
| Cell shading + per-edge borders (single / double / dashed / dotted) | ✅ |
| Row `cantSplit` (keep row intact across page break) | ✅ |
| Table styles (`w:tblStyle` + `w:tblLook` conditional emphasis) | ✅ |

### Images & graphics

| Feature | Status |
|---|---|
| Inline images: PNG / JPEG / GIF | ✅ |
| Image cropping (`a:srcRect`) and explicit extent | ✅ |
| Legacy VML images (`w:pict` / `v:imagedata`) — older Word, Excel/Outlook pastes | ✅ |
| Anchored images (`wp:anchor`) — rendered as inline best-effort | ⚠️ |
| Text wrap around floating images | ❌ |
| SmartArt diagrams | ❌ |
| Charts (`c:chart`) — title / labels extracted as `[Chart: …]` text | ⚠️ |

### Document structure & metadata

| Feature | Status |
|---|---|
| Paragraph styles with `basedOn` chains + `docDefaults` | ✅ |
| PDF outline / sidebar bookmarks from `Heading1..9` + `Title` styles | ✅ |
| Fields: `PAGE` / `NUMPAGES` / `DATE` / `AUTHOR` / `SEQ` / `REF` / `HYPERLINK` | ✅ |
| Both field encodings: `fldChar` complex + `fldSimple` compact | ✅ |
| Footnotes & endnotes (refs as `[N]`, bodies at page bottom / trailer) | ✅ |
| Comments (`comments.xml`) — surfaced as a trailing "Comments" section | ✅ |
| Tracked changes (`w:ins` / `w:del` / `w:moveFrom` / `w:moveTo`) — accept-all | ✅ |
| Content controls (`w:sdt`) — block + inline, transparent wrapper | ✅ |
| `mc:AlternateContent` — Choice over Fallback | ✅ |
| Doc properties (`docProps/core.xml` + `app.xml`) → PDF /Info | ✅ |
| Settings (`settings.xml`) — defaultTabStop / evenAndOddHeaders / displayBackgroundShape | ✅ |

### Advanced / partial

| Feature | Status |
|---|---|
| Math equations (`m:oMath` / `m:oMathPara`) — text extracted as italic, structure lost | ⚠️ |
| Text boxes (`wps:txbx`) — inline-extracted as italic; box geometry not preserved | ⚠️ |
| Floating frames (`w:framePr`) — anchored correctly; body text does **not** wrap around | ⚠️ |
| RTL (Hebrew / Arabic) — word order reversed, right-aligned; no UAX#9 mixed-direction | ⚠️ |
| OLE / embedded objects — emit `[Embedded object]` placeholder | ⚠️ |
| Arabic letter shaping (initial / medial / final) | ❌ |
| Form controls' interactive behavior | ❌ |
| Embedded fonts (`w:embedRegular`) loaded from package | ❌ |

If your document hinges on the "❌" rows and you need pixel-perfect
rendering, **fall back to a LibreOffice-backed service**. The "⚠️" rows
preserve content but lose some structural fidelity — check whether
that's acceptable for your use case.

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
                                sysfont.go    — system-font auto-detection
                                ttc.go        — TTC face-0 extraction
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
| Unit (parser + render) | 50+ | XML decoding, style resolution, list numbering, field codes, settings, VML, theme, borders |
| Unit (CLI) | 2 | Directory walking, extension handling |
| Public API smoke | 3 | Library is importable from outside the module |
| End-to-end | 137 | `docx → PDF → pdftotext + pdfinfo + PNG` per case |
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

Apple M-series (arm64), `go test -bench=. -benchtime=2s`:

| Benchmark | Time per op |
|---|---|
| Parse 2-paragraph docx | **22 µs** |
| Parse 500-paragraph docx | **534 µs** |
| Convert 2-paragraph → PDF | **13 ms** (font load dominates) |
| Convert 500-paragraph → PDF | **21 ms** |

The 500-paragraph render is ~8 ms slower than the small one — the line
breaker and table layout scale linearly with cheap constants, leaving
the per-doc font load as the bulk of small-doc cost.

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

## FAQ

**Does it support `.doc` (Word 97-2003 binary format)?**
No. `.docx` only. Convert legacy `.doc` to `.docx` first (LibreOffice's
`soffice --convert-to docx` does this reliably).

**Does it support `.pptx` / `.xlsx`?**
No. Word documents only. PowerPoint and Excel are separate OOXML
schemas with their own renderers (and very different layout models —
in particular slides are absolute-positioned, which is the opposite of
flow text).

**Encrypted / password-protected docx?**
No. The zip is opened with `archive/zip` which doesn't know about the
ECMA-376 Agile Encryption layer. Decrypt first with a dedicated tool.

**Will the output look identical to Word?**
Close, not pixel-identical. Fonts, hyphenation, and floating-image
wrap differ. For Latin / CJK content-driven docs the difference is
usually below "reader notices anything off". For complex layouts
(magazine-style multi-column with image wrap), it won't.

**Why doesn't Noto Sans CJK work as the fallback font?**
gopdf only renders TrueType outlines. Noto Sans CJK uses
CFF/PostScript outlines (the `.ttc` contains `OTTO`-tagged faces);
gopdf rejects them with a clear error. Use a TrueType CJK font like
WenQuanYi Zen Hei (the repo's batteries-included `Dockerfile` uses
it) or Source Han Sans's TTF distribution.

**Can the AST be modified before rendering?**
Yes — `Open` returns a `*Document` whose `Body` / `Sections` / `Styles`
fields are exported. The example in Quick start §3 walks the AST.
This is useful for redaction, translation, or template fill-in
pipelines.

**Is the public API stable?**
Pre-1.0, so the surface may shift between minor releases. The
function signatures of `Convert` / `ConvertReader` / `Open` / `Render`
are unlikely to change; the AST struct fields (Paragraph, Run, Table)
may gain fields as features land. Pin a tag in production.

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
