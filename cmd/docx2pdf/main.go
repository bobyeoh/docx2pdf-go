// docx2pdf — a pure-Go .docx → PDF command-line tool.
//
// Usage (single file):
//
//	docx2pdf -in input.docx -out output.pdf -font /path/to/regular.ttf
//
// Usage (batch — when -in is a directory):
//
//	docx2pdf -in indir -out outdir -font Regular.ttf [-recursive] [-keep-going]
//
// Usage (stream — read docx from stdin, write PDF to stdout):
//
//	docx2pdf -in - -out - -font Regular.ttf < in.docx > out.pdf
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bobyeoh/docx2pdf-go/internal/convert"
)

func main() {
	in := flag.String("in", "", "input .docx path OR directory OR '-' for stdin (required)")
	out := flag.String("out", "", "output .pdf path OR directory OR '-' for stdout (required)")
	font := flag.String("font", "", "path to regular TTF font (required)")
	fontBold := flag.String("font-bold", "", "path to bold TTF font (optional)")
	fontItalic := flag.String("font-italic", "", "path to italic TTF font (optional)")
	fontFallback := flag.String("font-fallback", "", "path to fallback TTF used for CJK glyphs (optional)")
	size := flag.Float64("size", 11, "default font size in points")
	pageNumbers := flag.Bool("page-numbers", false, "draw 'X / N' page numbers at the bottom of every page")
	recursive := flag.Bool("recursive", false, "in batch mode, descend into subdirectories")
	keepGoing := flag.Bool("keep-going", false, "in batch mode, continue past per-file errors (exit non-zero if any failed)")
	workers := flag.Int("workers", 1, "in batch mode, parallel worker count (default 1)")
	lenient := flag.Bool("lenient", false, "skip broken paragraphs/tables and log instead of failing")
	author := flag.String("author", "", "override AUTHOR field / PDF Info Author")
	verbose := flag.Bool("v", false, "verbose logging")
	flag.Parse()

	if *in == "" || *out == "" || *font == "" {
		flag.Usage()
		os.Exit(2)
	}

	opts := convert.Options{
		FontRegular:     *font,
		FontBold:        *fontBold,
		FontItalic:      *fontItalic,
		FontFallback:    *fontFallback,
		DefaultFontSize: *size,
		PageNumbers:     *pageNumbers,
		Verbose:         *verbose,
		Lenient:         *lenient,
		Author:          *author,
	}

	// Streaming mode: -in - and/or -out -.
	if *in == "-" || *out == "-" {
		if err := runStream(*in, *out, opts); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	st, err := os.Stat(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if st.IsDir() {
		failed := runBatch(*in, *out, *recursive, *keepGoing, *workers, opts)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}

	if err := convert.Convert(*in, *out, opts); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *verbose {
		fmt.Println("wrote", *out)
	}
}

// runStream handles -in - / -out -. When in is "-" we read stdin into memory
// (docx requires random access via io.ReaderAt). When out is "-" we write
// to stdout.
func runStream(in, out string, opts convert.Options) error {
	var inReader io.ReaderAt
	var size int64
	if in == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		inReader = bytes.NewReader(buf)
		size = int64(len(buf))
	} else {
		f, err := os.Open(in)
		if err != nil {
			return fmt.Errorf("open %s: %w", in, err)
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return err
		}
		inReader = f
		size = stat.Size()
	}

	var outWriter io.Writer
	if out == "-" {
		outWriter = os.Stdout
	} else {
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("create %s: %w", out, err)
		}
		defer f.Close()
		outWriter = f
	}

	return convert.ConvertReader(inReader, size, outWriter, opts)
}

// runBatch walks inDir for .docx files and converts each to a sibling .pdf
// in outDir, preserving the relative path. With workers > 1 the conversion
// runs in parallel — each Convert call is independent / thread-safe.
func runBatch(inDir, outDir string, recursive, keepGoing bool, workers int, opts convert.Options) int {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error: create outdir:", err)
		return 1
	}
	files, err := findDocx(inDir, recursive)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no .docx files found under", inDir)
		return 0
	}
	if workers < 1 {
		workers = 1
	}
	if workers > runtime.NumCPU()*2 {
		workers = runtime.NumCPU() * 2
	}

	type job struct{ src, rel, dst string }
	jobs := make(chan job, len(files))
	var failedMu sync.Mutex
	failed := 0
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for j := range jobs {
			start := time.Now()
			if err := os.MkdirAll(filepath.Dir(j.dst), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", j.src, err)
				failedMu.Lock()
				failed++
				failedMu.Unlock()
				if !keepGoing {
					// Drain remaining and stop.
					for range jobs {
					}
					return
				}
				continue
			}
			if err := convert.Convert(j.src, j.dst, opts); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", j.src, err)
				failedMu.Lock()
				failed++
				failedMu.Unlock()
				if !keepGoing {
					for range jobs {
					}
					return
				}
				continue
			}
			if opts.Verbose {
				fmt.Printf("  %s → %s (%s)\n", j.rel, withExt(j.rel, ".pdf"), time.Since(start).Round(time.Millisecond))
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	for _, src := range files {
		rel, err := filepath.Rel(inDir, src)
		if err != nil {
			rel = filepath.Base(src)
		}
		jobs <- job{src: src, rel: rel, dst: filepath.Join(outDir, withExt(rel, ".pdf"))}
	}
	close(jobs)
	wg.Wait()

	if opts.Verbose {
		fmt.Printf("done: %d ok, %d failed (workers=%d)\n", len(files)-failed, failed, workers)
	}
	return failed
}

// findDocx returns sorted .docx file paths under root. When recursive=false,
// only the top-level directory is scanned (no subdirectory descent).
func findDocx(root string, recursive bool) ([]string, error) {
	var out []string
	walk := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !recursive && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".docx") &&
			!strings.HasPrefix(filepath.Base(path), "~$") {
			out = append(out, path)
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	return out, nil
}

func withExt(p, newExt string) string {
	return strings.TrimSuffix(p, filepath.Ext(p)) + newExt
}
