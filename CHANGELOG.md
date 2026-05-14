# Changelog

All notable changes to docx2pdf-go are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- Split `internal/render/pdf.go` (2699 lines, single file) into one file per
  concern: `pdf.go`, `page.go`, `paragraph.go`, `text.go`, `table.go`,
  `image.go`, `fonts.go`, `fields.go`, `util.go`. No behavior change; the
  package boundary is unchanged.
- README feature table now reflects implemented behavior: footnotes / endnotes,
  nested tables, cell shading + per-edge borders, multi-column layout, and
  hidden text (`w:vanish`) are all marked supported.

### Added
- Direct unit tests for `internal/render` and `internal/convert` covering
  field-code parsing, page-number formatting, hex color parsing, list-marker
  generation, line-height rules, CJK detection, context cancellation, and
  parse-error paths. These tests run without a TTF dependency.
- GitHub Actions CI: `gofmt`, `go vet`, `go build`, `go test -race`, plus an
  informational coverage summary.

## [0.1.0] - initial public state

### Added
- Pure-Go `.docx` → PDF conversion via `signintech/gopdf` (no CGO, no JVM).
- Library entry points: `Convert`, `ConvertReader`, `Open` + `Render`, plus
  `Context`-aware variants for cancellation.
- CLI (`cmd/docx2pdf`) with stdin/stdout streaming, batch processing,
  parallel workers, `-keep-going`, `-lenient`, and verbose tracing.
- Renderer support for paragraphs (alignment, indent, line spacing, tabs,
  drop-caps), run formatting (bold/italic/underline/strike/color/size,
  highlight, shading, super/subscript, position, character scale, theme
  color/font), lists (decimal/bullet/letter/roman, multi-level, custom
  start, picture bullets), tables (column widths, `gridSpan`, `vMerge`,
  header-row repeat across pages, per-edge borders, cell shading, nested
  tables), inline + best-effort anchored images (PNG/JPEG/GIF, with crop),
  multi-section layout with per-section page size and headers/footers
  (first / default / even variants), fields (`PAGE`, `NUMPAGES`, `DATE`,
  `TIME`, `FILENAME`, `AUTHOR`, `TITLE`, `SUBJECT`, `SEQ`, `REF`,
  `HYPERLINK`), bookmark/cross-reference, footnotes and endnotes, CJK
  fallback fonts and per-character line breaking, multi-column layout,
  hidden text (`w:vanish`), mirror margins, page borders and background,
  page numbering and line numbering.
- End-to-end verification harness with 57+ synthetic cases, real-world
  docx4j corpus tests, crash-resistance tests, fuzz targets, golden image
  diffs, and benchmarks.
- Dockerfile producing a ~90 MB image with Noto Sans + Noto CJK preinstalled.
