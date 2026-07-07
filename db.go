package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ── Data types ──────────────────────────────────────

type Shipment struct {
	ID             int64       `json:"id"`
	TrackingNumber string      `json:"tracking_number"`
	CarrierCode    string      `json:"carrier_code"`
	CarrierName    string      `json:"carrier_name"`
	Status         string      `json:"status"`
	StatusCode     int         `json:"status_code"`
	StatusDesc     string      `json:"status_desc"`
	LastTrackTime  string      `json:"last_track_time"`
	LastTrackDesc  string      `json:"last_track_desc"`
	CurrentCity    string      `json:"current_city"`
	FromCity       string      `json:"from_city"`
	Predict        string      `json:"predict"`
	Progress       string      `json:"progress"`
	TraceCount     int         `json:"trace_count"`
	ResultJSON     interface{} `json:"result_json"`
	Remarks        string      `json:"remarks"`
	Tags           string      `json:"tags"`
	BatchID        string      `json:"batch_id"`
	RequestCount   int         `json:"request_count"`
	ErrorMsg       string      `json:"error_msg"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
}

type ParsedResult struct {
	MailNo     string      `json:"mailNo"`
	CpCode     string      `json:"cpCode"`
	CpName     string      `json:"cpName"`
	Status     string      `json:"status"`
	StatusCode string      `json:"statusCode"`
	StatusDesc string      `json:"statusDesc"`
	Progress   string      `json:"progress"`
	From       string      `json:"from"`
	Current    string      `json:"current"`
	Predict    string      `json:"predict"`
	TraceCount int         `json:"traceCount"`
	LastTime   string      `json:"lastTime"`
	LastDesc   string      `json:"lastDesc"`
	Traces     []TraceItem `json:"traces"`
}

type TraceItem struct {
	Time string `json:"time"`
	Desc string `json:"desc"`
}

type Stats struct {
	Total         int `json:"total"`
	NoTracking    int `json:"noTracking"`
	PendingPickup int `json:"pendingPickup"`
	PickedUp      int `json:"pickedUp"`
	InTransit     int `json:"inTransit"`
	Delivering    int `json:"delivering"`
	WaitingPickup int `json:"waitingPickup"`
	Delivered     int `json:"delivered"`
	Abnormal      int `json:"abnormal"`
	Monitoring    int `json:"monitoring"`
	Stale         int `json:"stale"`
	LongTransit   int `json:"longTransit"`
}

type CarrierInfo struct {
	CarrierName string `json:"carrier_name"`
	CarrierCode string `json:"carrier_code"`
	Count       int    `json:"count"`
}

type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type OpLog struct {
	ID        int64  `json:"id"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
	Count     int    `json:"count"`
	CreatedAt string `json:"created_at"`
}

type DashboardData struct {
	ByDate    []DateCount    `json:"byDate"`
	ByCarrier []CarrierInfo  `json:"byCarrier"`
	ByStatus  []StatusCount  `json:"byStatus"`
	Recent7   []RecentRecord `json:"recent7"`
}

type DateCount struct {
	Date  string `json:"d"`
	Count int    `json:"c"`
}

type StatusCount struct {
	Code  int `json:"code"`
	Count int `json:"c"`
}

type RecentRecord struct {
	Date       string `json:"d"`
	StatusCode int    `json:"code"`
	Count      int    `json:"c"`
}

