# Changelog

All notable changes to docx2pdf-go are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added (later round)
- **`w:fldSimple` (compact field encoding) is now recognized.** Word uses
  this form a lot in headers/footers — `<w:fldSimple w:instr="PAGE">…</w:fldSimple>`.
  Previously dropped, which made e.g. PAGE/NUMPAGES in headers render as
  their stale cached value (or vanish). It now expands to the same
  begin/instr/sep/.../end marker stream the complex `fldChar` form
  produces, so the existing field state machine handles both. Regression
  case #120.
- **`m:oMath` / `m:oMathPara` math equations** extract their visible text
  and emit it as an italic run (inline) or italic centered paragraph
  (display). Structural typesetting (fractions, sub/superscripts, integral
  signs) is lost, but the content survives instead of being silently
  dropped — the right trade-off until a real math renderer is on the
  table. Regression cases #118 (inline) and #119 (display).
- **`w:sdt` (content controls / structured document tags) are transparent.**
  Both inline (within a paragraph) and block-level SDTs (in body, table
  cells, headers/footers, notes) are recognized: `w:sdtPr` / `w:sdtEndPr`
  are ignored and `w:sdtContent`'s children join the surrounding flow.
  Nested SDTs flatten. Common Word-template form fields and placeholder
  text now survive the parser. Regression cases #115 (inline), #116
  (block), #117 (block in cell).
- **`w:framePr`-positioned paragraphs render at their anchored location**
  rather than falling through to inline flow. Resolves `hAnchor` / `vAnchor`
  (margin / page / text) plus `xAlign` / `yAlign` / `w:x` / `w:y` to absolute
  page coordinates; the document cursor stays put so frames are truly
  out-of-flow. Surrounding body text does NOT yet wrap around — that needs
  per-line shape exclusion in the layout pass (separate, larger piece of
  work; docx4j's FOP path has the same limitation). Regression case #114.
- **Legacy VML images render.** `w:pict` / `v:imagedata` (older Word output,
  Excel/Outlook pasted images) now flows through the same image pipeline as
  `w:drawing`. Sizes parsed from CSS-style `style="width:1in;height:0.5in"`
  with pt/in/cm/mm/px/pc/no-unit support. Regression case #113.

### Added (this round)
- **`word/settings.xml` is now parsed.** Three doc-level knobs flow into
  rendering:
  - `w:defaultTabStop` drives the implicit tab grid (previously hard-coded
    to 36pt).
  - `w:evenAndOddHeaders` is now the master switch for even-page H/F — a
    section's even references are honored only when the setting is on.
  - `w:displayBackgroundShape` gates `w:background` color drawing, matching
    Word's behavior (default off).
- **`w:moveTo` / `w:moveFrom`** flow through the wrapper handler the same
  way `w:ins` / `w:del` do (moveTo keeps content, moveFrom drops it).
- **`w:object` (OLE / embedded content)** emits an `[Embedded object]`
  marker run rather than silently disappearing.
- Regression cases 110–112 covering the above.

### Fixed
- **Image sizing was 33% too large.** Drawing extent EMU values were divided
  by 9525 (pixels-per-EMU @ 96 dpi) instead of 12700 (EMU-per-point). All
  `wp:extent`-sized images now render at the correct physical size.
- **Footnotes inside table cells rendered twice.** `measureCell`'s dry-layout
  pass shared the same `pendingFootnotes` queue as the real draw, so each
  note ref was enqueued during both. Now save/restore the queue around
  measurement.
- **Hanging indent (`w:ind w:hanging`) is now honored.** Negative
  `IndentFirstLinePt` outdents the first physical line; subsequent wraps
  occupy the body width as expected.
- **`w:w` (character scale) and `w:spacing` (letter spacing) now visibly
  apply.** The previous letter-spacing path advanced the cursor *after* the
  run, leaving glyphs tight; both are now wired through gopdf's
  `SetCharSpacing` so measurement and draw agree.
- **Theme-major fonts have a routing target.** New `Options.FontHeading`
  (CLI `-font-heading`) registers a TTF for runs tagged with
  `w:asciiTheme="majorHAnsi"` etc. Without it, theme-major runs still fall
  through to FontRegular (unchanged), so heading-vs-body font distinctions
  finally have a way to be expressed.
- **Cross-paragraph bookmarks now capture all spanned text.** The bookmark
  active-name state used to be a per-paragraph local map, so `bookmarkStart`
  in one paragraph and `bookmarkEnd` in another would lose every paragraph
  in between. State now lives on the document parse context.
- **Table style merge no longer drops `Vanish` / `PositionPt` /
  `CharacterScale` / `ThemeFontRole` / `StyleID`.** The renderer used a
  partial duplicate of the parser's `mergeRunProps`; the canonical
  implementation is now exported as `docx.MergeRunProps` and used in both
  places.
- **`w:tr/w:cantSplit` rows are now kept intact across page breaks.**
  Previously the flag was parsed but never honored — the row could be split
  in the middle by `ensureRoom`.
- **`w:smartTag` content no longer gets dropped.** The wrapper used to fall
  into the default `dec.Skip()` branch; now it's traversed transparently.
  Same for `w:customXml`.
- **`w:ins` / `w:del` recurse into nested wrappers.** Tracked-change
  insertions wrapping a `w:hyperlink` or `w:smartTag` previously lost the
  contained text; nested wrappers now route through the same handler. A
  `w:del` inside a `w:ins` correctly drops (Word's "accept all" semantics).
- **Footnote-area errors are logged when verbose / Lenient.**
  `drawFootnotesAtBottom` used to swallow `drawParagraph` errors silently;
  it now surfaces them via `Options.Logger` (or stdout in Verbose mode).

### Changed
- Split `internal/render/pdf.go` (2699 lines, single file) into one file per
  concern: `pdf.go`, `page.go`, `paragraph.go`, `text.go`, `table.go`,
  `image.go`, `fonts.go`, `fields.go`, `util.go`. No behavior change; the
  package boundary is unchanged.
- README feature table now reflects implemented behavior: footnotes / endnotes,
  nested tables, cell shading + per-edge borders, multi-column layout, and
  hidden text (`w:vanish`) are all marked supported.
- `docx.MergeRunProps` is exported (was the unexported `mergeRunProps`).

### Added
- `Options.FontHeading` / CLI `-font-heading` for theme-major runs.
- Direct unit tests for `internal/render` and `internal/convert` covering
  field-code parsing, page-number formatting, hex color parsing, list-marker
  generation, line-height rules, CJK detection, context cancellation, and
  parse-error paths. These tests run without a TTF dependency.
- Regression cases in the verify harness for: footnote-in-table-cell
  (#106), hanging indent (#107), smartTag transparency (#108), and
  tracked-insertion of a hyperlink (#109).
- GitHub Actions CI: `gofmt`, `go vet`, `go build`, `go test -race`, plus an
  informational coverage summary.

### Removed
- Dead code: `boolFloat`, `fieldCodeFromInstr`, `lookupFieldValue` (no-arg
  wrapper), `nextTabAfter` (no-align wrapper), `findBlipEmbed`,
  `LineNumbering.Distance`, `renderer.sectionMarL/R`, and a duplicate
  `croppedCache` map write.

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
