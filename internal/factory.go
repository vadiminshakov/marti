package internal

import (
	"context"
	"fmt"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"github.com/vadiminshakov/marti/internal/services/strategy/ai"
	"github.com/vadiminshakov/marti/internal/services/strategy/dca"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

type traderService interface {
	Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
	SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// createTraderAndPricer creates trader and pricer instances based on platform
func createTraderAndPricer(platform string, pair entity.Pair, marketType entity.MarketType, leverage int, client any) (traderService, Pricer, error) {
	switch platform {
	case "binance":
		binanceClient, ok := client.(*binance.Client)
		if !ok || binanceClient == nil {
			return nil, nil, fmt.Errorf("binance platform expects *binance.Client, got %T", client)
		}

		traderInstance, err := trader.NewBinanceTrader(binanceClient, pair, marketType, leverage)
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

		traderInstance, err := trader.NewBybitTrader(bybitClient, pair, marketType, leverage)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to create BybitTrader")
		}

		pricerInstance := pricer.NewBybitPricer(bybitClient)
		return traderInstance, pricerInstance, nil

	case "simulate":
		simulateClient, ok := client.(*clients.SimulateClient)
		if !ok || simulateClient == nil {
			return nil, nil, fmt.Errorf("simulate platform expects *clients.SimulateClient, got %T", client)
		}

		// use logger from context or create a new one
		logger := zap.L()
		pricerInstance := pricer.NewSimulatePricer(simulateClient.GetBinanceClient())
		traderInstance, err := trader.NewSimulateTrader(pair, marketType, leverage, logger, pricerInstance)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to create SimulateTrader")
		}
		return traderInstance, pricerInstance, nil

	default:
		return nil, nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// createTradingStrategy creates a trading strategy instance based on configuration
func createTradingStrategy(
	logger *zap.Logger,
	conf config.Config,
	pricer Pricer,
	tradeSvc traderService,
	client any,
) (TradingStrategy, error) {
	switch conf.StrategyType {
	case "dca":
		return createDCAStrategy(
			logger,
			conf.Pair,
			conf.AmountPercent,
			pricer,
			tradeSvc,
			conf.MaxDcaTrades,
			conf.DcaPercentThresholdBuy,
			conf.DcaPercentThresholdSell,
		)
	case "ai":
		return createAIStrategy(logger, conf, pricer, tradeSvc, client)
	default:
		return nil, fmt.Errorf("unsupported strategy type: %s", conf.StrategyType)
	}
}

// createDCAStrategy creates a DCA trading strategy
func createDCAStrategy(
	logger *zap.Logger,
	pair entity.Pair,
	amountPercent decimal.Decimal,
	pricer Pricer,
	tradeSvc traderService,
	maxDcaTrades int,
	dcaPercentThresholdBuy decimal.Decimal,
	dcaPercentThresholdSell decimal.Decimal,
) (TradingStrategy, error) {
	dcaStrategy, err := dca.NewDCAStrategy(
		logger,
		pair,
		amountPercent,
		pricer,
		tradeSvc,
		maxDcaTrades,
		dcaPercentThresholdBuy,
		dcaPercentThresholdSell,
	)
	if err != nil {
		return nil, err
	}

	return dcaStrategy, nil
}

// createAIStrategy creates an AI trading strategy
func createAIStrategy(
	logger *zap.Logger,
	conf config.Config,
	pricer Pricer,
	tradeSvc traderService,
	client any,
) (TradingStrategy, error) {
	// create PromptBuilder
	promptBuilder := promptbuilder.NewPromptBuilder(conf.Pair, logger)

	// create LLM client
	llmClient := clients.NewOpenAICompatibleClient(conf.LLMAPIURL, conf.LLMAPIKey, conf.Model, promptBuilder)

	// create kline provider based on platform
	var klineProvider collector.KlineProvider
	switch conf.Platform {
	case "binance":
		binanceClient, ok := client.(*binance.Client)
		if !ok {
			return nil, fmt.Errorf("binance platform expects *binance.Client for AI strategy")
		}
		klineProvider = collector.NewBinanceKlineProvider(binanceClient)
	case "bybit":
		bybitClient, ok := client.(*bybit.Client)
		if !ok {
			return nil, fmt.Errorf("bybit platform expects *bybit.Client for AI strategy")
		}
		klineProvider = collector.NewBybitKlineProvider(bybitClient)
	case "simulate":
		simulateClient, ok := client.(*clients.SimulateClient)
		if !ok {
			return nil, fmt.Errorf("simulate platform expects *clients.SimulateClient for AI strategy")
		}
		klineProvider = collector.NewBinanceKlineProvider(simulateClient.GetBinanceClient())
	default:
		return nil, fmt.Errorf("unsupported platform for AI strategy: %s", conf.Platform)
	}

	// create market data collector
	marketDataCollector := collector.NewMarketDataCollector(
		klineProvider,
		conf.Pair,
	)

	if conf.MarketType != entity.MarketTypeMargin {
		return nil, fmt.Errorf("AI strategy supports only margin market type, got %s", conf.MarketType)
	}

	// create AI strategy
	aiStrategy, err := ai.NewAIStrategy(
		logger,
		conf.Pair,
		conf.MarketType,
		llmClient,
		marketDataCollector,
		pricer,
		tradeSvc,
		conf.PrimaryTimeframe,
		conf.HigherTimeframe,
		conf.LookbackPeriods,
		conf.HigherLookbackPeriods,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create AI strategy")
	}

	return aiStrategy, nil
}
