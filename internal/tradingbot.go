// Package internal contains the core trading bot implementation and supporting infrastructure.
package internal

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/config"

	"github.com/vadiminshakov/marti/internal/entity"
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
	trader          traderService
	pricer          Pricer
	leverage        int
	// balanceStore persists snapshots for UI/recovery.
	balanceStore balanceSnapshotWriter
}

type balanceSnapshotWriter interface {
	Save(snapshot entity.BalanceSnapshot) error
}

// NewTradingBot creates a new trading bot instance with the specified configuration, exchange client, and logger.
// It initializes the appropriate trader and pricer components based on the platform specified in the config,
// and sets up the trading strategy with the provided parameters.
func NewTradingBot(logger *zap.Logger, conf config.Config, client any, balanceStore balanceSnapshotWriter) (*TradingBot, error) {
	// for AI strategy, use MaxLeverage; for DCA strategy, use Leverage
	leverage := conf.Leverage
	if conf.StrategyType == "ai" {
		leverage = conf.MaxLeverage
	}

	currentTrader, currentPricer, err := createTraderAndPricer(conf.Platform, conf.Pair, conf.MarketType, leverage, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create trader and pricer")
	}

	if logger == nil {
		logger = zap.L()
	}

	tradingStrategy, err := createTradingStrategy(
		logger,
		conf,
		currentPricer,
		currentTrader,
		client,
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
	// initialize trading strategy (handles initial buy if needed)
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
			return ctx.Err()
		case <-ticker.C:
			tradeEvent, err := b.tradingStrategy.Trade(ctx)
			if err != nil {
				logger.Error("Trading strategy failed", zap.Error(err))
				continue
			}

			if tradeEvent != nil {
				logger.Info("Trade event occurred", zap.Any("event", tradeEvent))
				go func() {
					if err := b.publishBalanceSnapshot(ctx); err != nil {
						logger.Debug("balance snapshot skipped", zap.Error(err))
					}
				}()
			}
		}
	}
}

func (b *TradingBot) streamBalances(ctx context.Context, logger *zap.Logger) {
	interval := b.Config.PollPriceInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := b.publishBalanceSnapshot(ctx); err != nil {
		logger.Debug("balance snapshot skipped", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.publishBalanceSnapshot(ctx); err != nil {
				logger.Debug("balance snapshot skipped", zap.Error(err))
			}
		}
	}
}

func (b *TradingBot) publishBalanceSnapshot(ctx context.Context) error {

	base, err := b.trader.GetBalance(ctx, b.Config.Pair.From)
	if err != nil {
		return errors.Wrapf(err, "get %s balance", b.Config.Pair.From)
	}
	quote, err := b.trader.GetBalance(ctx, b.Config.Pair.To)
	if err != nil {
		return errors.Wrapf(err, "get %s balance", b.Config.Pair.To)
	}
	price, err := b.pricer.GetPrice(ctx, b.Config.Pair)
	if err != nil {
		return errors.Wrap(err, "get price for balance snapshot")
	}

	total := quote.Add(base.Mul(price))
	if b.Config.MarketType == entity.MarketTypeMargin {
		position, err := b.trader.GetPosition(ctx, b.Config.Pair)
		if err != nil {
			return errors.Wrap(err, "get position for balance snapshot")
		}
		if position != nil && position.Amount.GreaterThan(decimal.Zero) {
			lev := b.leverage
			if lev < 1 {
				lev = 1
			}
			if lev > 1 {
				notional := position.Amount.Abs().Mul(position.EntryPrice)
				collateral := notional.Div(decimal.NewFromInt(int64(lev)))
				pnl := position.PnL(price)

				freeBase := base
				switch position.Side {
				case entity.PositionSideLong:
					if base.GreaterThanOrEqual(position.Amount) {
						freeBase = base.Sub(position.Amount)
					}
				case entity.PositionSideShort:
					shortImpact := position.Amount.Neg()
					if base.LessThanOrEqual(shortImpact) {
						freeBase = base.Sub(shortImpact)
					}
				}
				freeBaseValue := decimal.Zero
				if freeBase.GreaterThan(decimal.Zero) {
					freeBaseValue = freeBase.Mul(price)
				}
				total = quote.Add(freeBaseValue).Add(collateral).Add(pnl)
			}
		}
	}

	return b.balanceStore.Save(entity.BalanceSnapshot{
		Timestamp:  time.Now().UTC(),
		Pair:       b.Config.Pair.String(),
		Base:       base.String(),
		Quote:      quote.String(),
		TotalQuote: total.StringFixed(2),
		Price:      price.String(),
	})
}
