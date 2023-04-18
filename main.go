package main

import (
	"github.com/vadimInshakov/marti/services"
	"go.uber.org/zap"
	"time"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	ts := services.NewTradeService()
	t := time.NewTicker(1 * time.Second)
	for range t.C {
		te, err := ts.Trade()
		if err != nil {
			logger.Error(err.Error())
		}
		if te != nil {
			logger.Info(te.String())
		}
	}
}
