// Command marti runs the cryptocurrency trading bot with DCA strategy.
// It supports multiple exchanges (Binance, Bybit) and can be configured
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
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/events"
	"github.com/vadiminshakov/marti/internal/web"
	"go.uber.org/zap"
)

func main() {
	var webAddr string
	flag.StringVar(&webAddr, "web", ":8080", "address for web UI (disable with empty string)")
	flag.Parse()
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
		default:
			logger.Fatal("Unsupported platform", zap.String("platform", cfg.Platform))
		}

		botLogger := logger.With(
			zap.String("platform", cfg.Platform),
			zap.String("pair", cfg.Pair.String()),
		)

		bot, err := internal.NewTradingBot(botLogger, cfg, client)
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
			webLogger.Info("Starting web UI", zap.String("addr", webAddr))
			srv := web.NewServer(webAddr, events.DefaultBalanceBroadcaster)
			if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
				webLogger.Error("Web server exited", zap.Error(err))
			}
		}()
	}

	wg.Wait()

	logger.Info("All trading bots have shut down gracefully")
}
