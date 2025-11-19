package internal

import (
	"context"
	"fmt"
	"sync"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"github.com/vadiminshakov/marti/internal/services/strategy/ai"
	"github.com/vadiminshakov/marti/internal/services/strategy/dca"
	"github.com/vadiminshakov/marti/internal/services/trader"
)

type traderService interface {
	ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
	SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

type klineProvider interface {
	GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]entity.MarketCandle, error)
}

// ServiceProvider defines a factory interface for creating platform-specific services.
type ServiceProvider interface {
	Trader(pair entity.Pair, marketType entity.MarketType, leverage int, stateKey string) (traderService, error)
	Pricer() (Pricer, error)
	KlineProvider() (klineProvider, error)
}

// NewServiceProvider creates a new service provider based on the client type.
// This is the single point of truth for dispatching to platform-specific implementations.
func NewServiceProvider(client any, logger *zap.Logger) (ServiceProvider, error) {
	switch c := client.(type) {
	case *binance.Client:
		return &binanceProvider{client: c}, nil
	case *bybit.Client:
		return &bybitProvider{client: c}, nil
	case *clients.SimulateClient:
		return &simulateProvider{client: c, logger: logger}, nil
	case *clients.HyperliquidClient:
		return &hyperliquidProvider{client: c}, nil
	default:
		return nil, fmt.Errorf("unsupported client type: %T", client)
	}
}

type binanceProvider struct {
	client *binance.Client
}

func (p *binanceProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewBinanceTrader(p.client, pair, marketType, leverage)
}
func (p *binanceProvider) Pricer() (Pricer, error) {
	return pricer.NewBinancePricer(p.client), nil
}
func (p *binanceProvider) KlineProvider() (klineProvider, error) {
	return collector.NewBinanceKlineProvider(p.client), nil
}

type bybitProvider struct {
	client *bybit.Client
}

func (p *bybitProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewBybitTrader(p.client, pair, marketType, leverage)
}
func (p *bybitProvider) Pricer() (Pricer, error) {
	return pricer.NewBybitPricer(p.client), nil
}
func (p *bybitProvider) KlineProvider() (klineProvider, error) {
	return collector.NewBybitKlineProvider(p.client), nil
}

type simulateProvider struct {
	client     *clients.SimulateClient
	logger     *zap.Logger
	pricer     Pricer
	pricerOnce sync.Once
}

func (p *simulateProvider) getPricer() Pricer {
	p.pricerOnce.Do(func() {
		p.pricer = pricer.NewSimulatePricer(p.client.GetBinanceClient())
	})
	return p.pricer
}

func (p *simulateProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, stateKey string) (traderService, error) {
	return trader.NewSimulateTrader(pair, marketType, leverage, p.logger, p.getPricer(), stateKey)
}
func (p *simulateProvider) Pricer() (Pricer, error) {
	return p.getPricer(), nil
}
func (p *simulateProvider) KlineProvider() (klineProvider, error) {
	return collector.NewBinanceKlineProvider(p.client.GetBinanceClient()), nil
}

type hyperliquidProvider struct {
	client *clients.HyperliquidClient
}

func (p *hyperliquidProvider) Trader(pair entity.Pair, marketType entity.MarketType, leverage int, _ string) (traderService, error) {
	return trader.NewHyperliquidTrader(p.client.Exchange(), p.client.AccountAddress(), pair, marketType, leverage)
}
func (p *hyperliquidProvider) Pricer() (Pricer, error) {
	return pricer.NewHyperliquidPricer(p.client.Exchange().Info()), nil
}
func (p *hyperliquidProvider) KlineProvider() (klineProvider, error) {
	return collector.NewHyperliquidKlineProvider(p.client.Exchange().Info()), nil
}

// createTradingStrategy creates a trading strategy instance based on configuration.
func createTradingStrategy(
	logger *zap.Logger,
	conf config.Config,
	pricer Pricer,
	tradeSvc traderService,
	provider ServiceProvider,
	decisionStore aiDecisionWriter,
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
		return createAIStrategy(logger, conf, pricer, tradeSvc, provider, decisionStore)
	default:
		return nil, fmt.Errorf("unsupported strategy type: %s", conf.StrategyType)
	}
}

// createDCAStrategy creates a DCA trading strategy.
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
		return nil, errors.Wrap(err, "failed to create DCA strategy")
	}

	return dcaStrategy, nil
}

// createAIStrategy creates an AI trading strategy.
func createAIStrategy(
	logger *zap.Logger,
	conf config.Config,
	pricer Pricer,
	tradeSvc traderService,
	provider ServiceProvider,
	decisionStore aiDecisionWriter,
) (TradingStrategy, error) {
	// create PromptBuilder
	promptBuilder := promptbuilder.NewPromptBuilder(conf.Pair, logger)

	// create LLM client
	llmClient := clients.NewOpenAICompatibleClient(conf.LLMAPIURL, conf.LLMAPIKey, conf.Model, promptBuilder)

	// create kline provider using the provider
	klineProvider, err := provider.KlineProvider()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kline provider")
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
		decisionStore,
		conf.Model,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create AI strategy")
	}

	return aiStrategy, nil
}
