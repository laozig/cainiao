package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// ── Config (hardcoded defaults, all configurable via DB settings) ──

const (
	defaultAppKey   = "12574478"
	defaultTimeout  = 3
	defaultConc     = 5
	defaultMonLimit = 500
	defaultPort     = 3456
)

// getAppKey reads appKey from DB, falls back to hardcoded default
func getAppKey() string {
	return getSetting("appKey", defaultAppKey)
}

// ── Rate limiters ──────────────────────────────────
// Node: apiLimiter 120/min window, importLimiter 10/min window

var (
	apiLimiter    = rate.NewLimiter(rate.Every(time.Minute/120), 120) // 120/min sliding window
	importLimiter = rate.NewLimiter(rate.Every(time.Minute/10), 10)   // 10/min sliding window
)

// ── Handlers ───────────────────────────────────────

func handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings := getAppSettings(AppSettings{
		AppKey:       defaultAppKey,
		ProxyAPI:     "",
		Timeout:      defaultTimeout,
		Concurrency:  defaultConc,
		MonitorLimit: defaultMonLimit,
		Port:         defaultPort,
	})
	jsonMarshalResponse(w, settings)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body AppSettings
	if err := jsonDecodeBody(r, &body); err != nil {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "invalid body"})
		return
	}
	// Node: req.body?.proxyApi || '' — empty string is valid
	if body.ProxyAPI == "" {
		// keep whatever was stored, or default
	}
	if body.Timeout < 1 || body.Timeout > 30 {
		body.Timeout = defaultTimeout
	}
	if body.Concurrency < 1 || body.Concurrency > 20 {
		body.Concurrency = defaultConc
	}
	if body.MonitorLimit < 1 || body.MonitorLimit > 10000 {
		body.MonitorLimit = defaultMonLimit
	}
	if body.Port < 1 || body.Port > 65535 {
		body.Port = defaultPort
	}
	settings := updateAppSettings(body)
	jsonMarshalResponse(w, map[string]interface{}{"success": true, "settings": settings})
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MailNo   string `json:"mailNo"`
		CpCode   string `json:"cpCode"`
		ProxyAPI string `json:"proxyApi"`
		Timeout  int    `json:"timeout"`
	}
	if err := jsonDecodeBody(r, &body); err != nil {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "invalid body"})
		return
	}
	if body.MailNo == "" {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "请提供单号"})
		return
	}

	cpCode := body.CpCode
	if cpCode == "" {
		cpCode = detectCarrier(body.MailNo)
	}
	proxyAPI := getActiveProxyAPI(body.ProxyAPI)
	timeoutSec := body.Timeout
	if timeoutSec < 1 || timeoutSec > 30 {
		timeoutSec = defaultTimeout
	}

	result, err := queryWithRetry(body.MailNo, cpCode, proxyAPI, timeoutSec*1000, 5, nil)
	if err != nil {
		// Node: res.json({ success: false, error: e.message }) — 200 status
		jsonMarshalResponse(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	info := parseResult(result)
	if info != nil {
		if err := upsertShipment(*info, ""); err != nil {
			jsonMarshalResponse(w, map[string]interface{}{"success": false, "error": "保存记录失败: " + err.Error()})
			return
		}
	}
	jsonMarshalResponse(w, map[string]interface{}{"success": true, "data": info})
}

func handleImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Numbers     []string `json:"numbers"`
		CpCode      string   `json:"cpCode"`
		ProxyAPI    string   `json:"proxyApi"`
		Timeout     int      `json:"timeout"`
		Concurrency int      `json:"concurrency"`
	}
	if err := jsonDecodeBody(r, &body); err != nil {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "invalid body"})
		return
	}
	if len(body.Numbers) == 0 {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无单号"})
		return
	}

	proxyAPI := getActiveProxyAPI(body.ProxyAPI)
	timeoutSec := body.Timeout
	if timeoutSec < 1 || timeoutSec > 30 {
		timeoutSec = defaultTimeout
	}
	conc := body.Concurrency
	if conc < 1 || conc > 20 {
		conc = defaultConc
	}

	// Node: new Date().toISOString().replace(/[T:]/g, '-').slice(0, 19) → "2026-06-30T14-46-50"
	batchID := time.Now().Format("2006-01-02T15-04-05")
	runSSEBatch(body.Numbers, body.CpCode, proxyAPI, timeoutSec, conc, batchID, w, r)
}

func handleGetRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 20
	}

	statusCode := parseStatusCodeParam(q.Get("statusCode"))

	result := getRecords(page, pageSize, statusCode,
		q.Get("search"), q.Get("carrier"), q.Get("tag"),
		q.Get("sort"), q.Get("order"),
		q.Get("dateFrom"), q.Get("dateTo"))

	jsonMarshalResponse(w, result)
}

func handleExportRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100000 {
		limit = 100000
	}

	result := getRecords(1, limit, parseStatusCodeParam(q.Get("statusCode")),
		q.Get("search"), q.Get("carrier"), q.Get("tag"),
		q.Get("sort"), q.Get("order"),
		q.Get("dateFrom"), q.Get("dateTo"))

	jsonMarshalResponse(w, result)
}

func parseStatusCodeParam(scStr string) interface{} {
	if scStr == "monitoring" {
		return "monitoring"
	}
	if scStr != "" {
		if n, err := strconv.Atoi(scStr); err == nil {
			return n
		}
	}
	return nil
}

// BUG FIX: Node returns 404 when record not found, Go was returning 200
func handleGetRecord(w http.ResponseWriter, r *http.Request) {
	idStr := getPathParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	rec := getRecord(id)
	if rec == nil {
		w.WriteHeader(404)
		jsonMarshalResponse(w, map[string]string{"error": "记录不存在"})
		return
	}
	jsonMarshalResponse(w, rec)
}

func handleUpdateRemarks(w http.ResponseWriter, r *http.Request) {
	idStr := getPathParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	var body struct {
		Remarks string `json:"remarks"`
	}
	jsonDecodeBody(r, &body)
	updateRemarks(id, body.Remarks)
	jsonMarshalResponse(w, map[string]bool{"success": true})
}

// BUG FIX: Node returns 400 when no IDs, Go was returning 200
func handleDeleteRecords(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || len(body.IDs) == 0 {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无ID"})
		return
	}
	deleted := deleteRecords(body.IDs)
	addLog("delete", fmt.Sprintf("删除%d条记录", len(body.IDs)), deleted)
	jsonMarshalResponse(w, map[string]interface{}{"success": true, "deleted": deleted})
}

func handleGetStats(w http.ResponseWriter, r *http.Request) {
	jsonMarshalResponse(w, getStats())
}

func handleGetCarriers(w http.ResponseWriter, r *http.Request) {
	jsonMarshalResponse(w, getCarriers())
}

// BUG FIX: Node returns 400 when no IDs, Go was returning 200
func handleBatchRemarks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []int64 `json:"ids"`
		Remarks string  `json:"remarks"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || len(body.IDs) == 0 {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无ID"})
		return
	}
	updated := batchUpdateRemarks(body.IDs, body.Remarks)
	jsonMarshalResponse(w, map[string]interface{}{"success": true, "updated": updated})
}

func handleDetectCarrier(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MailNo string `json:"mailNo"`
	}
	jsonDecodeBody(r, &body)
	code := detectCarrier(body.MailNo)
	// Node: if (!mailNo) return res.json({ code: '', name: '' })
	if body.MailNo == "" {
		jsonMarshalResponse(w, map[string]string{"code": "", "name": ""})
		return
	}
	jsonMarshalResponse(w, map[string]string{"code": code, "name": getCarrierName(code)})
}

func handleCarrierRules(w http.ResponseWriter, r *http.Request) {
	jsonMarshalResponse(w, getCarrierRules())
}

func handleCheckDuplicates(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Numbers []string `json:"numbers"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || len(body.Numbers) == 0 {
		// Node: if (!numbers.length) return res.json({ duplicates: [], newCount: 0 })
		jsonMarshalResponse(w, map[string]interface{}{"duplicates": []interface{}{}, "newCount": 0, "totalCount": 0})
		return
	}

	type DupInfo struct {
		TrackingNumber string `json:"trackingNumber"`
		Status         int    `json:"status"`
		LastTime       string `json:"lastTime"`
	}

	var duplicates []DupInfo
	for _, no := range body.Numbers {
		no = strings.TrimSpace(no)
		rec := getRecordByTrackingNumber(no)
		if rec != nil {
			duplicates = append(duplicates, DupInfo{
				TrackingNumber: no,
				Status:         rec.StatusCode,
				LastTime:       rec.LastTrackTime,
			})
		}
	}

	if duplicates == nil {
		duplicates = []DupInfo{}
	}

	jsonMarshalResponse(w, map[string]interface{}{
		"duplicates": duplicates,
		"newCount":   len(body.Numbers) - len(duplicates),
		"totalCount": len(body.Numbers),
	})
}

