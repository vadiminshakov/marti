package internal

import (
	"context"
	"fmt"
	"time"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/config"

	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/strategy"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

type tradersvc interface {
	Buy(amount decimal.Decimal) error
	Sell(amount decimal.Decimal) error
}

type pricersvc interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

type TradingStrategy interface {
	Initialize() error
	Trade() (*entity.TradeEvent, error)
	Close() error
}

// TradingBot represents a single trading instance
type TradingBot struct {
	Trader          tradersvc
	Pricer          pricersvc
	Config          config.Config
	tradingStrategy TradingStrategy
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(conf config.Config, client any) (*TradingBot, error) {
	var currentTrader tradersvc
	var currentPricer pricersvc
	var err error

	switch conf.Platform {
	case "binance":
		binanceClient, ok := client.(*binance.Client)
		if !ok || binanceClient == nil {
			return nil, fmt.Errorf("binance platform expects *binance.Client, got %T", client)
		}
		currentTrader, err = trader.NewBinanceTrader(binanceClient, conf.Pair)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create BinanceTrader")
		}
		currentPricer = pricer.NewBinancePricer(binanceClient)
	case "bybit":
		bybitClient, ok := client.(*bybit.Client)
		if !ok || bybitClient == nil {
			return nil, fmt.Errorf("bybit platform expects *bybit.Client, got %T", client)
		}
		currentTrader, err = trader.NewBybitTrader(bybitClient, conf.Pair)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create BybitTrader")
		}
		currentPricer = pricer.NewBybitPricer(bybitClient)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", conf.Platform)
	}

	tsLogger := zap.L().With(zap.String("pair", conf.Pair.String()))
	tradingStrategy, err := strategy.NewDCAStrategy(
		tsLogger,
		conf.Pair,
		conf.Amount,
		currentPricer,
		currentTrader,
		conf.MaxDcaTrades,
		conf.DcaPercentThresholdBuy,
		conf.DcaPercentThresholdSell,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create DCAStrategy")
	}

	return &TradingBot{
		Trader:          currentTrader,
		Pricer:          currentPricer,
		Config:          conf,
		tradingStrategy: tradingStrategy,
	}, nil
}

// Close closes the trading bot
func (b *TradingBot) Close() {
	b.tradingStrategy.Close()
}

// Run executes the trading bot
func (b *TradingBot) Run(ctx context.Context, logger *zap.Logger) error {
	defer b.tradingStrategy.Close()

	// Initialize trading strategy (handles initial buy if needed)
	if err := b.tradingStrategy.Initialize(); err != nil {
		return errors.Wrap(err, "failed to initialize trading strategy")
	}

	ticker := time.NewTicker(b.Config.PollPriceInterval)
	defer ticker.Stop()

	logger.Info("Starting trading loop", zap.String("pair", b.Config.Pair.String()), zap.Duration("poll_interval", b.Config.PollPriceInterval))

	for {
		select {
		case <-ctx.Done():
			logger.Info("Context done, stopping trading bot run loop.", zap.String("pair", b.Config.Pair.String()))
			return ctx.Err()
		case <-ticker.C:
			logger.Debug("Trade service tick", zap.String("pair", b.Config.Pair.String()))
			tradeEvent, err := b.tradingStrategy.Trade()
			if err != nil {
				if errors.Is(err, strategy.ErrNoData) {
					logger.Debug("Trading strategy returned no data, continuing", zap.String("pair", b.Config.Pair.String()))
				} else {
					logger.Error("Trading strategy failed", zap.String("pair", b.Config.Pair.String()), zap.Error(err))
				}
				continue
			}

			if tradeEvent != nil {
				logger.Info("Trade event occurred", zap.String("pair", b.Config.Pair.String()), zap.Any("event", tradeEvent))
			}
		}
	}
}
