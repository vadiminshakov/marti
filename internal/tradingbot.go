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
	"github.com/vadiminshakov/marti/internal/services"
	"github.com/vadiminshakov/marti/internal/services/pricer"
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

// TradingBot represents a single trading instance
type TradingBot struct {
	Trader       tradersvc
	Pricer       pricersvc
	Config       config.Config
	tradeService *services.TradeService
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(conf config.Config, client interface{}) (*TradingBot, error) {
	var currentTrader tradersvc
	var currentPricer pricersvc
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
		conf.Amount,
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
	
	// Get current price and wait for it to drop before first buy
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
	calculatedInitialBuyAmount := b.Config.Amount.Div(maxDcaTradesDecimal)

	if calculatedInitialBuyAmount.IsZero() {
		logger.Error("Initial buy error: calculatedInitialBuyAmount is zero. Check Amount and MaxDcaTrades config.",
			zap.String("amount", b.Config.Amount.String()),
			zap.Int("maxDcaTrades", b.Config.MaxDcaTrades))
		return fmt.Errorf("calculatedInitialBuyAmount is zero, check Amount (%s) and MaxDcaTrades (%d)", b.Config.Amount.String(), b.Config.MaxDcaTrades)
	}

	// Set initial price as reference
	b.tradeService.SetLastSellPrice(currentPrice)
	
	// Check if we have any existing DCA series
	if len(b.tradeService.GetDCASeries().Purchases) == 0 {
		// No DCA series exists, execute initial buy
		logger.Info("No existing DCA series. Executing initial buy.",
			zap.String("pair", b.Config.Pair.String()),
			zap.String("currentPrice", currentPrice.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))
		
		// Execute initial buy
		if buyErr := b.Trader.Buy(calculatedInitialBuyAmount); buyErr != nil {
			logger.Error("Initial buy execution failed",
				zap.Error(buyErr),
				zap.String("pair", b.Config.Pair.String()))
			return errors.Wrapf(buyErr, "initial buy execution failed for %s", b.Config.Pair.String())
		}
		
		// Record the purchase
		if err := b.tradeService.AddDCAPurchase(currentPrice, calculatedInitialBuyAmount, time.Now(), 0); err != nil {
			logger.Error("Failed to record initial purchase state",
				zap.Error(err),
				zap.String("pair", b.Config.Pair.String()))
			return errors.Wrapf(err, "failed to record initial purchase state for %s", b.Config.Pair.String())
		}
		
		logger.Info("Initial buy executed successfully.",
			zap.String("pair", b.Config.Pair.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))
	} else {
		// DCA series already exists (loaded from WAL)
		logger.Info("DCA series already exists (loaded from WAL). Continuing with existing trades.",
			zap.String("pair", b.Config.Pair.String()),
			zap.Int("existingPurchases", len(b.tradeService.GetDCASeries().Purchases)))
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
			tradeEvent, err := b.tradeService.Trade()
			if err != nil {
				if errors.Is(err, services.ErrNoData) {
					logger.Info("TradeService returned no data, continuing", zap.String("pair", b.Config.Pair.String()), zap.Error(err))
				} else {
					logger.Error("TradeService.Trade failed", zap.String("pair", b.Config.Pair.String()), zap.Error(err))
				}
				continue
			}

			if tradeEvent != nil {
				logger.Info("Trade event occurred", zap.String("pair", b.Config.Pair.String()), zap.Any("event", tradeEvent))
			}
		}
	}
}
