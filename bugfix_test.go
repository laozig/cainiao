package main

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestParseResultMalformedSuccessDoesNotPanic(t *testing.T) {
	got := parseResult(map[string]interface{}{
		"ret":  []interface{}{"SUCCESS::调用成功"},
		"data": "unexpected",
	})
	if got != nil {
		t.Fatalf("expected nil for malformed upstream payload, got %#v", got)
	}
}

func TestParseXLSXTrackingNumbers(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mustWriteZip(t, zw, "xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>YT1234567890</t></si>
</sst>`)
	mustWriteZip(t, zw, "xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row>
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="inlineStr"><is><t>SF9876543210</t></is></c>
    </row>
  </sheetData>
</worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := parseXLSXTrackingNumbers(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"YT1234567890": true, "SF9876543210": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d tracking numbers, got %d: %#v", len(want), len(got), got)
	}
	for _, no := range got {
		if !want[no] {
			t.Fatalf("unexpected tracking number %q in %#v", no, got)
		}
	}
}

func TestIsAllowedLocalOrigin(t *testing.T) {
	allowed := []string{
		"http://localhost:3456",
		"http://127.0.0.1:3456",
		"http://[::1]:3456",
	}
	for _, origin := range allowed {
		if !isAllowedLocalOrigin(origin) {
			t.Fatalf("expected origin to be allowed: %s", origin)
		}
	}

	blocked := []string{
		"http://localhost.evil.com",
		"https://example.com",
		"file://localhost",
		"not a url",
	}
	for _, origin := range blocked {
		if isAllowedLocalOrigin(origin) {
			t.Fatalf("expected origin to be blocked: %s", origin)
		}
	}
}

func mustWriteZip(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
