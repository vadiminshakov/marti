// Package ai implements AI-based trading strategy using LLM for decision making.
package ai

import (
	"context"
	"encoding/json"
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
	"github.com/vadiminshakov/marti/internal/services/market/indicators"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"go.uber.org/zap"
)

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
	pair            entity.Pair
	llmClient       clients.LLMClient
	marketData      *collector.MarketDataCollector
	pricer          pricer
	trader          tradersvc
	logger          *zap.Logger
	currentPosition *Position
	marketAnalyzer  *analysis.MarketAnalyzer
	promptBuilder   *promptbuilder.PromptBuilder
	higherTimeframe string
}

// Position represents an open trading position tracked in memory.
// After restart, position is recovered from exchange balance.
type Position struct {
	EntryPrice   decimal.Decimal
	Amount       decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Invalidation string
	EntryTime    time.Time
}

// TradingDecision represents the AI's trading decision
type TradingDecision struct {
	Decision DecisionDetails `json:"decision"`
}

// DecisionDetails contains the details of the trading decision
type DecisionDetails struct {
	Action      string   `json:"action"`
	RiskPercent float64  `json:"risk_percent"`
	Reasoning   string   `json:"reasoning"`
	ExitPlan    ExitPlan `json:"exit_plan"`
}

// ExitPlan defines exit strategy for a trade
type ExitPlan struct {
	StopLossPrice         float64 `json:"stop_loss_price"`
	TakeProfitPrice       float64 `json:"take_profit_price"`
	InvalidationCondition string  `json:"invalidation_condition"`
}

