// Package internal contains the core trading bot implementation and supporting infrastructure.
package internal

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/config"
	entity "github.com/vadiminshakov/marti/internal/domain"
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

// DcaCostBasisProvider is an optional interface that DCA strategies implement
// to provide cost basis for PnL calculation.
type DcaCostBasisProvider interface {
	// GetDcaCostBasis returns average entry price and amount for PnL calculation.
	// Returns zeros if no position is open.
	GetDcaCostBasis() (entryPrice, amount decimal.Decimal)
}

// TradingBot represents a single trading instance that manages the execution
// of a specific trading strategy on a configured trading pair.
type TradingBot struct {
	tradingStrategy TradingStrategy
	trader          traderService
	pricer          priceService
	balanceStore    balanceSnapshotWriter
	Config          config.Config
	leverage        int
}

type balanceSnapshotWriter interface {
	Save(snapshot entity.BalanceSnapshot) error
}

type decisionStore interface {
	SaveAI(event entity.AIDecisionEvent) error
	SaveDCA(event entity.DCADecisionEvent) error
}

// NewTradingBot creates a new trading bot instance with the specified configuration, exchange client, and logger.
// It initializes the appropriate trader and pricer components based on the platform specified in the config,
// and sets up the trading strategy with the provided parameters.
func NewTradingBot(logger *zap.Logger, conf config.Config, client any, balanceStore balanceSnapshotWriter, decisions decisionStore) (*TradingBot, error) {
	provider, err := newServiceProvider(client, logger)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create service provider")
	}

	// for AI strategy, use MaxLeverage; for DCA strategy, use Leverage
	leverage := conf.Leverage
	if conf.StrategyType == "ai" {
		leverage = conf.MaxLeverage
	}

	stateKey := ""
	if conf.Platform == "simulate" {
		stateKey = conf.SimulationStateKey()
	}

	currentTrader, err := provider.Trader(conf.Pair, conf.MarketType, leverage, stateKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trader")
	}

	currentPricer, err := provider.Pricer()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create pricer")
	}

	var currentKlineProvider klineService
	if conf.StrategyType == "ai" {
		currentKlineProvider, err = provider.KlineProvider()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create kline provider")
		}
	}

	factory := newStrategyFactory(logger)

	tradingStrategy, err := factory.createTradingStrategy(
		conf,
		currentPricer,
		currentTrader,
		currentKlineProvider,
		decisions,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trading strategy")
	}

	return &TradingBot{
		Config:          conf,
		tradingStrategy: tradingStrategy,
		trader:          currentTrader,
		pricer:          currentPricer,
		leverage:        leverage,
		balanceStore:    balanceStore,
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
	if err := b.tradingStrategy.Initialize(ctx); err != nil {
		return errors.Wrap(err, "failed to initialize trading strategy")
	}

	go b.streamBalances(ctx, logger.With(zap.String("component", "balance-reporter")))

	ticker := time.NewTicker(b.Config.PollPriceInterval)
	defer ticker.Stop()

	logger.Info("Starting trading loop", zap.Duration("poll_interval", b.Config.PollPriceInterval))

	for {
		select {
		case <-ctx.Done():
			logger.Info("Context done, stopping trading bot run loop")

			return errors.Wrap(ctx.Err(), "context done")
		case <-ticker.C:
			tradeEvent, err := b.tradingStrategy.Trade(ctx)
			if err != nil {
				logger.Error("Trading strategy failed", zap.Error(err))

				continue
			}

			if tradeEvent == nil {
				continue
			}

			logger.Info("Trade event occurred", zap.Any("event", tradeEvent))

			go func() {
				if err := b.publishBalanceSnapshot(ctx); err != nil {
					logger.Debug("balance snapshot skipped", zap.Error(err))
				}
			}()
		}
	}
}


