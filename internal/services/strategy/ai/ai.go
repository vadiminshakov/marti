// Package ai implements an LLM-driven trading strategy.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
	"go.uber.org/zap"
)

// defaultHigherTimeframeLookback is used when not provided explicitly
const defaultHigherTimeframeLookback = 60

// tradersvc abstracts the exchange operations.
type tradersvc interface {
	ExecuteAction(ctx context.Context, action domain.Action, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair domain.Pair) (*domain.Position, error)
	SetPositionStops(ctx context.Context, pair domain.Pair, takeProfit, stopLoss decimal.Decimal) error
}

type pricer interface {
	GetPrice(ctx context.Context, pair domain.Pair) (decimal.Decimal, error)
}

type llmClient interface {
	GetDecision(ctx context.Context, prompt *domain.Prompt) (string, error)
}

type marketDataCollector interface {
	FetchTimeframeData(ctx context.Context, interval string, lookback int) (*domain.Timeframe, error)
}

type aiDecisionWriter interface {
	Save(event domain.AIDecisionEvent) error
}

// AIStrategy executes margin trades.
type AIStrategy struct {
	pair             domain.Pair
	marketType       domain.MarketType
	llmClient        llmClient
	marketData       marketDataCollector
	pricer           pricer
	trader           tradersvc
	logger           *zap.Logger
	primaryTimeframe string
	primaryLookback  int
	higherTimeframe  string
	higherLookback   int
	decisionStore    aiDecisionWriter
	modelName        string
}

// NewAIStrategy constructs an AI strategy instance.
func NewAIStrategy(
	logger *zap.Logger,
	pair domain.Pair,
	marketType domain.MarketType,
	llmClient llmClient,
	marketData marketDataCollector,
	pricer pricer,
	tradeSvc tradersvc,
	primaryTimeframe string,
	higherTimeframe string,
	primaryLookback int,
	higherLookback int,
	decisionStore aiDecisionWriter,
	modelName string,
) (*AIStrategy, error) {
	if marketType != domain.MarketTypeMargin {
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
		decisionStore:    decisionStore,
		modelName:        modelName,
	}, nil
}

// stub
func (s *AIStrategy) Initialize(ctx context.Context) error {
	return nil
}

// Trade performs one AI evaluation.
func (s *AIStrategy) Trade(ctx context.Context) (*domain.TradeEvent, error) {
	// collect market data and indicators
	snapshot, position, err := s.gatherMarketData(ctx)
	if err != nil {
		return nil, err
	}

	s.logMarketState(snapshot, position)

	// get decision from LLM
	decision, err := s.getAndValidateDecision(ctx, snapshot, position)
	if err != nil {
		return nil, err
	}

	// save decision to WAL
	if err := s.saveDecision(decision, snapshot, position); err != nil {
		return nil, errors.Wrap(err, "failed to save AI decision")
	}

	// execute decision
	return s.executeDecision(ctx, decision, snapshot, position)
}

func (s *AIStrategy) gatherMarketData(ctx context.Context) (domain.MarketSnapshot, *domain.Position, error) {
	primaryFrame, err := s.fetchPrimaryTimeframe(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, nil, err
	}

	higherTimeframeData := s.fetchHigherTimeframe(ctx)

	quoteBalance, position, err := s.fetchAccountState(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, nil, err
	}

	volumeAnalysis := domain.NewVolumeAnalysis(primaryFrame.Candles)

	snapshot := domain.MarketSnapshot{
		PrimaryTimeFrame: primaryFrame,
		HigherTimeFrame:  higherTimeframeData,
		QuoteBalance:     quoteBalance,
		VolumeAnalysis:   volumeAnalysis,
	}

	return snapshot, position, nil
}

func (s *AIStrategy) fetchPrimaryTimeframe(ctx context.Context) (*domain.Timeframe, error) {
	frame, err := s.marketData.FetchTimeframeData(ctx, s.primaryTimeframe, s.primaryLookback)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get market data")
	}

	if frame == nil || len(frame.Candles) == 0 {
		return nil, errors.New("insufficient market data")
	}

	if len(frame.Indicators) == 0 || frame.Summary == nil {
		return nil, errors.New("insufficient indicator data for primary timeframe")
	}

	return frame, nil
}

