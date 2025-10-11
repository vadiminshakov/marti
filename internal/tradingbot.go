// Package internal contains the core trading bot implementation and supporting infrastructure.
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

// TradingStrategy defines the interface that all trading strategies must implement.
// It provides lifecycle methods for initializing, executing trades, and cleanup.
type TradingStrategy interface {
	// Initialize prepares the trading strategy for operation
	Initialize(ctx context.Context) error
	// Trade executes the trading logic and returns a trade event if a trade should be made
	Trade(ctx context.Context) (*entity.TradeEvent, error)
	// Close performs cleanup operations when the strategy is shut down
	Close() error
}

// TradingBot represents a single trading instance that manages the execution
// of a specific trading strategy on a configured trading pair.
type TradingBot struct {
	// Config contains the trading configuration parameters
	Config config.Config
	// tradingStrategy holds the strategy implementation to be executed
	tradingStrategy TradingStrategy
}

// NewTradingBot creates a new trading bot instance with the specified configuration and exchange client.
// It initializes the appropriate trader and pricer components based on the platform specified in the config,
// and sets up the trading strategy with the provided parameters.
func NewTradingBot(conf config.Config, client any) (*TradingBot, error) {
	currentTrader, currentPricer, err := createTraderAndPricer(conf.Platform, conf.Pair, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trader and pricer")
	}

	tsLogger := zap.L().With(zap.String("pair", conf.Pair.String()))
	tradingStrategy, err := createTradingStrategy(
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
		return nil, errors.Wrap(err, "failed to create trading strategy")
	}

	return &TradingBot{
		Config:          conf,
		tradingStrategy: tradingStrategy,
	}, nil
}

// Close shuts down the trading bot by closing the underlying trading strategy.
// This method should be called when the bot is no longer needed to free up resources.
func (b *TradingBot) Close() {
	b.tradingStrategy.Close()
}

// Run starts the main trading loop that continuously monitors prices and executes trades.
// It initializes the trading strategy, then runs a ticker-based loop that calls the strategy's
// Trade method at regular intervals defined by PollPriceInterval.
// The method blocks until the context is cancelled or an unrecoverable error occurs.
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
