package main

import (
	"context"
	"log"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/app"
	"go.uber.org/zap"
)

func main() {
	bot, err := app.NewTradingBot(config.Config{}, nil)
	if err != nil {
		log.Fatal(err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// Run the trading bot
	if err := bot.Run(context.Background(), logger); err != nil {
		log.Fatal(err)
	}
}
