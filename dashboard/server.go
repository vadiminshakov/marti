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
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vadiminshakov/marti/config"
	entity "github.com/vadiminshakov/marti/internal/domain"
	"golang.org/x/crypto/acme/autocert"
	"gopkg.in/yaml.v3"
)

const snapshotPollInterval = 3 * time.Second

type balanceSnapshotReader interface {
	SnapshotsAfter(index uint64) ([]entity.BalanceSnapshotRecord, error)
}

type decisionReader interface {
	EventsAfter(index uint64) ([]entity.DecisionEventRecord, error)
}

type TradingBotInterface interface {
	SellAll(ctx context.Context) error
	GetPair() entity.Pair
}

// Server exposes HTTP endpoints serving the HTML UI and an SSE stream.
type Server struct {
	Addr          string
	Store         balanceSnapshotReader
	DecisionStore decisionReader
	OnSetupSaved  func(configPath string) error

	mu         sync.RWMutex
	Bots       []TradingBotInterface
	NeedsSetup bool
	configPath string
}

// NewServer creates a new web server instance.
func NewServer(
	addr string,
	store balanceSnapshotReader,
	decisions decisionReader,
	bots []TradingBotInterface,
	needsSetup bool,
	configPath string,
	onSetupSaved func(configPath string) error,
) *Server {
	return &Server{
		Addr:          addr,
		Store:         store,
		DecisionStore: decisions,
		Bots:          bots,
		NeedsSetup:    needsSetup,
		configPath:    configPath,
		OnSetupSaved:  onSetupSaved,
	}
}

// SetBots updates bots used by dashboard actions (e.g. Sell All).
func (s *Server) SetBots(bots []TradingBotInterface) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Bots = bots
}

// SetNeedsSetup toggles setup state returned by setup status endpoint.
func (s *Server) SetNeedsSetup(needsSetup bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NeedsSetup = needsSetup
}

func (s *Server) getBots() []TradingBotInterface {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bots := make([]TradingBotInterface, len(s.Bots))
	copy(bots, s.Bots)

	return bots
}

func (s *Server) needsSetup() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.NeedsSetup
}