type RecordsResult struct {
	Records  []Shipment `json:"records"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
}

type SyncFilter struct {
	Mode       string
	StatusCode interface{}
	Search     string
	Carrier    string
	Tag        string
	DateFrom   string
	DateTo     string
	Limit      int
}

// ── DB handle ───────────────────────────────────────

var db *sql.DB

// mustExec wraps db.Exec with error logging
func mustExec(query string, args ...interface{}) sql.Result {
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("[DB ERROR] Exec failed: %v, query: %.80s", err, query)
	}
	return result
}

// mustExecErr returns (result, err) with logging
func mustExecErr(query string, args ...interface{}) (sql.Result, error) {
	result, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("[DB ERROR] Exec failed: %v, query: %.80s", err, query)
	}
	return result, err
}

// initTraces ensures Traces is never nil
func initTraces(p *ParsedResult) {
	if p.Traces == nil {
		p.Traces = []TraceItem{}
	}
}

func initDB() {
	dbDir := filepath.Join(".", "data")
	os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "logistics.db")

	var err error
	db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		panic("open db: " + err.Error())
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS shipments (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		tracking_number  TEXT NOT NULL UNIQUE,
		carrier_code     TEXT DEFAULT '',
		carrier_name     TEXT DEFAULT '',
		status           TEXT DEFAULT '',
		status_code      INTEGER DEFAULT 0,
		status_desc      TEXT DEFAULT '',
		last_track_time  TEXT DEFAULT '',
		last_track_desc  TEXT DEFAULT '',
		current_city     TEXT DEFAULT '',
		from_city        TEXT DEFAULT '',
		predict          TEXT DEFAULT '',
		progress         TEXT DEFAULT '',
		trace_count      INTEGER DEFAULT 0,
		result_json      TEXT DEFAULT '{}',
		remarks          TEXT DEFAULT '',
		batch_id         TEXT DEFAULT '',
		request_count    INTEGER DEFAULT 0,
		error_msg        TEXT DEFAULT '',
		created_at       TEXT DEFAULT (datetime('now','localtime')),
		updated_at       TEXT DEFAULT (datetime('now','localtime'))
	);
	CREATE INDEX IF NOT EXISTS idx_status_code ON shipments(status_code);
	CREATE INDEX IF NOT EXISTS idx_batch_id ON shipments(batch_id);
	CREATE INDEX IF NOT EXISTS idx_carrier_code ON shipments(carrier_code);
	CREATE INDEX IF NOT EXISTS idx_created_at ON shipments(created_at);
	CREATE INDEX IF NOT EXISTS idx_updated_at ON shipments(updated_at);
	CREATE TABLE IF NOT EXISTS app_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT DEFAULT '',
		updated_at TEXT DEFAULT (datetime('now','localtime'))
	);
	CREATE TABLE IF NOT EXISTS operation_logs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		action     TEXT NOT NULL,
		detail     TEXT DEFAULT '',
		count      INTEGER DEFAULT 0,
		created_at TEXT DEFAULT (datetime('now','localtime'))
	);
	`
	for _, s := range strings.Split(schema, ";") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, err := mustExecErr(s); err != nil {
			panic("exec schema: " + err.Error())
		}
	}

	ensureColumn("shipments", "tags", "TEXT DEFAULT ''")
}

func ensureColumn(table, column, definition string) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		log.Printf("[DB ERROR] inspect table %s failed: %v", table, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err == nil && name == column {
			return
		}
	}
	mustExec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
}

// ── Status normalization ────────────────────────────

var statusPatterns = []struct {
	code int
	re   *regexp.Regexp
}{
	{6, regexp.MustCompile(`异常|退回|退件|拒`)},
	{5, regexp.MustCompile(`签收`)},
	{4, regexp.MustCompile(`派送|派件`)},
	{7, regexp.MustCompile(`待取件|待领取|暂存|驿站`)},
	{3, regexp.MustCompile(`运输|转运|途中|到达`)},
	{1, regexp.MustCompile(`待揽收|下单|已下单`)},
	{2, regexp.MustCompile(`揽收|收件`)},
}

func normalizeStatusCode(status string) int {
	if status == "" {
		return 0
	}
	for _, p := range statusPatterns {
		if p.re.MatchString(status) {
			return p.code
		}
	}
	return 0
}

// ── Upsert ─────────────────────────────────────────