func (s *AIStrategy) fetchHigherTimeframe(ctx context.Context) *domain.Timeframe {
	data, err := s.marketData.FetchTimeframeData(ctx, s.higherTimeframe, s.higherLookback)
	if err != nil {
		s.logger.Warn("Failed to get higher timeframe data, continuing without it",
			zap.Error(err),
			zap.String("timeframe", s.higherTimeframe))
		return nil
	}

	return data
}

func (s *AIStrategy) fetchAccountState(ctx context.Context) (decimal.Decimal, *domain.Position, error) {
	quoteBalance, err := s.trader.GetBalance(ctx, s.pair.To)
	if err != nil {
		return decimal.Zero, nil, errors.Wrap(err, "failed to get margin balance")
	}

	position, err := s.trader.GetPosition(ctx, s.pair)
	if err != nil {
		return decimal.Zero, nil, errors.Wrap(err, "failed to get position")
	}

	return quoteBalance, position, nil
}

func (s *AIStrategy) logMarketState(snapshot domain.MarketSnapshot, position *domain.Position) {
	currentPrice := snapshot.Price()
	logFields := []zap.Field{
		zap.String("price", currentPrice.StringFixed(2)),
		zap.String(s.pair.To+"_balance", snapshot.QuoteBalance.StringFixed(2)),
	}

	if position != nil {
		logFields = append(logFields,
			zap.String("position_amount", position.Amount.StringFixed(8)),
			zap.String("position_entry", position.EntryPrice.StringFixed(2)),
			zap.String("position_pnl", position.PnL(currentPrice).StringFixed(2)))
	}

	s.logger.Info("Market analysis", logFields...)
}

func (s *AIStrategy) getAndValidateDecision(
	ctx context.Context,
	snapshot domain.MarketSnapshot,
	position *domain.Position,
) (*domain.Decision, error) {
	response, err := s.llmClient.GetDecision(ctx, s.buildPrompt(snapshot, position))
	if err != nil {
		s.logger.Error("Failed to get AI response", zap.Error(err))
		return nil, errors.Wrap(err, "failed to get AI decision")
	}

	decision, err := domain.NewDecision(response)
	if err != nil {
		s.logger.Error("Decision validation failed",
			zap.Error(err),
			zap.String("response", response))
		return nil, errors.Wrap(err, "failed to parse AI decision")
	}

	s.logger.Info("Decision validation passed",
		zap.String("model", s.modelName),
		zap.String("action", decision.Action),
		zap.Float64("risk_percent", decision.RiskPercent))

	decisionFields := []zap.Field{
		zap.String("model", s.modelName),
		zap.String("action", strings.ToUpper(decision.Action)),
		zap.String("reasoning", decision.Reasoning),
	}

	action := decision.ToAction()
	if action == domain.ActionOpenLong || action == domain.ActionOpenShort {
		decisionFields = append(decisionFields,
			zap.Float64("risk_percent", decision.RiskPercent))
	}

	s.logger.Info("AI decision", decisionFields...)

	return decision, nil
}

// buildPrompt assembles the prompt entity for LLM.
func (s *AIStrategy) buildPrompt(snapshot domain.MarketSnapshot, position *domain.Position) *domain.Prompt {
	return domain.NewPrompt(s.pair).WithMarketContext(
		snapshot.PrimaryTimeFrame,
		snapshot.HigherTimeFrame,
		snapshot.VolumeAnalysis,
		position,
		snapshot.QuoteBalance,
	)
}

// executeDecision routes decision to handler.
func (s *AIStrategy) executeDecision(
	ctx context.Context,
	decision *domain.Decision,
	snapshot domain.MarketSnapshot,
	position *domain.Position,
) (*domain.TradeEvent, error) {
	action := decision.ToAction()

	switch action {
	case domain.ActionOpenLong:
		return s.executeEntry(ctx, domain.PositionSideLong, decision, snapshot, position)
	case domain.ActionCloseLong:
		return s.executeExit(ctx, domain.PositionSideLong, position, snapshot.Price())
	case domain.ActionOpenShort:
		return s.executeEntry(ctx, domain.PositionSideShort, decision, snapshot, position)
	case domain.ActionCloseShort:
		return s.executeExit(ctx, domain.PositionSideShort, position, snapshot.Price())
	default:
		return nil, fmt.Errorf("unknown action: %v", action)
	}
}

