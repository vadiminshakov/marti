package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vadiminshakov/marti/internal/domain"
)

const snapshotPollInterval = 3 * time.Second

type balanceSnapshotReader interface {
	SnapshotsAfter(index uint64) ([]entity.BalanceSnapshotRecord, error)
}

type aiDecisionReader interface {
	EventsAfter(index uint64) ([]entity.AIDecisionEventRecord, error)
}

// Server exposes HTTP endpoints serving the HTML UI and an SSE stream.
type Server struct {
	Addr            string
	Store           balanceSnapshotReader
	AIDecisionStore aiDecisionReader
}

// NewServer creates a new web server instance.
func NewServer(addr string, store balanceSnapshotReader, aiDecisionStore aiDecisionReader) *Server {
	return &Server{Addr: addr, Store: store, AIDecisionStore: aiDecisionStore}
}

// Start runs the HTTP server (blocking) and shuts it down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/balance/stream", s.handleBalanceStream)
	mux.HandleFunc("/ai/decisions/stream", s.handleAIDecisionStream)

	server := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// send a comment heartbeat every 30s so proxies keep connection
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	pollTicker := time.NewTicker(snapshotPollInterval)
	defer pollTicker.Stop()

	lastIndex := uint64(0)
	isFirstLoad := true
	sendSnapshots := func() error {
		records, err := s.Store.SnapshotsAfter(lastIndex)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}

		// Apply exponential thinning on first load for large datasets
		recordsToSend := records
		if isFirstLoad && len(records) > 100 {
			recordsToSend = thinRecords(records)
		}

		// Send records with throttling
		sendDelay := 0 * time.Millisecond
		if isFirstLoad && len(recordsToSend) > 50 {
			sendDelay = 10 * time.Millisecond
		}

		for _, record := range recordsToSend {
			payload, err := json.Marshal(record.Snapshot)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "event: balance\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			lastIndex = record.Index

			if sendDelay > 0 {
				time.Sleep(sendDelay)
			}
		}
		isFirstLoad = false
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

func (s *Server) handleAIDecisionStream(w http.ResponseWriter, r *http.Request) {
	if s.AIDecisionStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "AI decision store not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	pollTicker := time.NewTicker(snapshotPollInterval)
	defer pollTicker.Stop()

	lastIndex := uint64(0)
	sendDecisions := func() error {
		records, err := s.AIDecisionStore.EventsAfter(lastIndex)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		for _, record := range records {
			payload, err := json.Marshal(record.Event)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "event: ai_decision\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			lastIndex = record.Index
		}
		return nil
	}

	if err := sendDecisions(); err != nil {
		http.Error(w, "failed to load AI decisions", http.StatusInternalServerError)
		log.Printf("AI decision stream initial load: %v", err)
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
				log.Printf("AI decision stream poll err: %v", err)
			}
		}
	}
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.StripPrefix("/", http.FileServer(http.Dir("internal/web/static")))

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
		// double skip every 10 records (exponential)
		if (len(older) - 1 - i) % 10 == 0 {
			skip *= 2
		}
	}

	// append last 100 records as is
	return append(thinned, records[len(records)-keepLast:]...)
}