func handleParseExcel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data string `json:"data"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || body.Data == "" {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无文件数据"})
		return
	}

	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "base64解码失败"})
		return
	}

	if bytes.HasPrefix(raw, []byte("PK\x03\x04")) {
		numbers, err := parseXLSXTrackingNumbers(raw)
		if err != nil {
			w.WriteHeader(400)
			jsonMarshalResponse(w, map[string]string{"error": "XLSX解析失败"})
			return
		}
		jsonMarshalResponse(w, map[string]interface{}{"numbers": numbers, "count": len(numbers)})
		return
	}
	if bytes.HasPrefix(raw, []byte{0xD0, 0xCF, 0x11, 0xE0}) {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "暂不支持旧版XLS，请另存为XLSX或CSV"})
		return
	}

	seen := map[string]bool{}
	var numbers []string

	// Try CSV parsing first
	csvReader := csv.NewReader(strings.NewReader(string(raw)))
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		for _, cell := range record {
			v := strings.TrimSpace(cell)
			// BUG FIX: Node uses /^[A-Za-z0-9]+$/ which is ASCII-only,
			// but some tracking numbers contain hyphens — support common formats
			if len(v) >= 5 && isTrackingNumber(v) {
				if !seen[v] {
					seen[v] = true
					numbers = append(numbers, v)
				}
			}
		}
	}

	// If CSV found nothing, try line-by-line (TXT format)
	if len(numbers) == 0 {
		for _, line := range strings.Split(string(raw), "\n") {
			for _, v := range strings.Split(line, "\t") {
				v = strings.TrimSpace(v)
				if len(v) >= 5 && isTrackingNumber(v) {
					if !seen[v] {
						seen[v] = true
						numbers = append(numbers, v)
					}
				}
			}
		}
	}

	jsonMarshalResponse(w, map[string]interface{}{"numbers": numbers, "count": len(numbers)})
}

func parseXLSXTrackingNumbers(raw []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}

	sharedStrings := parseXLSXSharedStrings(zr)
	seen := map[string]bool{}
	var numbers []string

	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		values, err := parseXLSXWorksheetValues(f, sharedStrings)
		if err != nil {
			continue
		}
		for _, v := range values {
			addTrackingCandidate(&numbers, seen, v)
		}
	}

	if numbers == nil {
		numbers = []string{}
	}
	return numbers, nil
}

func parseXLSXSharedStrings(zr *zip.Reader) []string {
	for _, f := range zr.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		raw, err := readZipFile(f)
		if err != nil {
			return nil
		}
		return parseSharedStringsXML(raw)
	}
	return nil
}

func parseSharedStringsXML(raw []byte) []string {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var values []string
	var current strings.Builder
	inSI := false
	inText := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return values
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				current.Reset()
			case "t":
				if inSI {
					inText = true
				}
			}
		case xml.CharData:
			if inSI && inText {
				current.Write([]byte(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "si":
				values = append(values, current.String())
				inSI = false
			}
		}
	}
	return values
}

func parseXLSXWorksheetValues(f *zip.File, sharedStrings []string) ([]string, error) {
	raw, err := readZipFile(f)
	if err != nil {
		return nil, err
	}

	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var values []string
	var rawValue, inlineValue strings.Builder
	inCell := false
	inValue := false
	inText := false
	cellType := ""

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return values, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				inCell = true
				cellType = ""
				rawValue.Reset()
				inlineValue.Reset()
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
						break
					}
				}
			case "v":
				if inCell {
					inValue = true
				}
			case "t":
				if inCell {
					inText = true
				}
			}
		case xml.CharData:
			if inValue {
				rawValue.Write([]byte(t))
			}
			if inText {
				inlineValue.Write([]byte(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inValue = false
			case "t":
				inText = false
			case "c":
				text := xlsxCellText(cellType, rawValue.String(), inlineValue.String(), sharedStrings)
				if strings.TrimSpace(text) != "" {
					values = append(values, text)
				}
				inCell = false
			}
		}
	}

	return values, nil
}

func xlsxCellText(cellType, rawValue, inlineValue string, sharedStrings []string) string {
	switch cellType {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil || idx < 0 || idx >= len(sharedStrings) {
			return ""
		}
		return sharedStrings[idx]
	case "inlineStr":
		return inlineValue
	default:
		return rawValue
	}
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func addTrackingCandidate(numbers *[]string, seen map[string]bool, v string) {
	v = strings.TrimSpace(v)
	if len(v) >= 5 && isTrackingNumber(v) && !seen[v] {
		seen[v] = true
		*numbers = append(*numbers, v)
	}
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs         []int64 `json:"ids"`
		ProxyAPI    string  `json:"proxyApi"`
		Timeout     int     `json:"timeout"`
		Concurrency int     `json:"concurrency"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || len(body.IDs) == 0 {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无ID"})
		return
	}

	proxyAPI := getActiveProxyAPI(body.ProxyAPI)
	timeoutSec := body.Timeout
	if timeoutSec < 1 || timeoutSec > 30 {
		timeoutSec = defaultTimeout
	}
	conc := body.Concurrency
	if conc < 1 || conc > 20 {
		conc = defaultConc
	}

	runSSEBatchFromIDs(body.IDs, proxyAPI, timeoutSec, conc, w, r)
}

