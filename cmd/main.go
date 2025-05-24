package main

import (
	"context"
	"log"
	"os"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/clients"
	"go.uber.org/zap"
)

func main() {
	configs, err := config.Get()
	if err != nil {
		log.Fatal(err)
	}

	for _, config := range configs {
		var client interface{}
		switch config.Platform {
		case "binance":
			apiKey := os.Getenv("BINANCE_API_KEY")
			apiSecret := os.Getenv("BINANCE_API_SECRET")
			if apiKey == "" || apiSecret == "" {
				log.Fatal("BINANCE_API_KEY and BINANCE_API_SECRET environment variables must be set")
			}
			client = clients.NewBinanceClient(apiKey, apiSecret)
		case "bybit":
			apiKey := os.Getenv("BYBIT_API_KEY")
			apiSecret := os.Getenv("BYBIT_API_SECRET")
			if apiKey == "" || apiSecret == "" {
				log.Fatal("BYBIT_API_KEY and BYBIT_API_SECRET environment variables must be set")
			}
			client = clients.NewBybitClient(apiKey, apiSecret)
		default:
			log.Fatal("unsupported platform")
		}

		bot, err := internal.NewTradingBot(config, client)
		if err != nil {
			log.Fatal(err)
		}

		logger, _ := zap.NewProduction()
		defer logger.Sync()

		// Run the trading bot
		go func() {
			if err := bot.Run(context.Background(), logger); err != nil {
				log.Fatal(err)
			}
		}()
	}

	select {}
}
