// Package ai implements AI-based trading strategy using LLM for decision making.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/market/analysis"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"go.uber.org/zap"
)

// defaultHigherTimeframeLookback is used when not provided explicitly
const defaultHigherTimeframeLookback = 60

type tradersvc interface {
	Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// AIStrategy implements trading strategy using AI/LLM for decision making.
// The strategy is stateless - position state is derived from exchange balance.
// This is for SPOT trading only - no leverage or margin.
type AIStrategy struct {
	pair             entity.Pair
	llmClient        clients.LLMClient
	marketData       *collector.MarketDataCollector
	pricer           pricer
	trader           tradersvc
	logger           *zap.Logger
	currentPosition  *entity.Position
	marketAnalyzer   *analysis.MarketAnalyzer
	promptBuilder    *promptbuilder.PromptBuilder
	primaryTimeframe string
	primaryLookback  int
	higherTimeframe  string
	higherLookback   int
}

// MarketSnapshot aggregates relevant market data for a single trading decision cycle.
type MarketSnapshot struct {
	PrimaryTimeFrame *entity.Timeframe
	HigherTimeFrame  *entity.Timeframe
	QuoteBalance     decimal.Decimal
	VolumeAnalysis   analysis.VolumeAnalysis
}

// Price returns the latest close price of the primary timeframe if available.
func (s MarketSnapshot) Price() decimal.Decimal {
	if s.PrimaryTimeFrame == nil {
		return decimal.Zero
	}

	if s.PrimaryTimeFrame.Summary != nil {
		return s.PrimaryTimeFrame.Summary.Price
	}

	if price, ok := s.PrimaryTimeFrame.LatestPrice(); ok {
		return price
	}

	return decimal.Zero
}

// NewAIStrategy creates a new AI trading strategy instance
func NewAIStrategy(
	logger *zap.Logger,
	pair entity.Pair,
	llmClient clients.LLMClient,
	marketData *collector.MarketDataCollector,
	pricer pricer,
	trader tradersvc,
	primaryTimeframe string,
	higherTimeframe string,
	primaryLookback int,
	higherLookback int,
) (*AIStrategy, error) {
	marketAnalyzer := analysis.NewMarketAnalyzer(logger)
	promptBuilder := promptbuilder.NewPromptBuilder(pair, logger)

	if higherTimeframe == "" {
		higherTimeframe = "15m"
	}

	if higherLookback == 0 {
		// fall back to default to preserve previous behavior
		higherLookback = defaultHigherTimeframeLookback
	}

	return &AIStrategy{
		pair:             pair,
		llmClient:        llmClient,
		marketData:       marketData,
		pricer:           pricer,
		trader:           trader,
		logger:           logger,
		marketAnalyzer:   marketAnalyzer,
		promptBuilder:    promptBuilder,
		primaryTimeframe: primaryTimeframe,
		primaryLookback:  primaryLookback,
		higherTimeframe:  higherTimeframe,
		higherLookback:   higherLookback,
	}, nil
}

// Initialize prepares the AI strategy for trading
func (s *AIStrategy) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing AI strategy",
		zap.String("pair", s.pair.String()))

	// check current balances
	baseBalance, err := s.trader.GetBalance(ctx, s.pair.From)
	if err != nil {
		s.logger.Warn("Failed to get base currency balance", zap.Error(err))
	}
	quoteBalance, err := s.trader.GetBalance(ctx, s.pair.To)
	if err != nil {
		s.logger.Warn("Failed to get quote currency balance", zap.Error(err))
	}

	s.logger.Info("Starting AI trading strategy",
		zap.String("pair", s.pair.String()),
		zap.String(s.pair.From+"_balance", baseBalance.String()),
		zap.String(s.pair.To+"_balance", quoteBalance.String()))

	// if we have a base currency balance, we may have an open position from previous session
	// AI will evaluate it on the next Trade() call and decide what to do
	if baseBalance.GreaterThan(decimal.Zero) {
		s.logger.Info("Detected existing position, AI will evaluate on next iteration",
			zap.String("amount", baseBalance.String()))
	}

	return nil
}

