package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── SSE event types ────────────────────────────────

type SSEEvent struct {
	Type    string      `json:"type"`
	Index   int         `json:"index,omitempty"`
	MailNo  string      `json:"mailNo,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	OK      int         `json:"ok,omitempty"`
	Fail    int         `json:"fail,omitempty"`
	Total   int         `json:"total,omitempty"`
	Elapsed string      `json:"elapsed,omitempty"`
}

// ── SSE batch runner ────────────────────────────────

func runSSEBatch(numbers []string, cpCode, proxyAPI string, timeoutSec, concurrency int, batchID string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// BUG FIX: Node calls res.flushHeaders() to ensure headers are sent immediately
	// This prevents proxy buffering issues
	w.WriteHeader(200)
	flusher.Flush()

	writeMu := &sync.Mutex{}

	// send init
	sendSSE(w, flusher, writeMu, SSEEvent{Type: "init", Total: len(numbers)})

	// track connection close — Node uses res.on('close') + res.on('error')
	ctx := r.Context()
	closed := int32(0)

	var okCount, failCount, nextIdx int32
	t0 := time.Now()
	maxRetries := 3
	if proxyAPI != "" {
		maxRetries = 8
	}

	// heartbeat — matches Node's setInterval(20000)
	heartbeatDone := make(chan struct{})
	stopHeartbeat := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if atomic.LoadInt32(&closed) == 1 {
					return
				}
				writeMu.Lock()
				if _, err := fmt.Fprintf(w, ":heartbeat\n\n"); err != nil {
					writeMu.Unlock()
					atomic.StoreInt32(&closed, 1)
					return
				}
				flusher.Flush()
				writeMu.Unlock()
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	// BUG FIX: Node staggers worker startup with sleep(i * 150ms)
	// This prevents all workers from hitting the API at the same time
	for i := 0; i < len(numbers); i++ {
		if atomic.LoadInt32(&closed) == 1 {
			break
		}
		if ctx.Err() != nil {
			break
		}

		idx := int(atomic.AddInt32(&nextIdx, 1)) - 1
		if idx >= len(numbers) {
			break
		}
		no := strings.TrimSpace(numbers[idx])
		if no == "" {
			continue
		}

		effectiveCp := cpCode
		if effectiveCp == "" {
			effectiveCp = detectCarrier(no)
		}

		sendSSE(w, flusher, writeMu, SSEEvent{Type: "start", Index: idx, MailNo: no})

		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, no, effectiveCp string) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := queryWithRetry(no, effectiveCp, proxyAPI, timeoutSec*1000, maxRetries, func() bool {
				return atomic.LoadInt32(&closed) == 1 || ctx.Err() != nil
			})

			if err != nil {
				// BUG FIX: Node checks "已取消" and breaks, Go should also stop on cancel
				if err.Error() == "已取消" {
					atomic.StoreInt32(&closed, 1)
					return
				}
				fail := atomic.AddInt32(&failCount, 1)
				o := atomic.LoadInt32(&okCount)
				upsertFailed(no, err.Error(), batchID)
				sendSSE(w, flusher, writeMu, SSEEvent{
					Type: "error", Index: idx, MailNo: no, Error: err.Error(),
					OK: int(o), Fail: int(fail), Total: len(numbers),
				})
				return
			}

			info := parseResult(result)
			if info != nil {
				o := atomic.AddInt32(&okCount, 1)
				fail := atomic.LoadInt32(&failCount)
				upsertShipment(*info, batchID)
				sendSSE(w, flusher, writeMu, SSEEvent{
					Type: "result", Index: idx, MailNo: no, Data: info,
					OK: int(o), Fail: int(fail), Total: len(numbers),
				})
			} else {
				fail := atomic.AddInt32(&failCount, 1)
				o := atomic.LoadInt32(&okCount)
				upsertFailed(no, "无物流数据", batchID)
				sendSSE(w, flusher, writeMu, SSEEvent{
					Type: "error", Index: idx, MailNo: no, Error: "无物流数据",
					OK: int(o), Fail: int(fail), Total: len(numbers),
				})
			}
		}(idx, no, effectiveCp)

		// BUG FIX: Node staggers worker startup with sleep(i * 150ms)
		// Add a small delay between dispatching workers to avoid API burst
		if i < concurrency-1 {
			time.Sleep(150 * time.Millisecond)
		}
	}

	wg.Wait()

	elapsed := time.Since(t0).Seconds()
	addLog("batch", fmt.Sprintf("%s: 成功%d/失败%d/共%d", batchID, okCount, failCount, len(numbers)), int(okCount))
	sendSSE(w, flusher, writeMu, SSEEvent{
		Type: "complete", OK: int(okCount), Fail: int(failCount),
		Total: len(numbers), Elapsed: fmt.Sprintf("%.1f", elapsed),
	})

	// Signal heartbeat to stop and wait for it
	close(stopHeartbeat)
	<-heartbeatDone
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, writeMu *sync.Mutex, event SSEEvent) {
	data, _ := json.Marshal(event)
	writeMu.Lock()
	defer writeMu.Unlock()

	// BUG FIX: Node wraps res.write in try/catch, Go should handle write errors
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return
	}
	flusher.Flush()
}

// ── SSE for sync monitoring ────────────────────────

func runSSEBatchFromRecords(recs []Shipment, batchPrefix, proxyAPI string, timeoutSec, concurrency int, w http.ResponseWriter, r *http.Request) {
	if len(recs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无有效记录"})
		return
	}

	numbers := make([]string, 0, len(recs))
	for _, rec := range recs {
		no := strings.TrimSpace(rec.TrackingNumber)
		if no != "" {
			numbers = append(numbers, no)
		}
	}
	if len(numbers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		jsonMarshalResponse(w, map[string]string{"error": "无有效记录"})
		return
	}

	if batchPrefix == "" || batchPrefix == "sync-" {
		batchPrefix = "sync"
	}
	batchID := batchPrefix + "-" + time.Now().Format("2006-01-02T15-04-05")
	runSSEBatch(numbers, "", proxyAPI, timeoutSec, concurrency, batchID, w, r)
}

func runSSEBatchFromIDs(ids []int64, proxyAPI string, timeoutSec, concurrency int, w http.ResponseWriter, r *http.Request) {
	var recs []Shipment
	for _, id := range ids {
		rec := getRecord(id)
		if rec != nil {
			recs = append(recs, *rec)
		}
	}
	// BUG FIX: Node uses "sync-" prefix for batch ID
	runSSEBatchFromRecords(recs, "sync", proxyAPI, timeoutSec, concurrency, w, r)
}

func runSSEBatchMonitoring(proxyAPI string, timeoutSec, concurrency int, w http.ResponseWriter, r *http.Request) {
	recs := getAllSyncRecords(100000)
	runSSEBatchFromRecords(recs, "sync-all", proxyAPI, timeoutSec, concurrency, w, r)
}

// ── JSON helpers for handlers ──────────────────────

func jsonMarshalResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	b, _ := jsonMarshal(v)
	w.Write(b)
}

func safePositiveInt(v string, fallback, maxVal int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fallback
	}
	if n > maxVal {
		return maxVal
	}
	return n
}
