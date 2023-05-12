package main

import (
	"context"
	"fmt"
	"github.com/vadimInshakov/marti/config"
	"time"

	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/services/windowfinder"

	"github.com/adshao/go-binance/v2"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	recreateInterval   = 26 * time.Hour
	pollPricesInterval = 3 * time.Second
	restartWaitSec     = 30
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	pair, klineSize, koeff, usebalance, minwindow, err := config.Get()
	if err != nil {
		logger.Fatal("failed to get configuration", zap.Error(err))
	}

	binanceClient := binance.NewClient(apikey, secretkey)

	// TODO make anomaly detector

	//apikey := os.Getenv("APIKEY")
	//if len(apikey) == 0 {
	//	logger.Fatal("APIKEY env is not set")
	//}
	//require.NotEmpty(t, apikey, "APIKEY env is not set")
	//secretkey := os.Getenv("SECRETKEY")
	//if len(apikey) == 0 {
	//	logger.Fatal("SECRETKEY env is not set")
	//}

	g := new(errgroup.Group)

	g.Go(func() error {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), recreateInterval)
			wf := windowfinder.NewBinanceWindowFinder(binanceClient, minwindow, pair, klineSize, koeff)
			fn, err := binanceTradeServiceCreator(logger, wf, binanceClient, pair, usebalance)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to create binance trader service, recreate instance after %ds", restartWaitSec*2), zap.Error(err))
				time.Sleep(restartWaitSec * 2 * time.Second)
				continue
			}

			go timer(ctx, recreateInterval)
			if err := fn(ctx); err != nil {
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					logger.Info("recreate instance")
					continue
				}
				logger.Error(fmt.Sprintf("error, recreate instance after %ds", restartWaitSec), zap.Error(err))
				time.Sleep(restartWaitSec * time.Second)

				continue
			}
		}
	})

	if err := g.Wait(); err != nil {
		logger.Error(err.Error())
	}
}

func timer(ctx context.Context, recreateInterval time.Duration) {
	startpoint := time.Now()
	endpoint := startpoint.Add(recreateInterval)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			remain := endpoint.Sub(time.Now())
			fmt.Printf("%.0fs remaining before rebalance", remain.Seconds())
			fmt.Print("\r")
			time.Sleep(1 * time.Second)
		}
	}
}
