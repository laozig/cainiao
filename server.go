package main

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed public
var embeddedPublic embed.FS

// ── JSON marshal helper (shared across packages) ────

func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// ── Main ────────────────────────────────────────────

func main() {
	// Init DB
	initDB()
	log.Println("DB initialized")

	// Recalc all status on startup
	if n := recalcAllStatus(); n > 0 {
		log.Printf("Recalculated status for %d records", n)
	}

	// Create mux
	mux := http.NewServeMux()

	// ── API routes ──────────────────────────────────
	// IMPORTANT: Go ServeMux uses longest-prefix matching.
	// /api/records/batch-remarks and /api/records/batch-tags
	// MUST be registered BEFORE /api/records/ to avoid being caught by the catch-all.

	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			handleGetSettings(w, r)
		case "PUT":
			handleUpdateSettings(w, r)
		default:
			w.WriteHeader(405)
		}
	})

	mux.HandleFunc("/api/query", postOnly(handleQuery))
	mux.HandleFunc("/api/import", postOnly(handleImport))

	// BUG FIX: Register specific sub-routes BEFORE the catch-all /api/records/
	mux.HandleFunc("/api/records/batch-remarks", putOnly(handleBatchRemarks))
	mux.HandleFunc("/api/records/batch-tags", putOnly(handleBatchTags))

	mux.HandleFunc("/api/records", recordsRouter())
	mux.HandleFunc("/api/records/", recordsDetailRouter())

	mux.HandleFunc("/api/stats", getOnly(handleGetStats))
	mux.HandleFunc("/api/carriers", getOnly(handleGetCarriers))
	mux.HandleFunc("/api/detect-carrier", postOnly(handleDetectCarrier))
	mux.HandleFunc("/api/carrier-rules", getOnly(handleCarrierRules))
	mux.HandleFunc("/api/check-duplicates", postOnly(handleCheckDuplicates))
	mux.HandleFunc("/api/parse-excel", postOnly(handleParseExcel))
	mux.HandleFunc("/api/sync", postOnly(handleSync))
	mux.HandleFunc("/api/sync/filter", postOnly(handleSyncFilter))
	mux.HandleFunc("/api/sync/monitoring", postOnly(handleSyncMonitoring))
	mux.HandleFunc("/api/dashboard", getOnly(handleDashboard))
	mux.HandleFunc("/api/tags", getOnly(handleGetTags))
	mux.HandleFunc("/api/logs", getOnly(handleGetLogs))

	// ── Static files (embedded) ──────────────────────
	publicFS, _ := fs.Sub(embeddedPublic, "public")
	fileServer := http.FileServer(http.FS(publicFS))
	mux.Handle("/", cacheStatic(fileServer))

	// ── Apply middleware ────────────────────────────
	handler := applyMiddleware(mux)

	// ── Start server with graceful shutdown ─────────
	port := getSetting("port", "3456")
	addr := ":" + port
	log.Printf("物流监控平台已启动: http://localhost%s", addr)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // long for SSE
		IdleTimeout:  120 * time.Second,
	}

	// OPT #6: Graceful shutdown on SIGINT/SIGTERM
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("收到信号 %v，正在关闭服务器...", sig)
		server.Close()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("Server error:", err)
	}
	log.Println("服务器已关闭")
}

// ── Middleware ──────────────────────────────────────

func applyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// OPT #5: CORS - restrict to localhost in production
		allowedOrigin := "*"
		if origin := r.Header.Get("Origin"); origin != "" {
			if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
				allowedOrigin = origin
			}
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		// Rate limiting for API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !apiLimiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(429)
				jsonMarshalResponse(w, map[string]string{"error": "请求过于频繁，请稍后再试"})
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/import") || strings.HasPrefix(r.URL.Path, "/api/sync") {
				if !importLimiter.Allow() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(429)
					jsonMarshalResponse(w, map[string]string{"error": "批量导入过于频繁，请稍后再试"})
					return
				}
			}
		}

		// Recovery
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, rec)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(500)
				jsonMarshalResponse(w, map[string]string{"error": "服务器内部错误"})
			}
		}()

		// OPT #7: Gzip compression for API responses
		if strings.HasPrefix(r.URL.Path, "/api/") && !isStreamRoute(r.URL.Path) && acceptsGzip(r) {
			gw := &gzipResponseWriter{ResponseWriter: w, writer: nil}
			w.Header().Set("Content-Encoding", "gzip")
			next.ServeHTTP(gw, r)
			if gw.writer != nil {
				gw.writer.Close()
			}
		} else {
			next.ServeHTTP(w, r)
		}

		// OPT #9: Access log
		duration := time.Since(start)
		log.Printf("[%s] %s %s %v", r.Method, r.URL.Path, duration.Round(time.Millisecond), r.RemoteAddr)
	})
}

// ── Static and gzip helpers ─────────────────────────

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		next.ServeHTTP(w, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

func isStreamRoute(path string) bool {
	return path == "/api/import" || strings.HasPrefix(path, "/api/sync")
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.writer == nil {
		w.writer = gzip.NewWriter(w.ResponseWriter)
	}
	return w.writer.Write(b)
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Flush() {
	if w.writer != nil {
		w.writer.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Ensure gzipResponseWriter implements http.Flusher for SSE
var _ http.Flusher = (*gzipResponseWriter)(nil)

// Ensure gzipResponseWriter implements io.Closer
var _ io.Closer = (*gzipResponseWriter)(nil)

func (w *gzipResponseWriter) Close() error {
	if w.writer != nil {
		return w.writer.Close()
	}
	return nil
}

// ── Route helpers ──────────────────────────────────

func getOnly(h http.HandlerFunc) http.HandlerFunc {
	return methodOnly("GET", h)
}

func postOnly(h http.HandlerFunc) http.HandlerFunc {
	return methodOnly("POST", h)
}

func putOnly(h http.HandlerFunc) http.HandlerFunc {
	return methodOnly("PUT", h)
}

func methodOnly(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(405)
			jsonMarshalResponse(w, map[string]string{"error": "method not allowed"})
			return
		}
		handler(w, r)
	}
}

// recordsRouter handles /api/records (GET list, DELETE batch)
func recordsRouter() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			handleGetRecords(w, r)
		case "DELETE":
			handleDeleteRecords(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(405)
			jsonMarshalResponse(w, map[string]string{"error": "method not allowed"})
		}
	}
}

// recordsDetailRouter handles /api/records/{id}/remarks and /api/records/{id}
// NOTE: /api/records/batch-remarks and /api/records/batch-tags are handled
// by their own handlers registered above, so they won't reach here.
func recordsDetailRouter() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasSuffix(path, "/remarks") {
			if r.Method == "PUT" {
				handleUpdateRemarks(w, r)
			} else {
				w.WriteHeader(405)
			}
			return
		}

		if r.Method == "GET" {
			handleGetRecord(w, r)
		} else {
			w.WriteHeader(405)
		}
	}
}
