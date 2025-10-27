// Package strategy implements AI-based trading strategy using LLM for decision making.
package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/indicators"
	"github.com/vadiminshakov/marti/internal/services/marketdata"
	"go.uber.org/zap"
)

// AIStrategy implements trading strategy using AI/LLM for decision making.
// The strategy is stateless - position state is derived from exchange balance.
// This is for SPOT trading only - no leverage or margin.
type AIStrategy struct {
	pair            entity.Pair
	llmClient       clients.LLMClient
	marketData      *marketdata.MarketDataCollector
	pricer          pricer
	trader          tradersvc
	logger          *zap.Logger
	currentPosition *Position
}

// Position represents an open trading position tracked in memory.
// After restart, position is recovered from exchange balance.
type Position struct {
	EntryPrice   decimal.Decimal
	Amount       decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Invalidation string
}

// TradingDecision represents the AI's trading decision
type TradingDecision struct {
	Decision DecisionDetails `json:"decision"`
}

// DecisionDetails contains the details of the trading decision
type DecisionDetails struct {
	Action      string   `json:"action"`
	Confidence  float64  `json:"confidence"`
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
	marketData *marketdata.MarketDataCollector,
	pricer pricer,
	trader tradersvc,
) (*AIStrategy, error) {
	return &AIStrategy{
		pair:       pair,
		llmClient:  llmClient,
		marketData: marketData,
		pricer:     pricer,
		trader:     trader,
		logger:     logger,
	}, nil
}

// Initialize prepares the AI strategy for trading
func (s *AIStrategy) Initialize(ctx context.Context) error {
	s.logger.Info("Initializing AI strategy",
		zap.String("pair", s.pair.String()))

	// Check current balances
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

	// If we have a base currency balance, we may have an open position from previous session
	// AI will evaluate it on the next Trade() call and decide what to do
	if baseBalance.GreaterThan(decimal.Zero) {
		s.logger.Info("Detected existing position, AI will evaluate on next iteration",
			zap.String("amount", baseBalance.String()))
	}

	return nil
}