func upsertShipment(p ParsedResult, batchID string) error {
	sc := normalizeStatusCode(p.Status)
	if sc == 0 && p.StatusDesc != "" {
		sc = normalizeStatusCode(p.StatusDesc)
	}
	if sc == 0 && p.TraceCount > 0 {
		sc = 3
	}
	descSc := normalizeStatusCode(p.LastDesc)
	if descSc == 7 && sc < 7 {
		sc = 7
	}
	if descSc == 5 && sc < 5 {
		sc = 5
	}

	resultJSON := toJSON(p)

	q := `INSERT INTO shipments (tracking_number, carrier_code, carrier_name, status, status_code, status_desc,
		last_track_time, last_track_desc, current_city, from_city, predict, progress, trace_count,
		result_json, batch_id, request_count, error_msg)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, '')
		ON CONFLICT(tracking_number) DO UPDATE SET
		carrier_code=excluded.carrier_code,
		carrier_name=excluded.carrier_name,
		status=excluded.status,
		status_code=excluded.status_code,
		status_desc=excluded.status_desc,
		last_track_time=excluded.last_track_time,
		last_track_desc=excluded.last_track_desc,
		current_city=excluded.current_city,
		from_city=excluded.from_city,
		predict=excluded.predict,
		progress=excluded.progress,
		trace_count=excluded.trace_count,
		result_json=excluded.result_json,
		batch_id=CASE WHEN excluded.batch_id!='' THEN excluded.batch_id ELSE shipments.batch_id END,
		request_count=shipments.request_count+1,
		error_msg='',
		updated_at=datetime('now','localtime')`
	_, err := mustExecErr(q, p.MailNo, p.CpCode, p.CpName, p.Status, sc, p.StatusDesc,
		p.LastTime, p.LastDesc, p.Current, p.From,
		p.Predict, p.Progress, p.TraceCount, resultJSON, batchID)
	return err
}

func upsertFailed(trackingNumber, errorMsg, batchID string) error {
	q := `INSERT INTO shipments (tracking_number, status_code, error_msg, batch_id, request_count)
		VALUES (?, 0, ?, ?, 1)
		ON CONFLICT(tracking_number) DO UPDATE SET
		error_msg=excluded.error_msg,
		batch_id=CASE WHEN excluded.batch_id!='' THEN excluded.batch_id ELSE shipments.batch_id END,
		request_count=shipments.request_count+1,
		updated_at=datetime('now','localtime')`
	_, err := mustExecErr(q, trackingNumber, errorMsg, batchID)
	return err
}

// ── Records query ──────────────────────────────────

var allowedSort = map[string]bool{
	"id": true, "tracking_number": true, "status_code": true,
	"created_at": true, "updated_at": true, "last_track_time": true,
	"carrier_name": true, "current_city": true,
}

func buildRecordWhere(statusCode interface{}, search, carrier, tag, dateFrom, dateTo string) (string, []interface{}) {
	conditions := []string{}
	params := []interface{}{}

	if sc, ok := statusCode.(int); ok {
		conditions = append(conditions, "status_code = ?")
		params = append(params, sc)
	} else if s, ok := statusCode.(string); ok && s == "monitoring" {
		conditions = append(conditions, "status_code IN (1,2,3,4,7)")
	}

	if search != "" {
		conditions = append(conditions, "tracking_number LIKE ?")
		params = append(params, "%"+search+"%")
	}
	if carrier != "" {
		conditions = append(conditions, "(carrier_code LIKE ? OR carrier_name LIKE ?)")
		params = append(params, "%"+carrier+"%", "%"+carrier+"%")
	}
	if dateFrom != "" {
		conditions = append(conditions, "created_at >= ?")
		params = append(params, dateFrom)
	}
	if dateTo != "" {
		conditions = append(conditions, "created_at <= ?")
		params = append(params, dateTo+" 23:59:59")
	}
	if tag != "" {
		conditions = append(conditions, "(',' || tags || ',' LIKE '%,' || ? || ',%')")
		params = append(params, tag)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}
	return whereClause, params
}

