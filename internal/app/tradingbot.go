package app

import (
	"context"
	"fmt"
	"time"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/app/entity"
	"github.com/vadiminshakov/marti/internal/app/services/channel"
	"github.com/vadiminshakov/marti/internal/app/services/detector"
	"github.com/vadiminshakov/marti/internal/app/services/pricer"
	"github.com/vadiminshakov/marti/internal/app/services/trader"
	"go.uber.org/zap"
	"github.com/adshao/go-binance/v2"
	"github.com/hirokisan/bybit/v2"
)

// TradingBot represents a single trading instance
type TradingBot struct {
	ChannelFinder channel.ChannelFinder
	Trader        trader.Trader
	Detector      detector.Detector
	Pricer        pricer.Pricer
	Config        config.Config
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(conf config.Config, client interface{}) (*TradingBot, error) {
	switch conf.Platform {
	case "binance":
		binanceClient := client.(*binance.Client)
		return &TradingBot{
			ChannelFinder: channel.NewBinanceChannelFinder(binanceClient, conf.Pair, uint64(conf.StatHours)),
			// Handle error from NewBinanceTrader
			Trader: func() trader.Trader {
				t, err := trader.NewBinanceTrader(binanceClient, conf.Pair)
				if err != nil {
					// Log and return nil or handle error as appropriate for your application
					zap.L().Error("Failed to create BinanceTrader", zap.Error(err))
					return nil
				}
				return t
			}(),
			// Handle error from NewBinanceDetector
			Detector: func() detector.Detector {
				d, err := detector.NewBinanceDetector(binanceClient, conf.Usebalance, conf.Pair, decimal.Zero, decimal.Zero)
				if err != nil {
					zap.L().Error("Failed to create BinanceDetector", zap.Error(err))
					return nil
				}
				return d
			}(),
			Pricer:        pricer.NewBinancePricer(binanceClient),
			Config:        conf,
		}, nil
	case "bybit":
		bybitClient := client.(*bybit.Client)
		return &TradingBot{
			ChannelFinder: channel.NewBybitChannelFinder(bybitClient, conf.Pair, uint64(conf.StatHours)),
			// Handle error from NewBybitTrader
			Trader: func() trader.Trader {
				t, err := trader.NewBybitTrader(bybitClient, conf.Pair)
				if err != nil {
					// Log and return nil or handle error as appropriate for your application
					zap.L().Error("Failed to create BybitTrader", zap.Error(err))
					return nil
				}
				return t
			}(),
			// Handle error from NewBybitDetector
			Detector: func() detector.Detector {
				d, err := detector.NewBybitDetector(bybitClient, conf.Usebalance, conf.Pair, decimal.Zero, decimal.Zero)
				if err != nil {
					zap.L().Error("Failed to create BybitDetector", zap.Error(err))
					return nil
				}
				return d
			}(),
			Pricer:        pricer.NewBybitPricer(bybitClient),
			Config:        conf,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", conf.Platform)
	}
}

// Run executes the trading bot
func (b *TradingBot) Run(ctx context.Context, logger *zap.Logger) error {
	buyprice, channel, err := b.ChannelFinder.GetTradingChannel()
	if err != nil {
		return errors.Wrapf(err, "failed to find window for %s", b.Config.Pair.String())
	}

	logger.Info("trading channel found",
		zap.String("pair", b.Config.Pair.String()),
		zap.String("buyprice", buyprice.String()),
		zap.Any("channel", channel))

	ticker := time.NewTicker(b.Config.PollPriceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			price, err := b.Pricer.GetPrice(b.Config.Pair)
			if err != nil {
				logger.Error("failed to get price", zap.Error(err))
				continue
			}

			action, err := b.Detector.NeedAction(price)
			if err != nil {
				logger.Error("failed to detect action", zap.Error(err))
				continue
			}

			switch action {
			case entity.ActionBuy:
				if err := b.Trader.Buy(b.Config.Usebalance); err != nil {
					logger.Error("failed to execute buy order", zap.Error(err))
				} else {
					logger.Info("buy order executed",
						zap.String("pair", b.Config.Pair.String()))
				}
			case entity.ActionSell:
				if err := b.Trader.Sell(b.Config.Usebalance); err != nil {
					logger.Error("failed to execute sell order", zap.Error(err))
				} else {
					logger.Info("sell order executed",
						zap.String("pair", b.Config.Pair.String()))
				}
			}
		}
	}
}
