package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestGetFreshProxyRepullsDuplicate(t *testing.T) {
	var pulls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&pulls, 1)
		if n < 3 {
			fmt.Fprint(w, "127.0.0.1:8001")
			return
		}
		fmt.Fprint(w, "127.0.0.1:8002")
	}))
	defer server.Close()

	got, changed, err := getFreshProxy(server.URL, "http://127.0.0.1:8001")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:8002" || !changed {
		t.Fatalf("expected a changed proxy, got %q changed=%v", got, changed)
	}
	if pulls != 3 {
		t.Fatalf("expected 3 proxy pulls, got %d", pulls)
	}
}

func TestGetFreshProxyReportsRepeatedProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "127.0.0.1:8001")
	}))
	defer server.Close()

	got, changed, err := getFreshProxy(server.URL, "http://127.0.0.1:8001")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:8001" || changed {
		t.Fatalf("expected repeated proxy, got %q changed=%v", got, changed)
	}
}

func TestQueryWithRetryRequiresProxy(t *testing.T) {
	_, err := queryWithRetryLimit("TEST", "", "", 1000, 3, time.Minute, nil)
	if err == nil || err.Error() != "代理API不能为空" {
		t.Fatalf("expected proxy requirement, got %v", err)
	}
}

func TestQueryWithRetryLimitStopsBeforeFirstAttempt(t *testing.T) {
	started := time.Now()
	_, err := queryWithRetryLimit("TEST", "", "http://127.0.0.1:1", 1000, 3, time.Nanosecond, nil)
	if err == nil || !strings.Contains(err.Error(), "单项处理超时") {
		t.Fatalf("expected item timeout, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout check took too long: %s", elapsed)
	}
}

func TestQueryWithRetryLimitHonorsCancellation(t *testing.T) {
	_, err := queryWithRetryLimit("TEST", "", "http://127.0.0.1:1", 1000, 3, time.Minute, func() bool { return true })
	if err == nil || err.Error() != "已取消" {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestMakeHTTPClientUsesThreeSecondTimeout(t *testing.T) {
	client := makeHTTPClient("http://127.0.0.1:8001", 30000)
	if client.Timeout != 3*time.Second {
		t.Fatalf("expected 3s client timeout, got %s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || !transport.DisableKeepAlives {
		t.Fatalf("expected isolated proxy transport with keep-alive disabled")
	}
}

func TestNormalizeImportTags(t *testing.T) {
	got := normalizeImportTags("客户A， 加急;客户A； 七月 ")
	want := "客户A,加急,七月"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
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
