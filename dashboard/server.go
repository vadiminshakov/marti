package dashboard

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	entity "github.com/vadiminshakov/marti/internal/domain"
	"golang.org/x/crypto/acme/autocert"
)

const snapshotPollInterval = 3 * time.Second

type balanceSnapshotReader interface {
	SnapshotsAfter(index uint64) ([]entity.BalanceSnapshotRecord, error)
}

type decisionReader interface {
	EventsAfter(index uint64) ([]entity.DecisionEventRecord, error)
}

// Server exposes HTTP endpoints serving the HTML UI and an SSE stream.
type Server struct {
	Addr          string
	Store         balanceSnapshotReader
	DecisionStore decisionReader
}

// NewServer creates a new web server instance.
func NewServer(addr string, store balanceSnapshotReader, decisions decisionReader) *Server {
	return &Server{Addr: addr, Store: store, DecisionStore: decisions}
}

// Start runs the HTTP server (blocking) and shuts it down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/balance/stream", s.handleBalanceStream)
	mux.HandleFunc("/decisions/stream", s.handleDecisionStream)

	server := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// StartWithAutoTLS runs an HTTPS server with automatic TLS certificates via ACME.
// It also starts an HTTP server on port 80 to handle ACME HTTP-01 challenges.
func (s *Server) StartWithAutoTLS(ctx context.Context, domains []string, cacheDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(domains) == 0 {
		return fmt.Errorf("no domains provided for automatic TLS")
	}
	if cacheDir == "" {
		cacheDir = "cert-cache"
	}

	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/balance/stream", s.handleBalanceStream)
	mux.HandleFunc("/decisions/stream", s.handleDecisionStream)

	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domains...),
		Cache:      autocert.DirCache(cacheDir),
	}

	// HTTP server on port 80 for ACME challenges and HTTP->HTTPS redirects.
	httpSrv := &http.Server{
		Addr:              ":80",
		Handler:           manager.HTTPHandler(nil),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// HTTPS server serving the dashboard with automatic certificates.
	tlsConfig := manager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12

	httpsSrv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         tlsConfig,
	}

	// shutdown both servers when context is cancelled.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http (acme) server shutdown error: %v", err)
		}
		if err := httpsSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("https server shutdown error: %v", err)
		}
	}()

	// start HTTP (ACME) server in the background.
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http (acme) server error: %v", err)
		}
	}()

	if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleBalanceStream(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "snapshot store not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// send a comment heartbeat every 20s so proxies keep connection
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	pollTicker := time.NewTicker(snapshotPollInterval)
	defer pollTicker.Stop()

	lastIndex := parseLastEventID(r.Header.Get("Last-Event-ID"), r.URL.Query().Get("last_event_id"))
	isFirstLoad := lastIndex == 0
	sendSnapshots := func() error {
		records, err := s.Store.SnapshotsAfter(lastIndex)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			if isFirstLoad {
				isFirstLoad = false
			}
			return nil
		}

		// apply exponential thinning on first load for large datasets
		recordsToSend := records
		if isFirstLoad && len(records) > 100 {
			recordsToSend = thinRecords(records)
		}

		for _, record := range recordsToSend {
			payload, err := json.Marshal(record.Snapshot)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "id: %d\n", record.Index)
			fmt.Fprintf(w, "event: balance\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			lastIndex = record.Index
		}
		if isFirstLoad {
			isFirstLoad = false
		}
		return nil
	}

	if err := sendSnapshots(); err != nil {
		http.Error(w, "failed to load snapshots", http.StatusInternalServerError)
		log.Printf("balance stream initial load: %v", err)
		return
	}

	// after initial load, if no snapshots were sent, let client know
	// so it can update UI from 'loading' to 'no data yet' state.
	if lastIndex == 0 {
		fmt.Fprintf(w, "event: no_data\n")
		fmt.Fprintf(w, "data: {}\n\n")
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-pollTicker.C:
			if err := sendSnapshots(); err != nil {
				log.Printf("balance stream poll err: %v", err)
			}
		}
	}
}

func (s *Server) handleDecisionStream(w http.ResponseWriter, r *http.Request) {
	if s.DecisionStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "decision store not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	pollTicker := time.NewTicker(snapshotPollInterval)
	defer pollTicker.Stop()

	lastIndex := parseLastEventID(r.Header.Get("Last-Event-ID"), r.URL.Query().Get("last_event_id"))
	sendDecisions := func() error {
		records, err := s.DecisionStore.EventsAfter(lastIndex)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		for _, record := range records {
			// Wrap event to include type
			wrapper := struct {
				Type string      `json:"type"`
				Data interface{} `json:"data"`
			}{
				Type: string(record.Type), // "ai" or "dca"
				Data: record.Event,
			}

			payload, err := json.Marshal(wrapper)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "id: %d\n", record.Index)
			fmt.Fprintf(w, "event: decision\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			lastIndex = record.Index
		}
		return nil
	}

	if err := sendDecisions(); err != nil {
		http.Error(w, "failed to load decisions", http.StatusInternalServerError)
		log.Printf("decision stream initial load: %v", err)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-pollTicker.C:
			if err := sendDecisions(); err != nil {
				log.Printf("decision stream poll err: %v", err)
			}
		}
	}
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.StripPrefix("/", http.FileServer(http.Dir("dashboard/static")))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetPath := r.URL.Path
		if assetPath == "" || assetPath == "/" {
			assetPath = "/index.html"
		}

		if !shouldCompress(assetPath) || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			fileServer.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")

		gz := gzip.NewWriter(w)
		defer gz.Close()

		gzw := &gzipResponseWriter{ResponseWriter: w, writer: gz}
		fileServer.ServeHTTP(gzw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.writer.Write(b)
}

func shouldCompress(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	if ext == "" {
		return true
	}
	switch ext {
	case ".html", ".css", ".js", ".json", ".svg", ".txt":
		return true
	default:
		return false
	}
}

// parseLastEventID extracts an SSE event ID from either the Last-Event-ID header or a query parameter.
// The header is preferred; the query parameter allows manual reconnects to resume from a known index.
func parseLastEventID(headerVal, queryVal string) uint64 {
	idStr := strings.TrimSpace(headerVal)
	if idStr == "" {
		idStr = strings.TrimSpace(queryVal)
	}
	if idStr == "" {
		return 0
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		log.Printf("invalid last event id %q: %v", idStr, err)
		return 0
	}
	return id
}

// thinRecords applies exponential thinning to keep last 100 records fully, thin the rest
func thinRecords(records []entity.BalanceSnapshotRecord) []entity.BalanceSnapshotRecord {
	if len(records) <= 100 {
		return records
	}

	keepLast := 100
	older := records[:len(records)-keepLast]
	var thinned []entity.BalanceSnapshotRecord

	// exponentially thin older records
	skip := 1 // start by skipping 1 (send every 2nd)
	for i := len(older) - 1; i >= 0; i-- {
		thinned = append([]entity.BalanceSnapshotRecord{older[i]}, thinned...)
		// skip next 'skip' records
		i -= skip
		// double skip every 12 records (exponential)
		if (len(older)-1-i)%12 == 0 {
			skip *= 2
		}
	}

	// append last 100 records as is
	return append(thinned, records[len(records)-keepLast:]...)
}
