package internal

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/config"

	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/strategy"
	"go.uber.org/zap"
)

type TradingStrategy interface {
	Initialize(ctx context.Context) error
	Trade(ctx context.Context) (*entity.TradeEvent, error)
	Close() error
}

// TradingBot represents a single trading instance
type TradingBot struct {
	Trader          Trader
	Pricer          Pricer
	Config          config.Config
	tradingStrategy TradingStrategy
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(conf config.Config, client any) (*TradingBot, error) {
	currentTrader, currentPricer, err := createTraderAndPricer(conf.Platform, conf.Pair, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trader and pricer")
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
	// Initialize trading strategy (handles initial buy if needed)
	if err := b.tradingStrategy.Initialize(ctx); err != nil {
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
			tradeEvent, err := b.tradingStrategy.Trade(ctx)
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