// Trade executes the AI trading logic
func (s *AIStrategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	// collect market data and indicators
	primaryFrame, err := s.marketData.FetchTimeframeData(ctx, s.primaryTimeframe, s.primaryLookback)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get market data")
	}

	if primaryFrame == nil || len(primaryFrame.Candles) == 0 {
		return nil, errors.New("insufficient market data")
	}

	if len(primaryFrame.Indicators) == 0 || primaryFrame.Summary == nil {
		return nil, errors.New("insufficient indicator data for primary timeframe")
	}

	currentPrice := primaryFrame.Summary.Price

	// get current balances
	baseBalance, err := s.trader.GetBalance(ctx, s.pair.From)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get base balance")
	}
	quoteBalance, err := s.trader.GetBalance(ctx, s.pair.To)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get quote balance")
	}

	s.logger.Info("Market analysis",
		zap.String("price", currentPrice.StringFixed(2)),
		zap.String(s.pair.From+"_balance", baseBalance.StringFixed(8)),
		zap.String(s.pair.To+"_balance", quoteBalance.StringFixed(2)))

	// sync position state with actual balance
	s.syncPositionWithBalance(baseBalance, currentPrice)

	// fetch higher timeframe data for multi-timeframe analysis
	higherTimeframeData, err := s.marketData.FetchTimeframeData(ctx, s.higherTimeframe, s.higherLookback)
	if err != nil {
		s.logger.Warn("Failed to get higher timeframe data, continuing without it",
			zap.Error(err),
			zap.String("timeframe", s.higherTimeframe))
		higherTimeframeData = nil
	}

	// analyze volume patterns
	volumeAnalysis := s.marketAnalyzer.AnalyzeVolume(primaryFrame.Candles)

	snapshot := MarketSnapshot{
		PrimaryTimeFrame: primaryFrame,
		QuoteBalance:     quoteBalance,
		VolumeAnalysis:   volumeAnalysis,
		HigherTimeFrame:  higherTimeframeData,
	}

	// build prompt for LLM using PromptBuilder
	userPrompt := s.buildPrompt(snapshot)

	// get decision from LLM
	response, err := s.llmClient.Chat(ctx, promptbuilder.SystemPrompt, userPrompt)
	if err != nil {
		s.logger.Error("Failed to get AI response", zap.Error(err))
		return nil, errors.Wrap(err, "failed to get AI decision")
	}

	// parse and validate decision; default "hold" is produced on validation failures
	outcome, err := entity.ParseTradingDecision(response, s.currentPosition != nil)
	if err != nil {
		// this should rarely happen now, but keep for safety
		s.logger.Error("Critical error in parseDecision",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	if outcome.Defaulted {
		fields := []zap.Field{zap.String("reason", outcome.Reason)}
		switch outcome.Severity {
		case entity.DecisionSeverityWarn:
			s.logger.Warn("Decision validation warning; defaulting to HOLD", fields...)
		case entity.DecisionSeverityError:
			s.logger.Error("Decision validation failed; defaulting to HOLD", fields...)
		default:
			s.logger.Info("Decision validation info; defaulting to HOLD", fields...)
		}
	}

	decision := outcome.Decision

	if !outcome.Defaulted {
		s.logger.Info("Decision validation passed",
			zap.String("action", decision.Decision.Action),
			zap.Float64("risk_percent", decision.Decision.RiskPercent))
	}

	s.logger.Info("📊 AI Decision",
		zap.String("action", strings.ToUpper(decision.Decision.Action)),
		zap.Float64("risk_percent", decision.Decision.RiskPercent),
		zap.String("reasoning", decision.Decision.Reasoning))

	// execute decision
	return s.executeDecision(ctx, decision, snapshot)
}

// syncPositionWithBalance synchronizes position state with actual exchange balance
// syncPositionWithBalance derives / reconciles in-memory position state from the live base asset balance.
// The AI strategy is designed to be stateless: the exchange (or simulated) spot balance is the single
// source of truth for whether a position is open and for its current size.
//
// Rules:
//  1. baseBalance > 0 AND no currentPosition -> reconstruct a lightweight position via NewPositionFromSnapshot.
//     The entry price is approximated by currentPrice because the true historical fill price may not be
//     available after a restart; risk controls (SL/TP/Invalidation) are intentionally left blank.
//  2. baseBalance > 0 AND currentPosition exists -> update position.Amount if it changed (e.g. fees, partial fills).
//  3. baseBalance == 0 -> clear currentPosition (position considered closed externally or previously sold).
//
// Rationale:
// - Allows the bot to resume after a crash/restart without having persisted internal state.
// - Avoids acting on stale position metadata; only size and a best-effort entry price are reconstructed.
func (s *AIStrategy) syncPositionWithBalance(baseBalance, currentPrice decimal.Decimal) {
	// if we have balance but no position, reconstruct it
	if baseBalance.GreaterThan(decimal.Zero) {
		if s.currentPosition == nil {
			recovered, err := entity.NewPositionFromExternalSnapshot(baseBalance, currentPrice, time.Now())
			if err != nil {
				s.logger.Warn("Failed to reconstruct position from balance", zap.Error(err))
				return
			}
			s.currentPosition = recovered
			s.logger.Info("Created position record from balance",
				zap.String("amount", baseBalance.String()),
				zap.String("approx_entry", currentPrice.String()))
			return
		}

		oldAmount := s.currentPosition.Amount
		if s.currentPosition.UpdateAmount(baseBalance) {
			s.logger.Info("Updating position amount from balance",
				zap.String("old", oldAmount.String()),
				zap.String("new", baseBalance.String()))
		}
	} else {
		// no balance - clear position
		if s.currentPosition != nil {
			s.logger.Info("Clearing position - no balance detected")
			s.currentPosition = nil
		}
	}
}

// buildPrompt constructs the prompt for the LLM using PromptBuilder
func (s *AIStrategy) buildPrompt(snapshot MarketSnapshot) string {
	// Convert current position to promptbuilder format
	var pbPosition *promptbuilder.Position
	if s.currentPosition != nil {
		pbPosition = &promptbuilder.Position{
			EntryPrice:   s.currentPosition.EntryPrice,
			Amount:       s.currentPosition.Amount,
			StopLoss:     s.currentPosition.StopLoss,
			TakeProfit:   s.currentPosition.TakeProfit,
			Invalidation: s.currentPosition.Invalidation,
			EntryTime:    s.currentPosition.EntryTime,
		}
	}

	// Create market context
	ctx := promptbuilder.MarketContext{
		Primary:         snapshot.PrimaryTimeFrame,
		VolumeAnalysis:  snapshot.VolumeAnalysis,
		HigherTimeframe: snapshot.HigherTimeFrame,
		CurrentPosition: pbPosition,
		Balance:         snapshot.QuoteBalance,
	}

	// Use PromptBuilder to generate the prompt
	return s.promptBuilder.BuildUserPrompt(ctx)
}

// executeDecision executes the trading decision
func (s *AIStrategy) executeDecision(
	ctx context.Context,
	decision *entity.TradingDecision,
	snapshot MarketSnapshot,
) (*entity.TradeEvent, error) {
	switch decision.Decision.Action {
	case "buy":
		return s.executeBuy(ctx, decision, snapshot)
	case "close":
		return s.executeClose(ctx, snapshot.Price())
	case "hold":
		return nil, nil
	case "sell":
		s.logger.Warn("Short selling not yet supported, treating as HOLD")
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", decision.Decision.Action)
	}
}

// executeBuy executes a buy order
func (s *AIStrategy) executeBuy(
	ctx context.Context,
	decision *entity.TradingDecision,
	snapshot MarketSnapshot,
) (*entity.TradeEvent, error) {
	if s.currentPosition != nil {
		s.logger.Warn("Cannot open new position while one is already open")
		return nil, nil
	}

	// Calculate position size based on risk percent
	budget := entity.NewRiskBudget(decision.Decision.RiskPercent)
	positionValue, amount := budget.Allocate(snapshot.QuoteBalance, snapshot.Price())
	if amount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("Calculated position amount is zero; skipping buy execution",
			zap.Float64("risk_percent", decision.Decision.RiskPercent),
			zap.String("quote_balance", snapshot.QuoteBalance.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Executing AI buy order",
		zap.String("amount", amount.String()),
		zap.String("price", snapshot.Price().String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.Float64("risk_percent", decision.Decision.RiskPercent),
		zap.String("reasoning", decision.Decision.Reasoning),
		zap.String("order_id", orderID))

	if err := s.trader.Buy(ctx, amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute buy order")
	}

	// Create position record
	position, err := entity.NewPosition(amount, snapshot.Price(), time.Now(), decision.Decision.ExitPlan)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create position")
	}

	s.currentPosition = position

	return &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: amount,
		Pair:   s.pair,
		Price:  snapshot.Price(),
	}, nil
}

// executeClose closes the current position
func (s *AIStrategy) executeClose(ctx context.Context, currentPrice decimal.Decimal) (*entity.TradeEvent, error) {
	if s.currentPosition == nil {
		s.logger.Warn("No position to close")
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Closing position",
		zap.String("entry_price", s.currentPosition.EntryPrice.String()),
		zap.String("current_price", currentPrice.String()),
		zap.String("amount", s.currentPosition.Amount.String()),
		zap.String("order_id", orderID))

	if err := s.trader.Sell(ctx, s.currentPosition.Amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute sell order")
	}

	// Calculate P&L
	pnl := s.currentPosition.PnL(currentPrice)
	s.logger.Info("Position closed",
		zap.String("pnl", pnl.String()))

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: s.currentPosition.Amount,
		Pair:   s.pair,
		Price:  currentPrice,
	}

	// Clear position
	s.currentPosition = nil

	return tradeEvent, nil
}

// Close performs cleanup when the strategy is shut down
func (s *AIStrategy) Close() error {
	s.logger.Info("Closing AI strategy")
	return nil
}