func (s *Server) activePairs() map[string]struct{} {
	bots := s.getBots()
	if len(bots) == 0 {
		return nil
	}

	pairs := make(map[string]struct{}, len(bots))
	for _, bot := range bots {
		pairs[bot.GetPair().String()] = struct{}{}
	}

	return pairs
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
	mux.HandleFunc("POST /sellall/{pair}", s.handleSellAll)
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("GET /api/setup/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/setup/config", s.handleSetupConfig)

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
	mux.HandleFunc("POST /sellall/{pair}", s.handleSellAll)
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("GET /api/setup/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/setup/config", s.handleSetupConfig)

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

		activePairs := s.activePairs()

		for _, record := range recordsToSend {
			if len(activePairs) > 0 {
				if _, ok := activePairs[record.Snapshot.Pair]; !ok {
					lastIndex = record.Index
					continue
				}
			}

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
				Type: string(record.Type), // "ai" or "averaging"
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

func (s *Server) handleSellAll(w http.ResponseWriter, r *http.Request) {
	pairStr := r.PathValue("pair")
	if pairStr == "" {
		http.Error(w, "missing pair", http.StatusBadRequest)
		return
	}

	var targetBot TradingBotInterface
	for _, bot := range s.getBots() {
		if bot.GetPair().String() == pairStr {
			targetBot = bot
			break
		}
	}

	if targetBot == nil {
		http.Error(w, "bot not found for pair: "+pairStr, http.StatusNotFound)
		return
	}

	if err := targetBot.SellAll(r.Context()); err != nil {
		http.Error(w, fmt.Errorf("sell all failed: %w", err).Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	payload := struct {
		NeedsSetup bool `json:"needsSetup"`
	}{
		NeedsSetup: s.needsSetup(),
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("setup status encode error: %v", err)
		http.Error(w, "failed to encode status", http.StatusInternalServerError)
	}
}

const secretMask = "••••••••"

type configEntry struct {
	Strategy              string `json:"strategy"`
	Platform              string `json:"platform"`
	Pair                  string `json:"pair"`
	MarketType            string `json:"marketType"`
	PollInterval          string `json:"pollInterval"`
	Amount                string `json:"amount"`
	MaxDcaTrades          string `json:"maxDcaTrades"`
	BuyThreshold         string `json:"buyThreshold"`
	SellThreshold        string `json:"sellThreshold"`
	MartingaleMultiplier string `json:"martingaleMultiplier"`
	APIURL               string `json:"apiUrl"`
	APIKey                string `json:"apiKey"`
	Model                 string `json:"model"`
	PrimaryTimeframe      string `json:"primaryTimeframe"`
	HigherTimeframe       string `json:"higherTimeframe"`
	LookbackPeriods       string `json:"lookbackPeriods"`
	HigherLookbackPeriods string `json:"higherLookbackPeriods"`
	MaxLeverage           string `json:"maxLeverage"`
	Leverage              string `json:"leverage"`
	LLMProxyURL           string `json:"llmProxyUrl"`
	TelegramBotToken      string `json:"telegramBotToken"`
	TelegramChatID        string `json:"telegramChatID"`
}

func configTmpToEntry(c config.ConfigTmp, maskSecrets bool) configEntry {
	e := configEntry{
		Strategy:              c.Strategy,
		Platform:              c.Platform,
		Pair:                  c.Pair,
		MarketType:            c.MarketTypeStr,
		PollInterval:          c.PollPriceInterval.String(),
		Amount:                c.Amount,
		MaxDcaTrades:          c.MaxDcaTradesStr,
		BuyThreshold:         c.DcaPercentThresholdBuyStr,
		SellThreshold:        c.DcaPercentThresholdSellStr,
		MartingaleMultiplier: c.MartingaleMultiplierStr,
		APIURL:               c.LLMAPIURL,
		APIKey:                c.LLMAPIKey,
		Model:                 c.Model,
		PrimaryTimeframe:      c.PrimaryTimeframe,
		HigherTimeframe:       c.HigherTimeframe,
		LookbackPeriods:       c.PrimaryLookbackPeriodsStr,
		HigherLookbackPeriods: c.HigherLookbackPeriodsStr,
		MaxLeverage:           c.MaxLeverageStr,
		Leverage:              c.LeverageStr,
		LLMProxyURL:           c.LLMProxyURL,
		TelegramBotToken:      c.TelegramBotToken,
		TelegramChatID:        c.TelegramChatID,
	}
	if maskSecrets {
		if e.APIKey != "" {
			e.APIKey = secretMask
		}
		if e.TelegramBotToken != "" {
			e.TelegramBotToken = secretMask
		}
	}
	return e
}

func readExistingConfigs(path string) []config.ConfigTmp {
	if path == "" {
		return nil
	}
	var existing []config.ConfigTmp
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}
	return existing
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	existing := readExistingConfigs(s.configPath)

	entries := make([]configEntry, 0, len(existing))
	for _, c := range existing {
		entries = append(entries, configTmpToEntry(c, true))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		log.Printf("get config encode error: %v", err)
		http.Error(w, "failed to encode config", http.StatusInternalServerError)
	}
}

func (s *Server) handleSetupConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payloads []configEntry
	if err := json.NewDecoder(r.Body).Decode(&payloads); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	defer func() {
		_ = r.Body.Close()
	}()

	if len(payloads) == 0 {
		http.Error(w, "at least one trading pair is required", http.StatusBadRequest)
		return
	}

	// Read old config to recover masked secrets.
	oldConfigs := readExistingConfigs(s.configPath)
	oldByKey := make(map[string]config.ConfigTmp, len(oldConfigs))
	for _, c := range oldConfigs {
		key := c.Pair + "|" + c.Platform + "|" + c.Strategy
		oldByKey[key] = c
	}

	var configs []config.ConfigTmp
	for i, p := range payloads {
		if p.Pair == "" {
			http.Error(w, fmt.Sprintf("pair #%d: pair cannot be empty", i+1), http.StatusBadRequest)
			return
		}
		if p.PollInterval == "" {
			http.Error(w, fmt.Sprintf("pair #%d: poll interval is required", i+1), http.StatusBadRequest)
			return
		}

		pollInterval, err := time.ParseDuration(p.PollInterval)
		if err != nil {
			http.Error(w, fmt.Sprintf("pair #%d: invalid poll interval (use values like 30s, 1m, 5m)", i+1), http.StatusBadRequest)
			return
		}

		strategy := p.Strategy
		if strategy == "" {
			strategy = "dca"
		}
		marketType := p.MarketType
		if marketType == "" {
			marketType = "spot"
		}

		cfgTmp := config.ConfigTmp{
			Platform:          p.Platform,
			Pair:              p.Pair,
			Strategy:          strategy,
			MarketTypeStr:     marketType,
			PollPriceInterval: pollInterval,
			LeverageStr:       p.Leverage,
			TelegramChatID:    p.TelegramChatID,
		}

		// Recover masked secrets from old config.
		oldKey := p.Pair + "|" + p.Platform + "|" + strategy
		if old, ok := oldByKey[oldKey]; ok {
			if p.APIKey == secretMask {
				p.APIKey = old.LLMAPIKey
			}
			if p.TelegramBotToken == secretMask {
				p.TelegramBotToken = old.TelegramBotToken
			}
		}

		switch strategy {
		case "dca":
			cfgTmp.Amount = p.Amount
			cfgTmp.MaxDcaTradesStr = p.MaxDcaTrades
			cfgTmp.DcaPercentThresholdBuyStr = p.BuyThreshold
			cfgTmp.DcaPercentThresholdSellStr = p.SellThreshold
		case "martingale":
			cfgTmp.Amount = p.Amount
			cfgTmp.MaxDcaTradesStr = p.MaxDcaTrades
			cfgTmp.DcaPercentThresholdBuyStr = p.BuyThreshold
			cfgTmp.DcaPercentThresholdSellStr = p.SellThreshold
			cfgTmp.MartingaleMultiplierStr = p.MartingaleMultiplier
		case "ai":
			cfgTmp.LLMAPIURL = p.APIURL
			cfgTmp.LLMAPIKey = p.APIKey
			cfgTmp.Model = p.Model
			cfgTmp.PrimaryTimeframe = p.PrimaryTimeframe
			cfgTmp.HigherTimeframe = p.HigherTimeframe
			cfgTmp.PrimaryLookbackPeriodsStr = p.LookbackPeriods
			cfgTmp.HigherLookbackPeriodsStr = p.HigherLookbackPeriods
			cfgTmp.MaxLeverageStr = p.MaxLeverage
			cfgTmp.LLMProxyURL = p.LLMProxyURL
			if cfgTmp.PrimaryTimeframe == "" {
				cfgTmp.PrimaryTimeframe = "3m"
			}
			if cfgTmp.HigherTimeframe == "" {
				cfgTmp.HigherTimeframe = "15m"
			}
			if cfgTmp.PrimaryLookbackPeriodsStr == "" {
				cfgTmp.PrimaryLookbackPeriodsStr = "50"
			}
			if cfgTmp.HigherLookbackPeriodsStr == "" {
				cfgTmp.HigherLookbackPeriodsStr = "60"
			}
			if marketType == "margin" && cfgTmp.MaxLeverageStr == "" {
				cfgTmp.MaxLeverageStr = "10"
			}
		default:
			http.Error(w, fmt.Sprintf("pair #%d: unsupported strategy (must be 'dca', 'martingale' or 'ai')", i+1), http.StatusBadRequest)
			return
		}

		if p.TelegramBotToken != "" && p.TelegramBotToken != secretMask {
			cfgTmp.TelegramBotToken = p.TelegramBotToken
		}
		if p.TelegramChatID != "" {
			cfgTmp.TelegramChatID = p.TelegramChatID
		}

		configs = append(configs, cfgTmp)
	}

	const filename = "config.gen.yaml"

	data, err := yaml.Marshal(configs)
	if err != nil {
		log.Printf("setup config marshal error: %v", err)
		http.Error(w, "failed to generate yaml", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(filename, data, 0o644); err != nil {
		log.Printf("setup config write error: %v", err)
		http.Error(w, "failed to save config file", http.StatusInternalServerError)
		return
	}

	if s.OnSetupSaved != nil {
		if err := s.OnSetupSaved(filename); err != nil {
			log.Printf("setup config apply error: %v", err)
			http.Error(w, "failed to apply config", http.StatusInternalServerError)
			return
		}
	}

	s.mu.Lock()
	s.configPath = filename
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","file":"config.gen.yaml"}`))
}
