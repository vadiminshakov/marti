package ai

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/domain"
	llmClientMock "github.com/vadiminshakov/marti/mocks/llmclient"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
	"go.uber.org/zap"
)

func decimalMatcher(expected decimal.Decimal) interface{} {
	return mock.MatchedBy(func(actual decimal.Decimal) bool {
		return actual.Equal(expected)
	})
}

func TestAIStrategyExecuteDecision_HoldIsNoOp(t *testing.T) {
	t.Parallel()

	strategy := &AIStrategy{
		pair:      domain.Pair{From: "BTC", To: "USDT"},
		logger:    zap.NewNop(),
		modelName: "test-model",
	}

	event, err := strategy.executeDecision(
		context.Background(),
		&domain.Decision{Action: "hold", RiskPercent: 0, Reasoning: "No edge"},
		testSnapshot(decimal.NewFromInt(100), decimal.NewFromInt(1000)),
		nil,
	)

	require.NoError(t, err)
	require.Nil(t, event)
}

func TestAIStrategyExecuteDecision_OpenLongExecutesTradeAndSetsStops(t *testing.T) {
	t.Parallel()

	trader := traderMock.NewTrader(t)
	strategy := &AIStrategy{
		pair:      domain.Pair{From: "BTC", To: "USDT"},
		logger:    zap.NewNop(),
		modelName: "test-model",
		trader:    trader,
	}

	decision := &domain.Decision{
		Action:      "open_long",
		RiskPercent: 10,
		Reasoning:   "Breakout",
		ExitPlan: domain.ExitPlan{
			TakeProfitPrice:       120,
			StopLossPrice:         95,
			InvalidationCondition: "Price closes below 95",
		},
	}

	trader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(decimal.NewFromInt(1)), mock.AnythingOfType("string")).Return(nil).Once()
	trader.On("SetPositionStops", mock.Anything, strategy.pair, decimalMatcher(decimal.NewFromInt(120)), decimalMatcher(decimal.NewFromInt(95))).Return(nil).Once()

	event, err := strategy.executeDecision(
		context.Background(),
		decision,
		testSnapshot(decimal.NewFromInt(100), decimal.NewFromInt(1000)),
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, event)
	require.Equal(t, domain.ActionOpenLong, event.Action)
	require.True(t, event.Amount.Equal(decimal.NewFromInt(1)))
}

func TestAIStrategyExecuteDecision_OpenLongWithOppositePositionIsNoOp(t *testing.T) {
	t.Parallel()

	trader := traderMock.NewTrader(t)
	strategy := &AIStrategy{
		pair:      domain.Pair{From: "BTC", To: "USDT"},
		logger:    zap.NewNop(),
		modelName: "test-model",
		trader:    trader,
	}

	position, err := domain.NewPositionFromExternalSnapshot(
		decimal.NewFromInt(2),
		decimal.NewFromInt(100),
		time.Now(),
		domain.PositionSideShort,
	)
	require.NoError(t, err)

	event, err := strategy.executeDecision(
		context.Background(),
		&domain.Decision{Action: "open_long", RiskPercent: 10, Reasoning: "Reversal", ExitPlan: domain.ExitPlan{TakeProfitPrice: 110, StopLossPrice: 95, InvalidationCondition: "Below 95"}},
		testSnapshot(decimal.NewFromInt(100), decimal.NewFromInt(1000)),
		position,
	)

	require.NoError(t, err)
	require.Nil(t, event)
}

func TestAIStrategyExecuteDecision_CloseLongExecutesFullExit(t *testing.T) {
	t.Parallel()

	trader := traderMock.NewTrader(t)
	strategy := &AIStrategy{
		pair:      domain.Pair{From: "BTC", To: "USDT"},
		logger:    zap.NewNop(),
		modelName: "test-model",
		trader:    trader,
	}

	position, err := domain.NewPositionFromExternalSnapshot(
		decimal.RequireFromString("1.25"),
		decimal.NewFromInt(100),
		time.Now(),
		domain.PositionSideLong,
	)
	require.NoError(t, err)

	trader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(position.Amount), mock.AnythingOfType("string")).Return(nil).Once()

	event, err := strategy.executeDecision(
		context.Background(),
		&domain.Decision{Action: "close_long", RiskPercent: 0, Reasoning: "Target hit"},
		testSnapshot(decimal.NewFromInt(115), decimal.NewFromInt(1000)),
		position,
	)

	require.NoError(t, err)
	require.NotNil(t, event)
	require.Equal(t, domain.ActionCloseLong, event.Action)
	require.True(t, event.Amount.Equal(position.Amount))
}

func TestAIStrategyGetAndValidateDecision_RejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	llmClient := llmClientMock.NewLLMClient(t)
	llmClient.On("GetDecision", mock.Anything, mock.AnythingOfType("*domain.Prompt")).Return(`{"action":"open_long","risk_percent":0,"reasoning":"bad","exit_plan":{"stop_loss_price":95,"take_profit_price":110,"invalidation_condition":"below 95"}}`, nil).Once()

	strategy := &AIStrategy{
		pair:      domain.Pair{From: "BTC", To: "USDT"},
		logger:    zap.NewNop(),
		modelName: "test-model",
		llmClient: llmClient,
	}

	_, err := strategy.getAndValidateDecision(context.Background(), testSnapshot(decimal.NewFromInt(100), decimal.NewFromInt(1000)), nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to parse AI decision")
}

func testSnapshot(price, quoteBalance decimal.Decimal) domain.MarketSnapshot {
	return domain.MarketSnapshot{
		PrimaryTimeFrame: &domain.Timeframe{
			Summary: &domain.TimeframeSummary{Price: price},
		},
		QuoteBalance: quoteBalance,
	}
}