// executeEntry opens or adds to a position.
func (s *AIStrategy) executeEntry(
	ctx context.Context,
	side domain.PositionSide,
	decision *domain.Decision,
	snapshot domain.MarketSnapshot,
	position *domain.Position,
) (*domain.TradeEvent, error) {
	// validate position side
	if position != nil && position.Side != side {
		s.logger.Warn("Cannot open position: opposite position already exists",
			zap.String("existing_side", position.Side.String()),
			zap.String("requested_side", side.String()))
		return nil, nil
	}

	if position != nil {
		s.logger.Info("Adding to existing position",
			zap.String("model", s.modelName),
			zap.String("side", side.String()))
	} else {
		s.logger.Info("Opening new position",
			zap.String("model", s.modelName),
			zap.String("side", side.String()))
	}

	// calculate position size based on risk percent
	budget := domain.NewRiskBudget(decision.RiskPercent)
	positionValue, amount := budget.Allocate(snapshot.QuoteBalance, snapshot.Price())
	if amount.LessThanOrEqual(decimal.Zero) {
		s.logger.Warn("Calculated position amount is zero; skipping execution",
			zap.String("quote_balance", snapshot.QuoteBalance.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Executing AI entry order",
		zap.String("model", s.modelName),
		zap.String("side", side.String()),
		zap.String("amount", amount.String()),
		zap.String("price", snapshot.Price().String()),
		zap.String("position_value", positionValue.StringFixed(2)),
		zap.String("order_id", orderID))

	action := decision.ToAction()

	if err := s.trader.ExecuteAction(ctx, action, amount, orderID); err != nil {
		return nil, errors.Wrapf(err, "failed to execute %s order", action)
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

	return &domain.TradeEvent{
		Action: action,
		Amount: amount,
		Pair:   s.pair,
		Price:  snapshot.Price(),
	}, nil
}

// executeExit closes a position.
func (s *AIStrategy) executeExit(
	ctx context.Context,
	side domain.PositionSide,
	position *domain.Position,
	currentPrice decimal.Decimal,
) (*domain.TradeEvent, error) {
	if position == nil {
		s.logger.Warn("Received exit action without an open position, treating as HOLD")
		return nil, nil
	}

	if position.Side != side {
		s.logger.Warn("Cannot close position: position side mismatch",
			zap.String("position_side", position.Side.String()),
			zap.String("requested_side", side.String()))
		return nil, nil
	}

	orderID := uuid.New().String()

	s.logger.Info("Closing position",
		zap.String("model", s.modelName),
		zap.String("side", side.String()),
		zap.String("entry_price", position.EntryPrice.String()),
		zap.String("current_price", currentPrice.String()),
		zap.String("amount", position.Amount.String()),
		zap.String("order_id", orderID))

	var action domain.Action
	if side == domain.PositionSideLong {
		action = domain.ActionCloseLong
	} else {
		action = domain.ActionCloseShort
	}

	if err := s.trader.ExecuteAction(ctx, action, position.Amount, orderID); err != nil {
		return nil, errors.Wrapf(err, "failed to execute %s order", action)
	}

	// calculate P&L
	pnl := position.PnL(currentPrice)
	s.logger.Info("Position closed",
		zap.String("model", s.modelName),
		zap.String("side", side.String()),
		zap.String("pnl", pnl.String()))

	return &domain.TradeEvent{
		Action: action,
		Amount: position.Amount,
		Pair:   s.pair,
		Price:  currentPrice,
	}, nil
}

// saveDecision persists the AI decision event to WAL.
func (s *AIStrategy) saveDecision(decision *domain.Decision, snapshot domain.MarketSnapshot, position *domain.Position) error {
	if s.decisionStore == nil {
		return nil
	}

	var positionAmount, positionSide, positionEntryPrice string
	if position != nil {
		positionAmount = position.Amount.String()
		positionSide = position.Side.String()
		positionEntryPrice = position.EntryPrice.String()
	}

	event := domain.NewAIDecisionEvent(
		time.Now().UTC(),
		s.pair.String(),
		s.modelName,
		decision.Action,
		decision.Reasoning,
		decision.RiskPercent,
		decision.ExitPlan.TakeProfitPrice,
		decision.ExitPlan.StopLossPrice,
		decision.ExitPlan.InvalidationCondition,
		snapshot.Price().String(),
		snapshot.QuoteBalance.String(),
		positionAmount,
		positionSide,
		positionEntryPrice,
	)

	return s.decisionStore.Save(event)
}

// Close logs shutdown.
func (s *AIStrategy) Close() error {
	s.logger.Info("Closing AI strategy")
	return nil
}
