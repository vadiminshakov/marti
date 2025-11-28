package internal

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/clients"
	entity "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"github.com/vadiminshakov/marti/internal/services/strategy/ai"
	"github.com/vadiminshakov/marti/internal/services/strategy/dca"
)

// strategyFactory creates trading strategies.
type strategyFactory struct {
	logger *zap.Logger
}

// newStrategyFactory creates a new strategy factory.
func newStrategyFactory(logger *zap.Logger) *strategyFactory {
	return &strategyFactory{logger: logger}
}

// createTradingStrategy creates a trading strategy instance based on configuration.
func (f *strategyFactory) createTradingStrategy(
	conf config.Config,
	pricer priceService,
	tradeSvc traderService,
	klineProvider klineService,
	decisionStore aiDecisionWriter,
) (TradingStrategy, error) {
	switch conf.StrategyType {
	case "dca":
		return f.createDCAStrategy(
			conf.Pair,
			conf.AmountPercent,
			pricer,
			tradeSvc,
			conf.MaxDcaTrades,
			conf.DcaPercentThresholdBuy,
			conf.DcaPercentThresholdSell,
		)
	case "ai":
		return f.createAIStrategy(conf, pricer, tradeSvc, klineProvider, decisionStore)
	default:
		return nil, fmt.Errorf("unsupported strategy type: %s", conf.StrategyType)
	}
}

// createDCAStrategy creates a DCA trading strategy.
func (f *strategyFactory) createDCAStrategy(
	pair entity.Pair,
	amountPercent decimal.Decimal,
	pricer priceService,
	tradeSvc traderService,
	maxDcaTrades int,
	dcaPercentThresholdBuy decimal.Decimal,
	dcaPercentThresholdSell decimal.Decimal,
) (TradingStrategy, error) {
	dcaStrategy, err := dca.NewDCAStrategy(
		f.logger,
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
func (f *strategyFactory) createAIStrategy(
	conf config.Config,
	pricer priceService,
	tradeSvc traderService,
	klineProvider klineService,
	decisionStore aiDecisionWriter,
) (TradingStrategy, error) {
	// create PromptBuilder
	promptBuilder := promptbuilder.NewPromptBuilder(conf.Pair, f.logger)

	// create LLM client
	llmClient, err := clients.NewOpenAICompatibleClient(conf.LLMAPIURL, conf.LLMAPIKey, conf.Model, conf.LLMProxyURL, promptBuilder)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create LLM client")
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
		f.logger,
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
