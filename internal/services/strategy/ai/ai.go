// Package ai implements AI-based trading strategy using LLM for decision making.
package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/promptbuilder"
	"go.uber.org/zap"
)

// defaultHigherTimeframeLookback is used when not provided explicitly
const defaultHigherTimeframeLookback = 60

// tradersvc abstracts the exchange operations required for the AI margin strategy.
type tradersvc interface {
	ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
	SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

type llmClient interface {
	GetDecision(ctx context.Context, marketContext promptbuilder.MarketContext) (string, error)
}

type marketDataCollector interface {
	FetchTimeframeData(ctx context.Context, interval string, lookback int) (*entity.Timeframe, error)
}

// AIStrategy implements trading strategy using AI/LLM for decision making.
// The strategy operates in linear margin mode and derives its position state directly from the exchange.
type AIStrategy struct {
	pair             entity.Pair
	marketType       entity.MarketType
	llmClient        llmClient
	marketData       marketDataCollector
	pricer           pricer
	trader           tradersvc
	logger           *zap.Logger
	primaryTimeframe string
	primaryLookback  int
	higherTimeframe  string
	higherLookback   int
}

// NewAIStrategy creates a new AI trading strategy instance
func NewAIStrategy(
	logger *zap.Logger,
	pair entity.Pair,
	marketType entity.MarketType,
	llmClient llmClient,
	marketData marketDataCollector,
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

	if higherTimeframe == "" {
		higherTimeframe = "15m"
	}

	if higherLookback == 0 {
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

	position, err := s.trader.GetPosition(ctx, s.pair)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get position")
	}

	baseBalance, baseErr := s.trader.GetBalance(ctx, s.pair.From)
	if baseErr != nil {
		s.logger.Warn("Failed to get base currency balance for logs",
			zap.Error(baseErr),
			zap.String("currency", s.pair.From))
	}

	logFields := []zap.Field{
		zap.String("price", currentPrice.StringFixed(2)),
		zap.String(s.pair.To+"_balance", quoteBalance.StringFixed(2)),
	}

	if position != nil {
		logFields = append(logFields,
			zap.String("position_amount", position.Amount.StringFixed(8)),
			zap.String("position_entry", position.EntryPrice.StringFixed(2)))
	}

	if baseErr == nil {
		logFields = append(logFields,
			zap.String(s.pair.From+"_balance", baseBalance.StringFixed(2)))
	}

	if position != nil {
		logFields = append(logFields,
			zap.String("position_pnl", position.PnL(currentPrice).StringFixed(2)))
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
	volumeAnalysis := entity.NewVolumeAnalysis(primaryFrame.Candles)

	snapshot := entity.MarketSnapshot{
		PrimaryTimeFrame: primaryFrame,
		QuoteBalance:     quoteBalance,
		VolumeAnalysis:   volumeAnalysis,
		HigherTimeFrame:  higherTimeframeData,
	}

	// get decision from LLM
	response, err := s.llmClient.GetDecision(ctx, s.buildMarketContext(snapshot, position))
	if err != nil {
		s.logger.Error("Failed to get AI response", zap.Error(err))
		return nil, errors.Wrap(err, "failed to get AI decision")
	}

	// parse and validate decision
	decision, err := entity.NewDecision(response)
	if err != nil {
		s.logger.Error("Decision validation failed",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	s.logger.Info("Decision validation passed",
		zap.String("action", decision.Action),
		zap.Float64("risk_percent", decision.RiskPercent))

	decisionFields := []zap.Field{
		zap.String("action", strings.ToUpper(decision.Action)),
		zap.String("reasoning", decision.Reasoning),
	}

	if decision.Action == "open_long" || decision.Action == "open_short" {
		decisionFields = append(decisionFields,
			zap.Float64("risk_percent", decision.RiskPercent))
	}

	s.logger.Info("AI decision", decisionFields...)

	// execute decision
	return s.executeDecision(ctx, decision, snapshot, position)
}

// buildMarketContext constructs the market context for the LLM
func (s *AIStrategy) buildMarketContext(snapshot entity.MarketSnapshot, position *entity.Position) promptbuilder.MarketContext {
	return promptbuilder.MarketContext{
		Primary:         snapshot.PrimaryTimeFrame,
		VolumeAnalysis:  snapshot.VolumeAnalysis,
		HigherTimeframe: snapshot.HigherTimeFrame,
		CurrentPosition: position,
		Balance:         snapshot.QuoteBalance,
	}
}

// executeDecision executes the trading decision
func (s *AIStrategy) executeDecision(
	ctx context.Context,
	decision *entity.Decision,
	snapshot entity.MarketSnapshot,
	position *entity.Position,
) (*entity.TradeEvent, error) {
	switch decision.Action {
	case "open_long":
		return s.executeBuy(ctx, decision, snapshot, position)
	case "close_long":
		return s.executeSell(ctx, snapshot.Price(), position)
	case "open_short":
		return s.executeOpenShort(ctx, decision, snapshot, position)
	case "close_short":
		return s.executeCloseShort(ctx, snapshot.Price(), position)
	case "hold":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", decision.Action)
	}
}

// executeBuy executes a buy/open long order
func (s *AIStrategy) executeBuy(
	ctx context.Context,
	decision *entity.Decision,
	snapshot entity.MarketSnapshot,
	position *entity.Position,
) (*entity.TradeEvent, error) {
	if position != nil {
		s.logger.Info("Adding to existing long position")
	} else {
		s.logger.Info("Opening new long position")
	}

	// calculate position size based on risk percent
	budget := entity.NewRiskBudget(decision.RiskPercent)
	positionValue, amount := budget.Allocate(snapshot.QuoteBalance, snapshot.Price())
	if amount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("Calculated position amount is zero; skipping buy execution",
			zap.String("quote_balance", snapshot.QuoteBalance.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Executing AI buy order",
		zap.String("amount", amount.String()),
		zap.String("price", snapshot.Price().String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.String("order_id", orderID))

	if err := s.trader.ExecuteAction(ctx, entity.ActionOpenLong, amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute buy order")
	}

	exitPlan := decision.ExitPlan
	takeProfit := decimal.NewFromFloat(exitPlan.TakeProfitPrice)
	stopLoss := decimal.NewFromFloat(exitPlan.StopLossPrice)

	if takeProfit.GreaterThan(decimal.Zero) || stopLoss.GreaterThan(decimal.Zero) {
		if err := s.trader.SetPositionStops(ctx, s.pair, takeProfit, stopLoss); err != nil {
			s.logger.Warn("Failed to update position stops on exchange", zap.Error(err))
		} else {
			s.logger.Info("Updated position stops",
				zap.String("pair", s.pair.Symbol()),
				zap.String("take_profit", takeProfit.String()),
				zap.String("stop_loss", stopLoss.String()))
		}
	}

	return &entity.TradeEvent{
		Action: entity.ActionOpenLong,
		Amount: amount,
		Pair:   s.pair,
		Price:  snapshot.Price(),
	}, nil
}

// executeSell closes the current long position
func (s *AIStrategy) executeSell(ctx context.Context, currentPrice decimal.Decimal, position *entity.Position) (*entity.TradeEvent, error) {
	if position == nil {
		s.logger.Warn("Received 'sell' action without an open position, treating as HOLD")
		return nil, nil
	}

	if position.Side != entity.PositionSideLong {
		s.logger.Warn("Cannot close long: position is not long",
			zap.String("position_side", position.Side.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Closing long position",
		zap.String("entry_price", position.EntryPrice.String()),
		zap.String("current_price", currentPrice.String()),
		zap.String("amount", position.Amount.String()),
		zap.String("order_id", orderID))

	if err := s.trader.ExecuteAction(ctx, entity.ActionCloseLong, position.Amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute sell order")
	}

	// calculate P&L
	pnl := position.PnL(currentPrice)
	s.logger.Info("Long position closed",
		zap.String("pnl", pnl.String()))

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionCloseLong,
		Amount: position.Amount,
		Pair:   s.pair,
		Price:  currentPrice,
	}

	return tradeEvent, nil
}

// executeOpenShort executes a short position opening order
func (s *AIStrategy) executeOpenShort(
	ctx context.Context,
	decision *entity.Decision,
	snapshot entity.MarketSnapshot,
	position *entity.Position,
) (*entity.TradeEvent, error) {
	if position != nil && position.Side == entity.PositionSideLong {
		s.logger.Warn("Cannot open short: long position already exists")
		return nil, nil
	}

	if position != nil && position.Side == entity.PositionSideShort {
		s.logger.Info("Adding to existing short position")
	} else {
		s.logger.Info("Opening new short position")
	}

	// calculate position size based on risk percent
	budget := entity.NewRiskBudget(decision.RiskPercent)
	positionValue, amount := budget.Allocate(snapshot.QuoteBalance, snapshot.Price())
	if amount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("Calculated position amount is zero; skipping short execution",
			zap.String("quote_balance", snapshot.QuoteBalance.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Executing AI open short order",
		zap.String("amount", amount.String()),
		zap.String("price", snapshot.Price().String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.String("order_id", orderID))

	// use ExecuteAction for opening short
	if err := s.trader.ExecuteAction(ctx, entity.ActionOpenShort, amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute open short order")
	}

	exitPlan := decision.ExitPlan
	takeProfit := decimal.NewFromFloat(exitPlan.TakeProfitPrice)
	stopLoss := decimal.NewFromFloat(exitPlan.StopLossPrice)

	if takeProfit.GreaterThan(decimal.Zero) || stopLoss.GreaterThan(decimal.Zero) {
		if err := s.trader.SetPositionStops(ctx, s.pair, takeProfit, stopLoss); err != nil {
			s.logger.Warn("Failed to update position stops on exchange", zap.Error(err))
		} else {
			s.logger.Info("Updated position stops",
				zap.String("pair", s.pair.Symbol()),
				zap.String("take_profit", takeProfit.String()),
				zap.String("stop_loss", stopLoss.String()))
		}
	}

	return &entity.TradeEvent{
		Action: entity.ActionOpenShort,
		Amount: amount,
		Pair:   s.pair,
		Price:  snapshot.Price(),
	}, nil
}

// executeCloseShort closes the current short position
func (s *AIStrategy) executeCloseShort(ctx context.Context, currentPrice decimal.Decimal, position *entity.Position) (*entity.TradeEvent, error) {
	if position == nil {
		s.logger.Warn("Received 'close_short' action without an open position")
		return nil, nil
	}

	if position.Side != entity.PositionSideShort {
		s.logger.Warn("Cannot close short: position is not short",
			zap.String("position_side", position.Side.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Closing short position",
		zap.String("entry_price", position.EntryPrice.String()),
		zap.String("current_price", currentPrice.String()),
		zap.String("amount", position.Amount.String()),
		zap.String("order_id", orderID))

	// use ExecuteAction for closing short (buy to close)
	if err := s.trader.ExecuteAction(ctx, entity.ActionCloseShort, position.Amount, orderID); err != nil {
		return nil, errors.Wrap(err, "failed to execute close short order")
	}

	// calculate P&L (for short: profit when price goes down)
	pnl := position.PnL(currentPrice)
	s.logger.Info("Short position closed",
		zap.String("pnl", pnl.String()))

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionCloseShort,
		Amount: position.Amount,
		Pair:   s.pair,
		Price:  currentPrice,
	}

	return tradeEvent, nil
}

// Close performs cleanup when the strategy is shut down
func (s *AIStrategy) Close() error {
	s.logger.Info("Closing AI strategy")
	return nil
}