// NewAIStrategy creates a new AI trading strategy instance
func NewAIStrategy(
	logger *zap.Logger,
	pair entity.Pair,
	llmClient clients.LLMClient,
	marketData *collector.MarketDataCollector,
	pricer pricer,
	trader tradersvc,
) (*AIStrategy, error) {
	marketAnalyzer := analysis.NewMarketAnalyzer(logger)
	promptBuilder := promptbuilder.NewPromptBuilder(pair, logger)

	higherTimeframe := "4h"

	return &AIStrategy{
		pair:            pair,
		llmClient:       llmClient,
		marketData:      marketData,
		pricer:          pricer,
		trader:          trader,
		logger:          logger,
		marketAnalyzer:  marketAnalyzer,
		promptBuilder:   promptBuilder,
		higherTimeframe: higherTimeframe,
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
	klines, indicatorData, err := s.marketData.GetMarketData(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get market data")
	}

	if len(klines) == 0 || len(indicatorData) == 0 {
		return nil, errors.New("insufficient market data")
	}

	currentPrice := klines[len(klines)-1].Close

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
	higherTimeframeSnapshot, err := s.marketData.GetMultiTimeframeData(ctx, s.higherTimeframe)
	if err != nil {
		s.logger.Warn("Failed to get higher timeframe data, continuing without it",
			zap.Error(err),
			zap.String("timeframe", s.higherTimeframe))
		higherTimeframeSnapshot = nil
	}

	// analyze volume patterns
	volumeAnalysis := s.marketAnalyzer.AnalyzeVolume(klines)

	// build prompt for LLM using PromptBuilder
	userPrompt := s.buildPrompt(klines, indicatorData, quoteBalance, volumeAnalysis, higherTimeframeSnapshot)

	// get decision from LLM
	response, err := s.llmClient.Chat(ctx, promptbuilder.SystemPrompt, userPrompt)
	if err != nil {
		s.logger.Error("Failed to get AI response", zap.Error(err))
		return nil, errors.Wrap(err, "failed to get AI decision")
	}

	// parse and validate decision
	// note: parseDecision now returns a default "hold" decision on validation failures
	// instead of returning an error, so we always get a valid decision
	decision, err := s.parseDecision(response)
	if err != nil {
		// this should rarely happen now, but keep for safety
		s.logger.Error("Critical error in parseDecision",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	s.logger.Info("📊 AI Decision",
		zap.String("action", strings.ToUpper(decision.Decision.Action)),
		zap.Float64("risk_percent", decision.Decision.RiskPercent),
		zap.String("reasoning", decision.Decision.Reasoning))

	// execute decision
	return s.executeDecision(ctx, decision, currentPrice, quoteBalance)
}

// syncPositionWithBalance synchronizes position state with actual exchange balance
func (s *AIStrategy) syncPositionWithBalance(baseBalance, currentPrice decimal.Decimal) {
	if baseBalance.GreaterThan(decimal.Zero) {
		if s.currentPosition == nil {
			// We have balance but no position record - create minimal position
			// AI will re-evaluate and decide what to do with it
			s.currentPosition = &Position{
				EntryPrice: currentPrice, // Approximate - we don't know actual entry
				Amount:     baseBalance,
				EntryTime:  time.Now(), // Approximate - we don't know actual entry time
			}
			s.logger.Info("Created position record from balance",
				zap.String("amount", baseBalance.String()),
				zap.String("approx_entry", currentPrice.String()))
		} else {
			// Update amount if it changed
			if !s.currentPosition.Amount.Equal(baseBalance) {
				s.logger.Info("Updating position amount from balance",
					zap.String("old", s.currentPosition.Amount.String()),
					zap.String("new", baseBalance.String()))
				s.currentPosition.Amount = baseBalance
			}
		}
	} else {
		// No balance - clear position
		if s.currentPosition != nil {
			s.logger.Info("Clearing position - no balance detected")
			s.currentPosition = nil
		}
	}
}

// buildPrompt constructs the prompt for the LLM using PromptBuilder
func (s *AIStrategy) buildPrompt(
	klines []collector.KlineData,
	indicatorData []indicators.IndicatorData,
	balance decimal.Decimal,
	volumeAnalysis analysis.VolumeAnalysis,
	higherTimeframeSnapshot *collector.TimeframeSnapshot,
) string {
	// Convert higher timeframe snapshot to promptbuilder format
	var htfSnapshot *promptbuilder.TimeframeSnapshot
	if higherTimeframeSnapshot != nil {
		htfSnapshot = &promptbuilder.TimeframeSnapshot{
			Timeframe: higherTimeframeSnapshot.Timeframe,
			Price:     higherTimeframeSnapshot.Price,
			EMA20:     higherTimeframeSnapshot.EMA20,
			EMA50:     higherTimeframeSnapshot.EMA50,
			RSI14:     higherTimeframeSnapshot.RSI14,
			Trend:     higherTimeframeSnapshot.Trend,
		}
	}

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
		Klines:          klines,
		Indicators:      indicatorData,
		VolumeAnalysis:  volumeAnalysis,
		HigherTimeframe: htfSnapshot,
		CurrentPosition: pbPosition,
		Balance:         balance,
	}

	// Use PromptBuilder to generate the prompt
	return s.promptBuilder.BuildUserPrompt(ctx)
}

// parseDecision parses the LLM response into a TradingDecision with comprehensive validation
func (s *AIStrategy) parseDecision(response string) (*TradingDecision, error) {
	// Clean up response - remove markdown code blocks if present
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Validate JSON structure before unmarshaling
	if !json.Valid([]byte(response)) {
		s.logger.Error("Invalid JSON structure in LLM response",
			zap.String("response", response))
		return s.createDefaultHoldDecision("Invalid JSON structure"), nil
	}

	var decision TradingDecision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		s.logger.Error("Failed to unmarshal JSON response",
			zap.Error(err),
			zap.String("response", response))
		return s.createDefaultHoldDecision("JSON unmarshal error"), nil
	}

	// Verify all required fields are present
	if err := s.validateRequiredFields(&decision); err != nil {
		s.logger.Error("Missing required fields in LLM response",
			zap.Error(err),
			zap.String("response", response))
		return s.createDefaultHoldDecision(fmt.Sprintf("Missing required fields: %v", err)), nil
	}

	// Validate action is one of allowed values
	validActions := map[string]bool{"buy": true, "sell": true, "hold": true, "close": true}
	if !validActions[decision.Decision.Action] {
		s.logger.Error("Invalid action in LLM response",
			zap.String("action", decision.Decision.Action),
			zap.String("response", response))
		return s.createDefaultHoldDecision(fmt.Sprintf("Invalid action: %s", decision.Decision.Action)), nil
	}

	// Validate risk_percent range (0.0-15.0)
	if decision.Decision.RiskPercent < 0 || decision.Decision.RiskPercent > 15 {
		s.logger.Error("Risk percent out of valid range",
			zap.Float64("risk_percent", decision.Decision.RiskPercent),
			zap.String("response", response))
		return s.createDefaultHoldDecision(fmt.Sprintf("Invalid risk_percent: %f (must be 0.0-15.0)", decision.Decision.RiskPercent)), nil
	}

	// Validate action consistency with position state
	if err := s.validateActionConsistency(&decision); err != nil {
		s.logger.Warn("Action inconsistent with position state",
			zap.Error(err),
			zap.String("action", decision.Decision.Action),
			zap.Bool("has_position", s.currentPosition != nil))
		return s.createDefaultHoldDecision(fmt.Sprintf("Action consistency error: %v", err)), nil
	}

	// Validate exit_plan is provided for "buy" actions
	if decision.Decision.Action == "buy" {
		if err := s.validateExitPlan(&decision); err != nil {
			s.logger.Error("Invalid or missing exit plan for buy action",
				zap.Error(err),
				zap.String("response", response))
			return s.createDefaultHoldDecision(fmt.Sprintf("Exit plan validation error: %v", err)), nil
		}
	}

	s.logger.Info("Decision validation passed",
		zap.String("action", decision.Decision.Action),
		zap.Float64("risk_percent", decision.Decision.RiskPercent))

	return &decision, nil
}

// validateRequiredFields checks that all required fields are present in the decision
func (s *AIStrategy) validateRequiredFields(decision *TradingDecision) error {
	if decision.Decision.Action == "" {
		return errors.New("action field is required")
	}
	if decision.Decision.Reasoning == "" {
		return errors.New("reasoning field is required")
	}
	// Confidence and RiskPercent are float64, so they always have a value (default 0)
	// We validate their ranges separately
	return nil
}

// validateActionConsistency validates that the action is consistent with current position state
func (s *AIStrategy) validateActionConsistency(decision *TradingDecision) error {
	action := decision.Decision.Action

	// Reject "buy" action if position already exists
	if action == "buy" && s.currentPosition != nil {
		return errors.New("cannot buy when position already exists")
	}

	// Reject "close" action if no position exists
	if action == "close" && s.currentPosition == nil {
		return errors.New("cannot close when no position exists")
	}

	return nil
}

// validateExitPlan validates that exit plan is properly defined for buy actions
func (s *AIStrategy) validateExitPlan(decision *TradingDecision) error {
	exitPlan := decision.Decision.ExitPlan

	if exitPlan.StopLossPrice <= 0 {
		return errors.New("stop_loss_price must be greater than 0")
	}

	if exitPlan.TakeProfitPrice <= 0 {
		return errors.New("take_profit_price must be greater than 0")
	}

	if exitPlan.InvalidationCondition == "" {
		return errors.New("invalidation_condition is required")
	}

	// Validate that stop loss is below take profit (for long positions)
	if exitPlan.StopLossPrice >= exitPlan.TakeProfitPrice {
		return errors.New("stop_loss_price must be less than take_profit_price")
	}

	return nil
}

// createDefaultHoldDecision creates a default "hold" decision when validation fails
func (s *AIStrategy) createDefaultHoldDecision(reason string) *TradingDecision {
	s.logger.Info("Defaulting to HOLD action due to validation failure",
		zap.String("reason", reason))

	return &TradingDecision{
		Decision: DecisionDetails{
			Action:      "hold",
			RiskPercent: 0.0,
			Reasoning:   fmt.Sprintf("Validation failed: %s. Defaulting to hold for safety.", reason),
			ExitPlan: ExitPlan{
				StopLossPrice:         0.0,
				TakeProfitPrice:       0.0,
				InvalidationCondition: "",
			},
		},
	}
}

// executeDecision executes the trading decision
func (s *AIStrategy) executeDecision(
	ctx context.Context,
	decision *TradingDecision,
	currentPrice decimal.Decimal,
	balance decimal.Decimal,
) (*entity.TradeEvent, error) {
	switch decision.Decision.Action {
	case "buy":
		return s.executeBuy(ctx, decision, currentPrice, balance)
	case "close":
		return s.executeClose(ctx, currentPrice)
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
	decision *TradingDecision,
	currentPrice decimal.Decimal,
	balance decimal.Decimal,
) (*entity.TradeEvent, error) {
	if s.currentPosition != nil {
		s.logger.Warn("Cannot open new position while one is already open")
		return nil, nil
	}

	// Calculate position size based on risk percent
	// For spot trading: we use risk_percent of available balance
	positionValue := balance.Mul(decimal.NewFromFloat(decision.Decision.RiskPercent / 100))

	// Calculate amount in base currency
	amount := positionValue.Div(currentPrice)

	orderID := uuid.New().String()

	s.logger.Info("Executing AI buy order",
		zap.String("amount", amount.String()),
		zap.String("price", currentPrice.String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.Float64("risk_percent", decision.Decision.RiskPercent),
		zap.String("reasoning", decision.Decision.Reasoning),
		zap.String("order_id", orderID))

	if err := s.trader.Buy(ctx, amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute buy order")
	}

	// Create position record
	s.currentPosition = &Position{
		EntryPrice:   currentPrice,
		Amount:       amount,
		StopLoss:     decimal.NewFromFloat(decision.Decision.ExitPlan.StopLossPrice),
		TakeProfit:   decimal.NewFromFloat(decision.Decision.ExitPlan.TakeProfitPrice),
		Invalidation: decision.Decision.ExitPlan.InvalidationCondition,
		EntryTime:    time.Now(),
	}

	return &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: amount,
		Pair:   s.pair,
		Price:  currentPrice,
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
	pnl := currentPrice.Sub(s.currentPosition.EntryPrice).Mul(s.currentPosition.Amount)
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