func handleSyncFilter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode        string `json:"mode"`
		StatusCode  string `json:"statusCode"`
		Search      string `json:"search"`
		Carrier     string `json:"carrier"`
		Tag         string `json:"tag"`
		DateFrom    string `json:"dateFrom"`
		DateTo      string `json:"dateTo"`
		Limit       int    `json:"limit"`
		ProxyAPI    string `json:"proxyApi"`
		Timeout     int    `json:"timeout"`
		Concurrency int    `json:"concurrency"`
	}
	if err := jsonDecodeBody(r, &body); err != nil {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "参数错误"})
		return
	}

	var statusCode interface{}
	if body.StatusCode == "monitoring" {
		statusCode = "monitoring"
	} else if body.StatusCode != "" {
		if n, err := strconv.Atoi(body.StatusCode); err == nil {
			statusCode = n
		}
	}

	proxyAPI := getActiveProxyAPI(body.ProxyAPI)
	timeoutSec := body.Timeout
	if timeoutSec < 1 || timeoutSec > 30 {
		timeoutSec = defaultTimeout
	}
	conc := body.Concurrency
	if conc < 1 || conc > 20 {
		conc = defaultConc
	}

	recs := getSyncRecordsByFilter(SyncFilter{
		Mode:       body.Mode,
		StatusCode: statusCode,
		Search:     strings.TrimSpace(body.Search),
		Carrier:    strings.TrimSpace(body.Carrier),
		Tag:        strings.TrimSpace(body.Tag),
		DateFrom:   strings.TrimSpace(body.DateFrom),
		DateTo:     strings.TrimSpace(body.DateTo),
		Limit:      body.Limit,
	})
	runSSEBatchFromRecords(recs, "sync-"+body.Mode, proxyAPI, timeoutSec, conc, w, r)
}