func getRecords(page, pageSize int, statusCode interface{}, search, carrier, tag, sort, order, dateFrom, dateTo string) RecordsResult {
	whereClause, params := buildRecordWhere(statusCode, search, carrier, tag, dateFrom, dateTo)

	// count
	var total int
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM shipments %s", whereClause)
	db.QueryRow(countQ, params...).Scan(&total)

	// sort
	sortCol := "updated_at"
	if allowedSort[sort] {
		sortCol = sort
	}
	sortDir := "DESC"
	if strings.EqualFold(order, "asc") {
		sortDir = "ASC"
	}

	offset := (page - 1) * pageSize
	queryQ := fmt.Sprintf("SELECT id, tracking_number, carrier_code, carrier_name, status, status_code, status_desc, last_track_time, last_track_desc, current_city, from_city, predict, progress, trace_count, result_json, remarks, batch_id, request_count, error_msg, created_at, updated_at, tags FROM shipments %s ORDER BY %s %s LIMIT ? OFFSET ?", whereClause, sortCol, sortDir)
	qParams := append(params, pageSize, offset)

	rows, err := db.Query(queryQ, qParams...)
	if err != nil {
		return RecordsResult{Total: total, Page: page, PageSize: pageSize}
	}
	defer rows.Close()

	var records []Shipment
	for rows.Next() {
		var s Shipment
		var resultJSON, tags, fromCity, predict, progress, statusDesc, status, lastTrackTime, lastTrackDesc, currentCity, remarks, batchID, errorMsg sql.NullString
		var carrierCode, carrierName sql.NullString
		var createdAt, updatedAt sql.NullString

		rows.Scan(&s.ID, &s.TrackingNumber, &carrierCode, &carrierName, &status, &s.StatusCode,
			&statusDesc, &lastTrackTime, &lastTrackDesc, &currentCity, &fromCity,
			&predict, &progress, &s.TraceCount, &resultJSON, &remarks, &batchID,
			&s.RequestCount, &errorMsg, &createdAt, &updatedAt, &tags)

		s.CarrierCode = nullStr(carrierCode)
		s.CarrierName = nullStr(carrierName)
		s.Status = nullStr(status)
		s.StatusDesc = nullStr(statusDesc)
		s.LastTrackTime = nullStr(lastTrackTime)
		s.LastTrackDesc = nullStr(lastTrackDesc)
		s.CurrentCity = nullStr(currentCity)
		s.FromCity = nullStr(fromCity)
		s.Predict = nullStr(predict)
		s.Progress = nullStr(progress)
		if resultJSON.Valid {
			var parsed interface{}
			if json.Unmarshal([]byte(resultJSON.String), &parsed) == nil {
				s.ResultJSON = parsed
			} else {
				s.ResultJSON = resultJSON.String
			}
		} else {
			s.ResultJSON = nil
		}
		s.Remarks = nullStr(remarks)
		s.Tags = nullStr(tags)
		s.BatchID = nullStr(batchID)
		s.ErrorMsg = nullStr(errorMsg)
		s.CreatedAt = nullStr(createdAt)
		s.UpdatedAt = nullStr(updatedAt)

		records = append(records, s)
	}
	if records == nil {
		records = []Shipment{}
	}

	return RecordsResult{Records: records, Total: total, Page: page, PageSize: pageSize}
}

