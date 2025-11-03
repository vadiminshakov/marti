// Package ai implements AI-based trading strategy using LLM for decision making.
package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/market/analysis"
	"github.com/vadiminshakov/marti/internal/services/market/collector"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

// defaultHigherTimeframeLookback is used when not provided explicitly
const defaultHigherTimeframeLookback = 60

// tradersvc abstracts the exchange operations required for the AI margin strategy.
type tradersvc interface {
	Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
	SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// AIStrategy implements trading strategy using AI/LLM for decision making.
// The strategy operates in linear margin mode and derives its position state directly from the exchange.
type AIStrategy struct {
	pair             entity.Pair
	marketType       entity.MarketType
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
	marketType entity.MarketType,
	llmClient clients.LLMClient,
	marketData *collector.MarketDataCollector,
	pricer pricer,
	tradeSvc tradersvc,
	primaryTimeframe string,
	higherTimeframe string,
	primaryLookback int,
	higherLookback int,
) (*AIStrategy, error) {
	if marketType != entity.MarketTypeMargin {
		return nil, fmt.Errorf("AI strategy requires margin market type, got %s", marketType)
	}

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
		marketType:       marketType,
		llmClient:        llmClient,
		marketData:       marketData,
		pricer:           pricer,
		trader:           tradeSvc,
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

	fields := []zap.Field{
		zap.String("pair", s.pair.String()),
		zap.String("mode", s.marketType.String()),
	}

	quoteBalance, err := s.trader.GetBalance(ctx, s.pair.To)
	if err != nil {
		s.logger.Warn("Failed to get quote currency balance", zap.Error(err))
	} else {
		fields = append(fields,
			zap.String(s.pair.To+"_balance", quoteBalance.String()))
	}

	s.logger.Info("Starting AI trading strategy", fields...)

	s.refreshPosition(ctx)

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

	quoteBalance, err := s.trader.GetBalance(ctx, s.pair.To)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get margin balance")
	}

	s.refreshPosition(ctx)

	logFields := []zap.Field{
		zap.String("price", currentPrice.StringFixed(2)),
		zap.String(s.pair.To+"_balance", quoteBalance.StringFixed(2)),
	}

	if s.currentPosition != nil {
		logFields = append(logFields,
			zap.String("position_amount", s.currentPosition.Amount.StringFixed(8)),
			zap.String("position_entry", s.currentPosition.EntryPrice.StringFixed(2)))
	}

	s.logger.Info("Market analysis", logFields...)

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

	// parse and validate decision
	decision, err := entity.NewDecision(response, s.currentPosition != nil)
	if err != nil {
		// this should rarely happen now, but keep for safety
		s.logger.Error("Decision validation failed",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	s.logger.Info("Decision validation passed",
		zap.String("action", decision.Action),
		zap.Float64("risk_percent", decision.RiskPercent))

	s.logger.Info("📊 AI Decision",
		zap.String("action", strings.ToUpper(decision.Action)),
		zap.Float64("risk_percent", decision.RiskPercent),
		zap.String("reasoning", decision.Reasoning))

	// execute decision
	return s.executeDecision(ctx, decision, snapshot)
}

// refreshPosition pulls the latest margin position state from the exchange.
func (s *AIStrategy) refreshPosition(ctx context.Context) {
	position, err := s.trader.GetPosition(ctx, s.pair)
	if err != nil {
		s.logger.Warn("Failed to refresh margin position from exchange", zap.Error(err))
		return
	}

	if position == nil || position.Amount.LessThanOrEqual(decimal.Zero) {
		if s.currentPosition != nil {
			s.logger.Info("Clearing position - exchange reports no open margin position")
		}
		s.currentPosition = nil
		return
	}

	prev := s.currentPosition
	s.currentPosition = position

	if prev == nil {
		s.logger.Info("Tracked margin position initialized from exchange",
			zap.String("amount", position.Amount.String()),
			zap.String("entry_price", position.EntryPrice.String()))
		return
	}

	if !prev.Amount.Equal(position.Amount) {
		s.logger.Info("Margin position size updated",
			zap.String("old", prev.Amount.String()),
			zap.String("new", position.Amount.String()))
	}

	if !prev.EntryPrice.Equal(position.EntryPrice) {
		s.logger.Info("Margin position entry price updated",
			zap.String("old", prev.EntryPrice.String()),
			zap.String("new", position.EntryPrice.String()))
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
	decision *entity.Decision,
	snapshot MarketSnapshot,
) (*entity.TradeEvent, error) {
	switch decision.Action {
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
		return nil, fmt.Errorf("unknown action: %s", decision.Action)
	}
}

// executeBuy executes a buy order
func (s *AIStrategy) executeBuy(
	ctx context.Context,
	decision *entity.Decision,
	snapshot MarketSnapshot,
) (*entity.TradeEvent, error) {
	if s.currentPosition != nil {
		s.logger.Warn("Cannot open new position while one is already open")
		return nil, nil
	}

	// Calculate position size based on risk percent
	budget := entity.NewRiskBudget(decision.RiskPercent)
	positionValue, amount := budget.Allocate(snapshot.QuoteBalance, snapshot.Price())
	if amount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("Calculated position amount is zero; skipping buy execution",
			zap.Float64("risk_percent", decision.RiskPercent),
			zap.String("quote_balance", snapshot.QuoteBalance.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Executing AI buy order",
		zap.String("amount", amount.String()),
		zap.String("price", snapshot.Price().String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.Float64("risk_percent", decision.RiskPercent),
		zap.String("reasoning", decision.Reasoning),
		zap.String("order_id", orderID))

	if err := s.trader.Buy(ctx, amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute buy order")
	}

	if sim, ok := s.trader.(trader.SimulationTrader); ok {
		if err := sim.ApplyTrade(snapshot.Price(), positionValue, "buy"); err != nil {
			s.logger.Warn("Failed to apply simulated buy trade",
				zap.Error(err))
		}
	}

	exitPlan := decision.ExitPlan
	takeProfit := decimal.NewFromFloat(exitPlan.TakeProfitPrice)
	stopLoss := decimal.NewFromFloat(exitPlan.StopLossPrice)

	if takeProfit.GreaterThan(decimal.Zero) || stopLoss.GreaterThan(decimal.Zero) {
		if err := s.trader.SetPositionStops(ctx, s.pair, takeProfit, stopLoss); err != nil {
			s.logger.Warn("Failed to update position stops on exchange", zap.Error(err))
		}
	}

	s.refreshPosition(ctx)

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

	position := s.currentPosition

	orderID := uuid.New().String()

	s.logger.Info("Closing position",
		zap.String("entry_price", position.EntryPrice.String()),
		zap.String("current_price", currentPrice.String()),
		zap.String("amount", position.Amount.String()),
		zap.String("order_id", orderID))

	if err := s.trader.Sell(ctx, position.Amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute sell order")
	}

	if sim, ok := s.trader.(trader.SimulationTrader); ok {
		if err := sim.ApplyTrade(currentPrice, position.Amount, "sell"); err != nil {
			s.logger.Warn("Failed to apply simulated sell trade",
				zap.Error(err))
		}
	}

	// Calculate P&L
	pnl := position.PnL(currentPrice)
	s.logger.Info("Position closed",
		zap.String("pnl", pnl.String()))

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: position.Amount,
		Pair:   s.pair,
		Price:  currentPrice,
	}

	// Clear position
	s.currentPosition = nil
	s.refreshPosition(ctx)

	return tradeEvent, nil
}

// Close performs cleanup when the strategy is shut down
func (s *AIStrategy) Close() error {
	s.logger.Info("Closing AI strategy")
	return nil
}
