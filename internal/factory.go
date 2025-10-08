package internal

import (
	"context"
	"fmt"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/strategy"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

type Trader interface {
	Buy(ctx context.Context, amount decimal.Decimal) error
	Sell(ctx context.Context, amount decimal.Decimal) error
}

type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// createTraderAndPricer creates trader and pricer instances based on platform
func createTraderAndPricer(platform string, pair entity.Pair, client any) (Trader, Pricer, error) {
	switch platform {
	case "binance":
		binanceClient, ok := client.(*binance.Client)
		if !ok || binanceClient == nil {
			return nil, nil, fmt.Errorf("binance platform expects *binance.Client, got %T", client)
		}

		traderInstance, err := trader.NewBinanceTrader(binanceClient, pair)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to create BinanceTrader")
		}

		pricerInstance := pricer.NewBinancePricer(binanceClient)
		return traderInstance, pricerInstance, nil

	case "bybit":
		bybitClient, ok := client.(*bybit.Client)
		if !ok || bybitClient == nil {
			return nil, nil, fmt.Errorf("bybit platform expects *bybit.Client, got %T", client)
		}

		traderInstance, err := trader.NewBybitTrader(bybitClient, pair)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to create BybitTrader")
		}

		pricerInstance := pricer.NewBybitPricer(bybitClient)
		return traderInstance, pricerInstance, nil

	default:
		return nil, nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// createTradingStrategy creates a trading strategy instance
func createTradingStrategy(
	logger *zap.Logger,
	pair entity.Pair,
	amount decimal.Decimal,
	pricer Pricer,
	trader Trader,
	maxDcaTrades int,
	dcaPercentThresholdBuy decimal.Decimal,
	dcaPercentThresholdSell decimal.Decimal,
) (TradingStrategy, error) {
	// currently only DCA strategy is supported, but this can be extended
	return strategy.NewDCAStrategy(
		logger,
		pair,
		amount,
		pricer,
		trader,
		maxDcaTrades,
		dcaPercentThresholdBuy,
		dcaPercentThresholdSell,
	)
}