func handleSyncMonitoring(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProxyAPI    string `json:"proxyApi"`
		Timeout     int    `json:"timeout"`
		Concurrency int    `json:"concurrency"`
	}
	jsonDecodeBody(r, &body)

	proxyAPI := getActiveProxyAPI(body.ProxyAPI)
	timeoutSec := body.Timeout
	if timeoutSec < 1 || timeoutSec > 30 {
		timeoutSec = defaultTimeout
	}
	conc := body.Concurrency
	if conc < 1 || conc > 20 {
		conc = defaultConc
	}

	runSSEBatchMonitoring(proxyAPI, timeoutSec, conc, w, r)
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	jsonMarshalResponse(w, getDashboardData())
}

func handleGetTags(w http.ResponseWriter, r *http.Request) {
	jsonMarshalResponse(w, getAllTags())
}

// BUG FIX: Node returns 400 when no IDs or missing tag, Go was returning 200
func handleBatchTags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Tag    string  `json:"tag"`
		Tags   string  `json:"tags"`
	}
	if err := jsonDecodeBody(r, &body); err != nil || len(body.IDs) == 0 {
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无ID"})
		return
	}

	var changed int
	switch body.Action {
	case "set":
		changed = batchSetTags(body.IDs, strings.TrimSpace(body.Tags))
	case "add":
		if body.Tag == "" {
			w.WriteHeader(400)
			jsonMarshalResponse(w, map[string]string{"error": "缺少标签名"})
			return
		}
		changed = batchAddTag(body.IDs, strings.TrimSpace(body.Tag))
	case "remove":
		if body.Tag == "" {
			w.WriteHeader(400)
			jsonMarshalResponse(w, map[string]string{"error": "缺少标签名"})
			return
		}
		changed = batchRemoveTag(body.IDs, strings.TrimSpace(body.Tag))
	default:
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "action 须为 set/add/remove"})
		return
	}

	addLog("batch-tag", fmt.Sprintf("%s: %s → %d条", body.Action, body.Tag+body.Tags, len(body.IDs)), changed)
	jsonMarshalResponse(w, map[string]interface{}{"success": true, "changed": changed})
}

func handleGetLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 50
	}
	jsonMarshalResponse(w, getLogs(limit))
}

// ── Helpers ────────────────────────────────────────

func getActiveProxyAPI(custom string) string {
	if v := strings.TrimSpace(custom); v != "" {
		return v
	}
	return getSetting("proxyApi", "")
}

// BUG FIX: getPathParam was fragile — only found last numeric segment.
// Now properly extracts the ID segment from /api/records/{id} and /api/records/{id}/remarks
func getPathParam(r *http.Request, key string) string {
	path := r.URL.Path
	parts := strings.Split(strings.TrimRight(path, "/"), "/")

	// For /api/records/{id}/remarks, the ID is at index 3
	// For /api/records/{id}, the ID is at index 3
	if len(parts) >= 4 && parts[1] == "api" && parts[2] == "records" {
		// Check if parts[3] is numeric
		if _, err := strconv.ParseInt(parts[3], 10, 64); err == nil {
			return parts[3]
		}
	}

	// Fallback: find last numeric segment
	for i := len(parts) - 1; i >= 0; i-- {
		if _, err := strconv.ParseInt(parts[i], 10, 64); err == nil {
			return parts[i]
		}
	}
	return ""
}

// isAlphanumeric checks if string is ASCII alphanumeric (matches Node's /^[A-Za-z0-9]+$/)
func isAlphanumeric(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// isTrackingNumber checks if string looks like a tracking number.
// Supports alphanumeric + hyphens (common format like YT-1234567890)
func isTrackingNumber(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func jsonDecodeBody(r *http.Request, v interface{}) error {
	decoder := json.NewDecoder(r.Body)
	return decoder.Decode(v)
}
