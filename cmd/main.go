// Command marti runs the cryptocurrency trading bot with DCA strategy.
// It supports multiple exchanges (Binance, Bybit, Hyperliquid) and can be configured
// via YAML configuration files or command-line arguments.
//
// Usage:
//
//	marti --config config.yaml
//	marti (uses CLI arguments)
//
// Required environment variables:
//
//	For Binance: BINANCE_API_KEY, BINANCE_API_SECRET
//	For Bybit: BYBIT_API_KEY, BYBIT_API_SECRET
//	For Hyperliquid: HYPERLIQ_PRIVATE_KEY (hex), optional HYPERLIQ_API_URL
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/dashboard"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/setup"
	"github.com/vadiminshakov/marti/internal/storage/balancesnapshots"
	"github.com/vadiminshakov/marti/internal/storage/decisions"
	"go.uber.org/zap"
)

func main() {
	var webAddr string
	var tlsDomainsArg string
	var tlsCacheDir string
	var uiFlag bool

	flag.StringVar(&webAddr, "web", ":8000", "address for web UI (disable with empty string)")
	flag.StringVar(&tlsDomainsArg, "tls-domain", "", "comma-separated list of domains for automatic TLS via ACME (e.g. Let's Encrypt); requires ports 80 and 443")
	flag.StringVar(&tlsCacheDir, "tls-cache-dir", "cert-cache", "directory to cache automatic TLS certificates")
	flag.BoolVar(&uiFlag, "ui", false, "open terminal UI configuration wizard if config is missing")
	flag.Parse()

	// check if we need to run setup ui
	configFlag := flag.Lookup("config")
	if uiFlag && (configFlag == nil || configFlag.Value.String() == "") {
		logger.Info("Starting TUI configuration wizard...")
		if err := setup.RunTUI(); err != nil {
			logger.Fatal("Setup failed", zap.Error(err))
		}

		if err := flag.Set("config", "config.gen.yaml"); err != nil {
			logger.Fatal("Failed to set config flag after setup", zap.Error(err))
		}
	}

	var tlsDomains []string
	if tlsDomainsArg != "" {
		for _, d := range strings.Split(tlsDomainsArg, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				tlsDomains = append(tlsDomains, d)
			}
		}
	}
	cfg := zap.NewProductionConfig()
	cfg.DisableStacktrace = true
	logger := zap.Must(cfg.Build())
	defer func() {
		_ = logger.Sync()
	}()

	configs, err := config.Get()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	snapshotStore, err := balancesnapshots.NewWALStore("")
	if err != nil {
		logger.Fatal("Failed to initialize balance snapshot store", zap.Error(err))
	}
	defer func() {
		if err := snapshotStore.Close(); err != nil {
			logger.Warn("Failed to close snapshot store", zap.Error(err))
		}
	}()

	decisionStore, err := decisions.NewWALStore("")
	if err != nil {
		logger.Fatal("Failed to initialize decision store", zap.Error(err))
	}
	defer func() {
		if err := decisionStore.Close(); err != nil {
			logger.Warn("Failed to close decision store", zap.Error(err))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case sig := <-sigChan:
			logger.Info("Received shutdown signal, initiating graceful shutdown...", zap.String("signal", sig.String()))
		case <-ctx.Done():
			return
		}
		cancel()
	}()

	var wg sync.WaitGroup

	for _, cfg := range configs {
		cfg := cfg
		var client any
		switch cfg.Platform {
		case "binance":
			apiKey := os.Getenv("BINANCE_API_KEY")
			apiSecret := os.Getenv("BINANCE_API_SECRET")
			if apiKey == "" || apiSecret == "" {
				logger.Fatal("Missing Binance credentials", zap.String("platform", cfg.Platform))
			}
			client = clients.NewBinanceClient(apiKey, apiSecret)
		case "bybit":
			apiKey := os.Getenv("BYBIT_API_KEY")
			apiSecret := os.Getenv("BYBIT_API_SECRET")
			if apiKey == "" || apiSecret == "" {
				logger.Fatal("Missing Bybit credentials", zap.String("platform", cfg.Platform))
			}
			client = clients.NewBybitClient(apiKey, apiSecret)
		case "simulate":
			logger.Info("Using simulation mode - no real trades will be executed",
				zap.String("pair", cfg.Pair.String()))
			client = clients.NewSimulateClient()
		case "hyperliquid":
			pk := os.Getenv("HYPERLIQ_PRIVATE_KEY")
			if pk == "" {
				logger.Fatal("Missing Hyperliquid private key", zap.String("platform", cfg.Platform))
			}
			baseURL := os.Getenv("HYPERLIQ_API_URL") // optional; defaults to mainnet
			hl, err := clients.NewHyperliquidClient(pk, baseURL)
			if err != nil {
				logger.Fatal("Failed to init Hyperliquid client", zap.Error(err))
			}
			client = hl
		default:
			logger.Fatal("Unsupported platform", zap.String("platform", cfg.Platform))
		}

		botLogger := logger.With(
			zap.String("platform", cfg.Platform),
			zap.String("pair", cfg.Pair.String()),
		)

		bot, err := internal.NewTradingBot(botLogger, cfg, client, snapshotStore, decisionStore)
		if err != nil {
			botLogger.Fatal("Failed to create trading bot", zap.Error(err))
		}

		wg.Add(1)
		go func(bot *internal.TradingBot, l *zap.Logger) {
			defer wg.Done()
			defer bot.Close()
			if err := bot.Run(ctx, l); err != nil && ctx.Err() == nil {
				l.Error("Trading bot failed", zap.Error(err))
			}
		}(bot, botLogger)
	}

	// start web server if enabled
	if webAddr != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			webLogger := logger.With(zap.String("component", "web"))
			srv := dashboard.NewServer(webAddr, snapshotStore, decisionStore)
			if len(tlsDomains) > 0 {
				webLogger.Info("Starting web UI with automatic TLS",
					zap.String("addr", webAddr),
					zap.String("domains", strings.Join(tlsDomains, ",")),
				)
				if err := srv.StartWithAutoTLS(ctx, tlsDomains, tlsCacheDir); err != nil && ctx.Err() == nil {
					webLogger.Error("Web server (automatic TLS) exited", zap.Error(err))
				}
			} else {
				webLogger.Info("Starting web UI", zap.String("addr", webAddr))
				if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
					webLogger.Error("Web server exited", zap.Error(err))
				}
			}
		}()
	}

	wg.Wait()

	logger.Info("All trading bots have shut down gracefully")
}
