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

	// "github.com/vadiminshakov/marti/internal/services/channel"  // Removed
	// "github.com/vadiminshakov/marti/internal/services/detector" // Removed
	"github.com/vadiminshakov/marti/internal/services"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

// TradingBot represents a single trading instance
type TradingBot struct {
	Trader       trader.Trader
	Pricer       pricer.Pricer
	Config       config.Config
	tradeService *services.TradeService
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(conf config.Config, client interface{}) (*TradingBot, error) {
	var currentTrader trader.Trader
	var currentPricer pricer.Pricer
	var err error

	switch conf.Platform {
	case "binance":
		binanceClient := client.(*binance.Client)
		currentTrader, err = trader.NewBinanceTrader(binanceClient, conf.Pair)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create BinanceTrader")
		}
		currentPricer = pricer.NewBinancePricer(binanceClient)
	case "bybit":
		bybitClient := client.(*bybit.Client)
		currentTrader, err = trader.NewBybitTrader(bybitClient, conf.Pair)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create BybitTrader")
		}
		currentPricer = pricer.NewBybitPricer(bybitClient)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", conf.Platform)
	}

	tsLogger := zap.L().With(zap.String("pair", conf.Pair.String()))
	tradeService, err := services.NewTradeService(
		tsLogger,
		conf.Pair,
		conf.Usebalance, // This is the base amount for each DCA operation.
		currentPricer,
		currentTrader,
		conf.MaxDcaTrades,
		conf.DcaPercentThresholdBuy,
		conf.DcaPercentThresholdSell,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create TradeService")
	}

	return &TradingBot{
		Trader:       currentTrader,
		Pricer:       currentPricer,
		Config:       conf,
		tradeService: tradeService,
	}, nil
}

// Run executes the trading bot
func (b *TradingBot) Run(ctx context.Context, logger *zap.Logger) error {
	defer b.tradeService.Close()

	// Initial Buy Logic:
	// 1. Get current price.
	// 2. Try to record this as an initial purchase with TradeService.
	//    RecordInitialPurchase checks if purchases already exist.
	//    If it returns "already recorded" error, we log and continue (series exists).
	//    If it returns any other error, it's critical (e.g., WAL save failed for a new series).
	//    If it returns nil, the series was new and placeholder recorded. We then execute the actual buy.

	currentPrice, err := b.Pricer.GetPrice(b.Config.Pair)
	if err != nil {
		logger.Error("Failed to get current price for initial buy check", zap.Error(err), zap.String("pair", b.Config.Pair.String()))
		return errors.Wrapf(err, "failed to get current price for initial buy check for %s", b.Config.Pair.String())
	}

	// Calculate individual buy amount for the initial purchase
	if b.Config.MaxDcaTrades < 1 {
		logger.Error("Initial buy error: MaxDcaTrades must be at least 1.", zap.Int("maxDcaTrades", b.Config.MaxDcaTrades))
		return fmt.Errorf("MaxDcaTrades must be at least 1, configured value: %d", b.Config.MaxDcaTrades)
	}
	maxDcaTradesDecimal := decimal.NewFromInt(int64(b.Config.MaxDcaTrades))
	// b.Config.Usebalance is total capital
	calculatedInitialBuyAmount := b.Config.Usebalance.Div(maxDcaTradesDecimal)

	if calculatedInitialBuyAmount.IsZero() {
		logger.Error("Initial buy error: calculatedInitialBuyAmount is zero. Check Usebalance and MaxDcaTrades config.",
			zap.String("usebalance", b.Config.Usebalance.String()),
			zap.Int("maxDcaTrades", b.Config.MaxDcaTrades))
		return fmt.Errorf("calculatedInitialBuyAmount is zero, check Usebalance (%s) and MaxDcaTrades (%d)", b.Config.Usebalance.String(), b.Config.MaxDcaTrades)
	}

	purchaseTime := time.Now()

	// Attempt to record the potential first purchase.
	err = b.tradeService.RecordInitialPurchase(currentPrice, calculatedInitialBuyAmount, purchaseTime)

	if err == nil { // Successfully recorded a new series (placeholder for initial buy)
		logger.Info("New DCA series initiated in TradeService. Executing initial buy with Trader.",
			zap.String("pair", b.Config.Pair.String()),
			zap.String("price", currentPrice.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))

		if buyErr := b.Trader.Buy(calculatedInitialBuyAmount); buyErr != nil {
			logger.Error("Initial buy execution failed after series was marked for init in TradeService",
				zap.Error(buyErr),
				zap.String("pair", b.Config.Pair.String()))
			// CRITICAL: State recorded in WAL, but buy failed. Manual intervention might be needed.
			// Or, implement logic to revert/remove the last WAL entry in TradeService.
			return errors.Wrapf(buyErr, "initial buy execution failed for %s after series marked for init", b.Config.Pair.String())
		}
		logger.Info("Initial buy executed successfully.",
			zap.String("pair", b.Config.Pair.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))

	} else if err.Error() == "initial purchase already recorded or DCA series is not empty" {
		logger.Info("DCA series already has purchases (likely loaded from WAL). Skipping explicit initial buy.",
			zap.String("pair", b.Config.Pair.String()))
	} else { // Any other error from RecordInitialPurchase (e.g., WAL write failure)
		logger.Error("Failed to record initial purchase state in TradeService for a new series",
			zap.Error(err),
			zap.String("pair", b.Config.Pair.String()))
		return errors.Wrapf(err, "failed to record initial purchase state for new series for %s", b.Config.Pair.String())
	}

	// --- Main Trading Loop ---
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
			tradeEvent, err := b.tradeService.Trade()
			if err != nil {
				// services.ErrNoData is a common, non-critical error if WAL is empty or no initial data.
				if errors.Is(err, services.ErrNoData) {
					logger.Info("TradeService returned no data, continuing", zap.String("pair", b.Config.Pair.String()), zap.Error(err))
				} else {
					// For other errors, log as error and continue, or decide if some are fatal.
					logger.Error("TradeService.Trade failed", zap.String("pair", b.Config.Pair.String()), zap.Error(err))
				}
				continue // Continue to next tick
			}

			if tradeEvent != nil {
				logger.Info("Trade event occurred", zap.String("pair", b.Config.Pair.String()), zap.Any("event", tradeEvent))
			}
		}
	}
}
