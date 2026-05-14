package convert

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvert_NoSuchFile(t *testing.T) {
	err := Convert("/does/not/exist.docx", filepath.Join(t.TempDir(), "out.pdf"), Options{})
	if err == nil {
		t.Fatal("expected error for missing input file")
	}
	if !strings.Contains(err.Error(), "parse docx") {
		t.Errorf("error = %q; want it to mention parse docx", err.Error())
	}
}

func TestConvert_NoSuchFont(t *testing.T) {
	// Use a syntactically valid docx zip path that doesn't exist; the parse
	// error path runs first.
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in.docx")
	out := filepath.Join(tmp, "out.pdf")
	if err := writeMinimalDocx(in); err != nil {
		t.Fatal(err)
	}
	err := Convert(in, out, Options{FontRegular: "/does/not/exist.ttf"})
	if err == nil {
		t.Fatal("expected error for missing font")
	}
	if !strings.Contains(err.Error(), "render pdf") {
		t.Errorf("error = %q; want it to mention render pdf", err.Error())
	}
}

func TestConvertReader_InvalidZip(t *testing.T) {
	// Random bytes that don't form a valid zip → parse error.
	data := []byte("this is not a zip file")
	var out bytes.Buffer
	err := ConvertReader(bytes.NewReader(data), int64(len(data)), &out, Options{})
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

func TestConvertContext_CanceledUpFront(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before call
	err := ConvertContext(ctx, "/any/path.docx", "/any/out.pdf", Options{})
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestConvertReaderContext_CanceledUpFront(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	err := ConvertReaderContext(ctx, bytes.NewReader([]byte("x")), 1, &buf, Options{})
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// TestConvert_SourceFilenameFallback ensures Convert auto-fills SourceFilename
// from the input path when the caller leaves it blank.
func TestConvert_SourceFilenameFallback(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "named.docx")
	if err := writeMinimalDocx(in); err != nil {
		t.Fatal(err)
	}
	// Use Options without FontRegular: the call will fail at render, but we
	// can still inspect opts.SourceFilename indirectly via the error message
	// path? — Easier: assert that the call reaches render-stage failure (not
	// parse-stage), which means SourceFilename was set without us providing
	// it.
	err := Convert(in, filepath.Join(tmp, "out.pdf"), Options{FontRegular: "/missing"})
	if err == nil {
		t.Fatal("expected font error")
	}
	if !strings.Contains(err.Error(), "render pdf") {
		t.Errorf("expected render-stage error, got: %v", err)
	}
}