// Trade executes the AI trading logic
func (s *AIStrategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	// Collect market data and indicators
	klines, indicatorData, err := s.marketData.GetMarketData(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get market data")
	}

	if len(klines) == 0 || len(indicatorData) == 0 {
		return nil, errors.New("insufficient market data")
	}

	currentPrice := klines[len(klines)-1].Close

	// Get current balances
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

	// Sync position state with actual balance
	s.syncPositionWithBalance(baseBalance, currentPrice)

	// Build prompt for LLM
	userPrompt := s.buildPrompt(klines, indicatorData, quoteBalance)

	// Get decision from LLM
	response, err := s.llmClient.Chat(ctx, systemPrompt, userPrompt)
	if err != nil {
		s.logger.Error("Failed to get AI response", zap.Error(err))
		return nil, errors.Wrap(err, "failed to get AI decision")
	}

	// Parse decision
	decision, err := s.parseDecision(response)
	if err != nil {
		s.logger.Error("Failed to parse AI decision",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	s.logger.Info("📊 AI Decision",
		zap.String("action", strings.ToUpper(decision.Decision.Action)),
		zap.Float64("confidence", decision.Decision.Confidence),
		zap.String("reasoning", decision.Decision.Reasoning))

	// Execute decision
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

// buildPrompt constructs the prompt for the LLM
func (s *AIStrategy) buildPrompt(
	klines []marketdata.KlineData,
	indicatorData []indicators.IndicatorData,
	balance decimal.Decimal,
) string {
	var sb strings.Builder

	// Get latest data points
	latestKline := klines[len(klines)-1]
	latestIndicators := indicatorData[len(indicatorData)-1]

	sb.WriteString(fmt.Sprintf("# Market Analysis for %s\n\n", s.pair.String()))
	sb.WriteString("## Current Market State\n\n")

	// Current price and indicators
	sb.WriteString(fmt.Sprintf("**Current Price:** %s\n", latestKline.Close.String()))
	sb.WriteString(fmt.Sprintf("**EMA20:** %s\n", latestIndicators.EMA20.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**EMA50:** %s\n", latestIndicators.EMA50.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**MACD:** %s\n", latestIndicators.MACD.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**RSI (7-period):** %s\n", latestIndicators.RSI7.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**RSI (14-period):** %s\n", latestIndicators.RSI14.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**ATR (3-period):** %s\n", latestIndicators.ATR3.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**ATR (14-period):** %s\n", latestIndicators.ATR14.StringFixed(2)))
	sb.WriteString("\n")

	// Recent price action (last 10 candles)
	sb.WriteString("## Recent Price Action (oldest → newest)\n\n")
	startIdx := len(klines) - 10
	if startIdx < 0 {
		startIdx = 0
	}

	sb.WriteString("Close prices: [")
	for i := startIdx; i < len(klines); i++ {
		if i > startIdx {
			sb.WriteString(", ")
		}
		sb.WriteString(klines[i].Close.StringFixed(2))
	}
	sb.WriteString("]\n\n")

	// Recent indicators (last 10 periods)
	if len(indicatorData) >= 10 {
		startIdx = len(indicatorData) - 10

		sb.WriteString("EMA20: [")
		for i := startIdx; i < len(indicatorData); i++ {
			if i > startIdx {
				sb.WriteString(", ")
			}
			sb.WriteString(indicatorData[i].EMA20.StringFixed(2))
		}
		sb.WriteString("]\n")

		sb.WriteString("MACD: [")
		for i := startIdx; i < len(indicatorData); i++ {
			if i > startIdx {
				sb.WriteString(", ")
			}
			sb.WriteString(indicatorData[i].MACD.StringFixed(2))
		}
		sb.WriteString("]\n")

		sb.WriteString("RSI (7-period): [")
		for i := startIdx; i < len(indicatorData); i++ {
			if i > startIdx {
				sb.WriteString(", ")
			}
			sb.WriteString(indicatorData[i].RSI7.StringFixed(2))
		}
		sb.WriteString("]\n\n")
	}

	// Account information
	sb.WriteString("## Account Information\n\n")
	sb.WriteString(fmt.Sprintf("**Available Balance (%s):** %s\n", s.pair.To, balance.StringFixed(2)))

	if s.currentPosition != nil {
		sb.WriteString("\n**Current Position:**\n")
		sb.WriteString(fmt.Sprintf("- Entry Price: %s\n", s.currentPosition.EntryPrice.String()))
		sb.WriteString(fmt.Sprintf("- Amount: %s\n", s.currentPosition.Amount.String()))
		if !s.currentPosition.StopLoss.IsZero() {
			sb.WriteString(fmt.Sprintf("- Stop Loss: %s\n", s.currentPosition.StopLoss.String()))
		}
		if !s.currentPosition.TakeProfit.IsZero() {
			sb.WriteString(fmt.Sprintf("- Take Profit: %s\n", s.currentPosition.TakeProfit.String()))
		}
		if s.currentPosition.Invalidation != "" {
			sb.WriteString(fmt.Sprintf("- Invalidation: %s\n", s.currentPosition.Invalidation))
		}

		// Calculate unrealized P&L
		currentPrice := latestKline.Close
		pnl := currentPrice.Sub(s.currentPosition.EntryPrice).Mul(s.currentPosition.Amount)
		pnlPercent := pnl.Div(s.currentPosition.EntryPrice.Mul(s.currentPosition.Amount)).Mul(decimal.NewFromInt(100))
		sb.WriteString(fmt.Sprintf("- Unrealized P&L: %s (%s%%)\n", pnl.StringFixed(2), pnlPercent.StringFixed(2)))
	} else {
		sb.WriteString("\n**Current Position:** None\n")
	}

	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString("Based on the market data above, provide your trading decision in JSON format.\n")
	if s.currentPosition != nil {
		sb.WriteString("You currently have an open position. Decide whether to hold or close it.\n")
	} else {
		sb.WriteString("You have no open position. Decide whether to buy, or hold (wait).\n")
	}

	return sb.String()
}

// parseDecision parses the LLM response into a TradingDecision
func (s *AIStrategy) parseDecision(response string) (*TradingDecision, error) {
	// Clean up response - remove markdown code blocks if present
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var decision TradingDecision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal JSON response")
	}

	// Validate decision
	validActions := map[string]bool{"buy": true, "sell": true, "hold": true, "close": true}
	if !validActions[decision.Decision.Action] {
		return nil, fmt.Errorf("invalid action: %s", decision.Decision.Action)
	}

	if decision.Decision.Confidence < 0 || decision.Decision.Confidence > 1 {
		return nil, fmt.Errorf("invalid confidence: %f", decision.Decision.Confidence)
	}

	if decision.Decision.RiskPercent < 0 || decision.Decision.RiskPercent > 15 {
		return nil, fmt.Errorf("invalid risk_percent: %f (must be 0-15)", decision.Decision.RiskPercent)
	}

	return &decision, nil
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
		zap.Float64("confidence", decision.Decision.Confidence),
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
