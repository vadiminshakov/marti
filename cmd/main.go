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
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/clients"
	"go.uber.org/zap"
)

func main() {
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

	go func() {
		<-sigChan
		logger.Info("Received shutdown signal, initiating graceful shutdown...")
		cancel()
	}()

	var wg sync.WaitGroup

	for _, cfg := range configs {
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

		wg.Go(func() {
			defer bot.Close()
		
			if err := bot.Run(ctx, botLogger); err != nil {
				botLogger.Error("Trading bot failed", zap.Error(err))
			}
		})
	}

	wg.Wait()

	signal.Stop(sigChan)
	logger.Info("All trading bots have shut down gracefully")
}