func getSyncRecordsByFilter(f SyncFilter) []Shipment {
	limit := f.Limit
	if limit < 1 {
		limit = 100000
	}
	if limit > 100000 {
		limit = 100000
	}

	var whereClause string
	var params []interface{}
	switch f.Mode {
	case "noTracking":
		whereClause = "WHERE status_code = 0"
	case "failed":
		whereClause = "WHERE error_msg <> ''"
	case "monitoring":
		whereClause = "WHERE status_code IN (1,2,3,4,7)"
	default:
		whereClause, params = buildRecordWhere(f.StatusCode, f.Search, f.Carrier, f.Tag, f.DateFrom, f.DateTo)
	}

	queryQ := fmt.Sprintf("SELECT id, tracking_number, carrier_code FROM shipments %s ORDER BY updated_at DESC LIMIT ?", whereClause)
	params = append(params, limit)
	rows, err := db.Query(queryQ, params...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var recs []Shipment
	for rows.Next() {
		var s Shipment
		var cc sql.NullString
		rows.Scan(&s.ID, &s.TrackingNumber, &cc)
		s.CarrierCode = nullStr(cc)
		recs = append(recs, s)
	}
	if recs == nil {
		recs = []Shipment{}
	}
	return recs
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func getRecord(id int64) *Shipment {
	var s Shipment
	var resultJSON, tags, fromCity, predict, progress, statusDesc, status, lastTrackTime, lastTrackDesc, currentCity, remarks, batchID, errorMsg sql.NullString
	var carrierCode, carrierName sql.NullString
	var createdAt, updatedAt sql.NullString

	err := db.QueryRow("SELECT id, tracking_number, carrier_code, carrier_name, status, status_code, status_desc, last_track_time, last_track_desc, current_city, from_city, predict, progress, trace_count, result_json, remarks, batch_id, request_count, error_msg, created_at, updated_at, tags FROM shipments WHERE id = ?", id).Scan(
		&s.ID, &s.TrackingNumber, &carrierCode, &carrierName, &status, &s.StatusCode,
		&statusDesc, &lastTrackTime, &lastTrackDesc, &currentCity, &fromCity,
		&predict, &progress, &s.TraceCount, &resultJSON, &remarks, &batchID,
		&s.RequestCount, &errorMsg, &createdAt, &updatedAt, &tags)
	if err != nil {
		return nil
	}

	s.CarrierCode = nullStr(carrierCode)
	s.CarrierName = nullStr(carrierName)
	s.Status = nullStr(status)
	s.StatusDesc = nullStr(statusDesc)
	s.LastTrackTime = nullStr(lastTrackTime)
	s.LastTrackDesc = nullStr(lastTrackDesc)
	s.CurrentCity = nullStr(currentCity)
	s.FromCity = nullStr(fromCity)
	s.Predict = nullStr(predict)
	s.Progress = nullStr(progress)
	if resultJSON.Valid {
		var parsed interface{}
		if json.Unmarshal([]byte(resultJSON.String), &parsed) == nil {
			s.ResultJSON = parsed
		} else {
			s.ResultJSON = resultJSON.String
		}
	} else {
		s.ResultJSON = nil
	}
	s.Remarks = nullStr(remarks)
	s.Tags = nullStr(tags)
	s.BatchID = nullStr(batchID)
	s.ErrorMsg = nullStr(errorMsg)
	s.CreatedAt = nullStr(createdAt)
	s.UpdatedAt = nullStr(updatedAt)

	return &s
}

func getRecordByTrackingNumber(tn string) *Shipment {
	var s Shipment
	var resultJSON, tags, fromCity, predict, progress, statusDesc, status, lastTrackTime, lastTrackDesc, currentCity, remarks, batchID, errorMsg sql.NullString
	var carrierCode, carrierName sql.NullString
	var createdAt, updatedAt sql.NullString

	err := db.QueryRow("SELECT id, tracking_number, carrier_code, carrier_name, status, status_code, status_desc, last_track_time, last_track_desc, current_city, from_city, predict, progress, trace_count, result_json, remarks, batch_id, request_count, error_msg, created_at, updated_at, tags FROM shipments WHERE tracking_number = ?", tn).Scan(
		&s.ID, &s.TrackingNumber, &carrierCode, &carrierName, &status, &s.StatusCode,
		&statusDesc, &lastTrackTime, &lastTrackDesc, &currentCity, &fromCity,
		&predict, &progress, &s.TraceCount, &resultJSON, &remarks, &batchID,
		&s.RequestCount, &errorMsg, &createdAt, &updatedAt, &tags)
	if err != nil {
		return nil
	}

	s.CarrierCode = nullStr(carrierCode)
	s.CarrierName = nullStr(carrierName)
	s.Status = nullStr(status)
	s.StatusDesc = nullStr(statusDesc)
	s.LastTrackTime = nullStr(lastTrackTime)
	s.LastTrackDesc = nullStr(lastTrackDesc)
	s.CurrentCity = nullStr(currentCity)
	s.FromCity = nullStr(fromCity)
	s.Predict = nullStr(predict)
	s.Progress = nullStr(progress)
	if resultJSON.Valid {
		var parsed interface{}
		if json.Unmarshal([]byte(resultJSON.String), &parsed) == nil {
			s.ResultJSON = parsed
		} else {
			s.ResultJSON = resultJSON.String
		}
	} else {
		s.ResultJSON = nil
	}
	s.Remarks = nullStr(remarks)
	s.Tags = nullStr(tags)
	s.BatchID = nullStr(batchID)
	s.ErrorMsg = nullStr(errorMsg)
	s.CreatedAt = nullStr(createdAt)
	s.UpdatedAt = nullStr(updatedAt)

	return &s
}

func updateRemarks(id int64, remarks string) {
	mustExec("UPDATE shipments SET remarks = ?, updated_at = datetime('now','localtime') WHERE id = ?", remarks, id)
}

func deleteRecords(ids []int64) int {
	if len(ids) == 0 {
		return 0
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf("DELETE FROM shipments WHERE id IN (%s)", strings.Join(placeholders, ","))
	r, _ := mustExecErr(q, args...)
	n, _ := r.RowsAffected()
	return int(n)
}

// ── Stats ──────────────────────────────────────────

func getStats() Stats {
	rows, err := db.Query("SELECT status_code, COUNT(*) FROM shipments GROUP BY status_code")
	if err != nil {
		return Stats{}
	}
	defer rows.Close()

	m := map[int]int{}
	total := 0
	for rows.Next() {
		var code, count int
		rows.Scan(&code, &count)
		m[code] = count
		total += count
	}

	var stale, longTransit int
	db.QueryRow("SELECT COUNT(*) FROM shipments WHERE status_code IN (1,2,3,4,7) AND updated_at < datetime('now','localtime','-3 days')").Scan(&stale)
	db.QueryRow("SELECT COUNT(*) FROM shipments WHERE status_code = 3 AND updated_at < datetime('now','localtime','-7 days')").Scan(&longTransit)

	return Stats{
		Total:         total,
		NoTracking:    m[0],
		PendingPickup: m[1],
		PickedUp:      m[2],
		InTransit:     m[3],
		Delivering:    m[4],
		WaitingPickup: m[7],
		Delivered:     m[5],
		Abnormal:      m[6],
		Monitoring:    m[1] + m[2] + m[3] + m[4] + m[7],
		Stale:         stale,
		LongTransit:   longTransit,
	}
}

func getAllSyncRecords(limit int) []Shipment {
	if limit < 1 {
		limit = 10000
	}
	if limit > 100000 {
		limit = 100000
	}
	rows, err := db.Query("SELECT id, tracking_number, carrier_code FROM shipments ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var recs []Shipment
	for rows.Next() {
		var s Shipment
		var cc sql.NullString
		rows.Scan(&s.ID, &s.TrackingNumber, &cc)
		s.CarrierCode = nullStr(cc)
		recs = append(recs, s)
	}
	return recs
}

func getCarriers() []CarrierInfo {
	rows, err := db.Query("SELECT carrier_name, carrier_code, COUNT(*) as count FROM shipments WHERE carrier_name != '' GROUP BY carrier_name ORDER BY count DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var carriers []CarrierInfo
	for rows.Next() {
		var c CarrierInfo
		rows.Scan(&c.CarrierName, &c.CarrierCode, &c.Count)
		carriers = append(carriers, c)
	}
	return carriers
}

func batchUpdateRemarks(ids []int64, remarks string) int {
	if len(ids) == 0 {
		return 0
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)+1)
	args[0] = remarks
	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}
	q := fmt.Sprintf("UPDATE shipments SET remarks = ?, updated_at = datetime('now','localtime') WHERE id IN (%s)", strings.Join(placeholders, ","))
	r, _ := mustExecErr(q, args...)
	n, _ := r.RowsAffected()
	return int(n)
}

// ── recalcAllStatus ─────────────────────────────────

func recalcAllStatus() int {
	limit := 500
	lastID := 0
	updated := 0
	type statusRecalcRow struct {
		id         int64
		resultJSON string
	}

	for {
		rows, err := db.Query("SELECT id, result_json FROM shipments WHERE id > ? AND result_json != '{}' ORDER BY id ASC LIMIT ?", lastID, limit)
		if err != nil {
			break
		}

		recalcRows := make([]statusRecalcRow, 0, limit)
		for rows.Next() {
			var id int64
			var resultJSON string
			rows.Scan(&id, &resultJSON)
			recalcRows = append(recalcRows, statusRecalcRow{id: id, resultJSON: resultJSON})
			lastID = int(id)
		}
		rows.Close()

		for _, row := range recalcRows {
			var p ParsedResult
			if err := jsonUnmarshal([]byte(row.resultJSON), &p); err != nil {
				continue
			}

			sc := normalizeStatusCode(p.Status)
			if sc == 0 && p.StatusDesc != "" {
				sc = normalizeStatusCode(p.StatusDesc)
			}
			if sc == 0 && p.TraceCount > 0 {
				sc = 3
			}
			descSc := normalizeStatusCode(p.LastDesc)
			if descSc == 7 && sc < 7 {
				sc = 7
			}
			if descSc == 5 && sc < 5 {
				sc = 5
			}

			mustExec("UPDATE shipments SET status_code = ? WHERE id = ?", sc, row.id)
			updated++
		}

		if len(recalcRows) < limit {
			break
		}
	}

	return updated
}

// ── Settings ────────────────────────────────────────

func getSetting(key, fallback string) string {
	var val string
	err := db.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return fallback
	}
	return val
}

func setSetting(key, value string) {
	mustExec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, datetime('now','localtime'))
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=datetime('now','localtime')`, key, value)
}

type AppSettings struct {
	AppKey       string `json:"appKey"`
	ProxyAPI     string `json:"proxyApi"`
	Timeout      int    `json:"timeout"`
	Concurrency  int    `json:"concurrency"`
	MonitorLimit int    `json:"monitorLimit"`
	Port         int    `json:"port"`
}

func getAppSettings(defaults AppSettings) AppSettings {
	timeout, _ := strconv.Atoi(getSetting("timeout", strconv.Itoa(defaults.Timeout)))
	concurrency, _ := strconv.Atoi(getSetting("concurrency", strconv.Itoa(defaults.Concurrency)))
	monitorLimit, _ := strconv.Atoi(getSetting("monitorLimit", strconv.Itoa(defaults.MonitorLimit)))
	port, _ := strconv.Atoi(getSetting("port", strconv.Itoa(defaults.Port)))
	if timeout < 1 {
		timeout = defaults.Timeout
	}
	if concurrency < 1 {
		concurrency = defaults.Concurrency
	}
	if monitorLimit < 1 {
		monitorLimit = defaults.MonitorLimit
	}
	if port < 1 || port > 65535 {
		port = defaults.Port
	}
	return AppSettings{
		AppKey:       getSetting("appKey", defaults.AppKey),
		ProxyAPI:     getSetting("proxyApi", defaults.ProxyAPI),
		Timeout:      timeout,
		Concurrency:  concurrency,
		MonitorLimit: monitorLimit,
		Port:         port,
	}
}

func updateAppSettings(s AppSettings) AppSettings {
	// Only update non-empty/non-zero fields to avoid overwriting with defaults from partial form
	if ak := strings.TrimSpace(s.AppKey); ak != "" {
		setSetting("appKey", ak)
	}
	setSetting("proxyApi", strings.TrimSpace(s.ProxyAPI))
	if s.Timeout > 0 {
		setSetting("timeout", strconv.Itoa(s.Timeout))
	}
	if s.Concurrency > 0 {
		setSetting("concurrency", strconv.Itoa(s.Concurrency))
	}
	if s.MonitorLimit > 0 {
		setSetting("monitorLimit", strconv.Itoa(s.MonitorLimit))
	}
	if s.Port > 0 {
		setSetting("port", strconv.Itoa(s.Port))
	}
	return getAppSettings(AppSettings{})
}

// ── Tags ───────────────────────────────────────────

func getAllTags() []TagInfo {
	rows, err := db.Query("SELECT tags FROM shipments WHERE tags != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	m := map[string]int{}
	for rows.Next() {
		var tags string
		rows.Scan(&tags)
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				m[t]++
			}
		}
	}

	var result []TagInfo
	for name, count := range m {
		result = append(result, TagInfo{Name: name, Count: count})
	}
	// sort by count desc
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Count > result[i].Count {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if result == nil {
		result = []TagInfo{}
	}
	return result
}

func batchSetTags(ids []int64, tags string) int {
	if len(ids) == 0 {
		return 0
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)+1)
	args[0] = tags
	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}
	q := fmt.Sprintf("UPDATE shipments SET tags = ?, updated_at = datetime('now','localtime') WHERE id IN (%s)", strings.Join(placeholders, ","))
	r, _ := mustExecErr(q, args...)
	n, _ := r.RowsAffected()
	return int(n)
}

func batchAddTag(ids []int64, tag string) int {
	if len(ids) == 0 || tag == "" {
		return 0
	}
	changed := 0
	for _, id := range ids {
		var existing string
		err := db.QueryRow("SELECT tags FROM shipments WHERE id = ?", id).Scan(&existing)
		if err != nil {
			continue
		}
		parts := splitTags(existing)
		if containsStr(parts, tag) {
			continue
		}
		parts = append(parts, tag)
		mustExec("UPDATE shipments SET tags = ?, updated_at = datetime('now','localtime') WHERE id = ?", strings.Join(parts, ","), id)
		changed++
	}
	return changed
}

func batchRemoveTag(ids []int64, tag string) int {
	if len(ids) == 0 || tag == "" {
		return 0
	}
	changed := 0
	for _, id := range ids {
		var existing string
		err := db.QueryRow("SELECT tags FROM shipments WHERE id = ?", id).Scan(&existing)
		if err != nil {
			continue
		}
		parts := splitTags(existing)
		filtered := []string{}
		for _, t := range parts {
			if t != tag {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == len(parts) {
			continue
		}
		mustExec("UPDATE shipments SET tags = ?, updated_at = datetime('now','localtime') WHERE id = ?", strings.Join(filtered, ","), id)
		changed++
	}
	return changed
}

func splitTags(s string) []string {
	var result []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ── Operation logs ──────────────────────────────────

func addLog(action, detail string, count int) {
	mustExec("INSERT INTO operation_logs (action, detail, count) VALUES (?, ?, ?)", action, detail, count)
}

func getLogs(limit int) []OpLog {
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := db.Query("SELECT * FROM operation_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var logs []OpLog
	for rows.Next() {
		var l OpLog
		var detail, createdAt sql.NullString
		rows.Scan(&l.ID, &l.Action, &detail, &l.Count, &createdAt)
		l.Detail = nullStr(detail)
		l.CreatedAt = nullStr(createdAt)
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []OpLog{}
	}
	return logs
}

// ── Dashboard ──────────────────────────────────────

func getDashboardData() DashboardData {
	data := DashboardData{}

	// byDate
	rows, _ := db.Query("SELECT date(created_at) as d, COUNT(*) as c FROM shipments GROUP BY d ORDER BY d DESC LIMIT 30")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var dc DateCount
			rows.Scan(&dc.Date, &dc.Count)
			data.ByDate = append(data.ByDate, dc)
		}
	}
	// reverse
	for i, j := 0, len(data.ByDate)-1; i < j; i, j = i+1, j-1 {
		data.ByDate[i], data.ByDate[j] = data.ByDate[j], data.ByDate[i]
	}

	// byCarrier
	rows, _ = db.Query("SELECT carrier_name as name, COUNT(*) as c FROM shipments WHERE carrier_name != '' GROUP BY carrier_name ORDER BY c DESC LIMIT 10")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var c CarrierInfo
			rows.Scan(&c.CarrierName, &c.Count)
			data.ByCarrier = append(data.ByCarrier, c)
		}
	}

	// byStatus
	rows, _ = db.Query("SELECT status_code as code, COUNT(*) as c FROM shipments GROUP BY status_code ORDER BY code")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s StatusCount
			rows.Scan(&s.Code, &s.Count)
			data.ByStatus = append(data.ByStatus, s)
		}
	}

	// recent7
	rows, _ = db.Query("SELECT date(created_at) as d, status_code as code, COUNT(*) as c FROM shipments WHERE created_at >= datetime('now','localtime','-7 days') GROUP BY d, code ORDER BY d")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var r RecentRecord
			rows.Scan(&r.Date, &r.StatusCode, &r.Count)
			data.Recent7 = append(data.Recent7, r)
		}
	}

	return data
}

// ── JSON helpers ────────────────────────────────────

func toJSON(v interface{}) string {
	b, err := jsonMarshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// nowLocal returns current local time in SQLite format
func nowLocal() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
