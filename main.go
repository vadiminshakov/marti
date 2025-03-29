package main

import (
	"context"
	"fmt"
	"github.com/hirokisan/bybit/v2"
	"github.com/vadiminshakov/marti/config"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/services/channel"

	"github.com/adshao/go-binance/v2"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	restartWaitSec = 30

	platform = "binance"
)

func main() {
	apikey := os.Getenv("APIKEY")
	if len(apikey) == 0 {
		log.Fatal("APIKEY env is not set")
	}

	secretKey := os.Getenv("SECRETKEY")
	if len(apikey) == 0 {
		log.Fatal("SECRETKEY env is not set")
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	configs, err := config.Get()
	if err != nil {
		logger.Fatal("failed to get configuration", zap.Error(err))
	}

	g := new(errgroup.Group)
	var timerStarted atomic.Bool
	timerStarted.Store(false)
	for _, conf := range configs {
		g.Go(func() error {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), conf.RebalanceInterval)
				go timer(ctx, conf.RebalanceInterval, &timerStarted)

				executor := func(context.Context) error { return nil }

				if platform == "binance" {
					binanceClient := binance.NewClient(apikey, secretKey)
					cf := channel.NewBinanceChannelFinder(binanceClient, conf.Pair, conf.StatHours)
					executor, err = binanceTradeServiceCreator(logger, cf, binanceClient, conf.Pair, conf.Usebalance, conf.PollPriceInterval)
					if err != nil {
						logger.Error(fmt.Sprintf("failed to create binance trader service for pair %s, recreate instance after %ds", conf.Pair.String(),
							restartWaitSec*2), zap.Error(err))
						time.Sleep(restartWaitSec * 2 * time.Second)
						continue
					}
				}

				if platform == "bybit" {
					bybitClient := bybit.NewClient().WithAuth(apikey, secretKey)

					cf := channel.NewBybitChannelFinder(bybitClient, conf.Pair, conf.StatHours)

					executor = func(context.Context) error {
						buyprice, channel, err := cf.GetTradingChannel()
						if err != nil {
							return errors.Wrapf(err, "failed to find window for %s", conf.Pair.String())
						}

						fmt.Printf("buyprice: %v, channel: %v\n", buyprice, channel)
						select {}

						return nil
					}
				}

				if err = executor(ctx); err != nil {
					cancel()
					if errors.Is(err, context.DeadlineExceeded) {
						logger.Info("recreate instance", zap.String("pair", conf.Pair.String()))
						continue
					}
					logger.Error(fmt.Sprintf("error, recreate instance for pair %s after %ds", conf.Pair.String(), restartWaitSec), zap.Error(err))
					time.Sleep(restartWaitSec * time.Second)

					continue
				}
			}
		})
		logger.Info("started", zap.String("pair", conf.Pair.String()))
	}

	if err := g.Wait(); err != nil {
		logger.Error(err.Error())
	}
}

// timer prints remaining time before rebalance.
func timer(ctx context.Context, recreateInterval time.Duration, timerStarted *atomic.Bool) {
	if swapped := timerStarted.CompareAndSwap(false, true); !swapped {
		return
	}
	startpoint := time.Now()
	endpoint := startpoint.Add(recreateInterval)
	for {
		select {
		case <-ctx.Done():
			timerStarted.CompareAndSwap(true, false)
			return
		default:
			remain := endpoint.Sub(time.Now())
			fmt.Printf("%.0fs remaining before rebalance", remain.Seconds())
			fmt.Print("\r")
			time.Sleep(1 * time.Second)
		}
	}
}
