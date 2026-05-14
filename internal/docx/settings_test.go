package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestParseSettings_DefaultTabStop(t *testing.T) {
	// Build a minimal docx in memory with the test settings.xml.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	w, err := zw.Create("word/document.xml")
	must(err)
	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>hi</w:t></w:r></w:p></w:body></w:document>`))
	must(err)
	w, err = zw.Create("word/settings.xml")
	must(err)
	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:defaultTabStop w:val="1440"/><w:evenAndOddHeaders/><w:displayBackgroundShape/></w:settings>`))
	must(err)
	must(zw.Close())

	doc, err := Parse(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Settings.DefaultTabStopTwips != 1440 {
		t.Errorf("DefaultTabStopTwips = %d, want 1440", doc.Settings.DefaultTabStopTwips)
	}
	if !doc.Settings.EvenAndOddHeaders {
		t.Error("EvenAndOddHeaders = false, want true")
	}
	if !doc.Settings.DisplayBackgroundShape {
		t.Error("DisplayBackgroundShape = false, want true")
	}
}
