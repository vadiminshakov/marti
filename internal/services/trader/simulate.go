package trader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/storage/simstate"
	"go.uber.org/zap"
)

// Pricer defines an interface for getting the price of a trading pair.
type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// SimulateTrader is a simple spot/margin simulator.
type SimulateTrader struct {
	mu         sync.RWMutex
	pair       entity.Pair
	logger     *zap.Logger
	wallet     map[string]decimal.Decimal
	orders     map[string]orderInfo
	position   *entity.Position
	pricer     Pricer
	leverage   int
	marketType entity.MarketType
	// marginUsed tracks how much quote-side collateral is currently locked
	// for open margin positions so that we can release the same amount plus
	// realised PnL when the position is unwound.
	marginUsed decimal.Decimal
	stateStore *simstate.Store
}

type orderInfo struct {
	amount decimal.Decimal
	side   string
}

// NewSimulateTrader creates a new SimulateTrader.
func NewSimulateTrader(pair entity.Pair, marketType entity.MarketType, leverage int, logger *zap.Logger, pricer Pricer) (*SimulateTrader, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if pricer == nil {
		return nil, errors.New("pricer is required for SimulateTrader")
	}
	if leverage < 1 {
		leverage = 1
	}
	wallet := map[string]decimal.Decimal{pair.From: decimal.Zero, pair.To: decimal.NewFromInt(10000)}
	stateStore, err := simstate.NewStore(pair)
	if err != nil {
		return nil, errors.Wrap(err, "init simulate state store")
	}
	trader := &SimulateTrader{
		pair:       pair,
		logger:     logger,
		wallet:     wallet,
		orders:     make(map[string]orderInfo),
		pricer:     pricer,
		leverage:   leverage,
		marketType: marketType,
		marginUsed: decimal.Zero,
		stateStore: stateStore,
	}
	if err := trader.restoreState(); err != nil {
		logger.Warn("failed to restore simulate state", zap.Error(err))
	}
	logger.Info("simulate init",
		zap.String("pair", pair.String()),
		zap.String("base", trader.wallet[pair.From].String()),
		zap.String("quote", trader.wallet[pair.To].String()),
		zap.String("market_type", string(marketType)),
		zap.Int("leverage", leverage))
	return trader, nil
}

// ExecuteAction executes a trading action.
func (t *SimulateTrader) ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error {
	switch action {
	case entity.ActionOpenLong:
		return t.buy(ctx, amount, clientOrderID)
	case entity.ActionCloseLong:
		return t.sell(ctx, amount, clientOrderID)
	case entity.ActionOpenShort:
		if t.marketType != entity.MarketTypeMargin {
			return fmt.Errorf("short positions are supported only in margin trading mode")
		}
		return t.sell(ctx, amount, clientOrderID)
	case entity.ActionCloseShort:
		if t.marketType != entity.MarketTypeMargin {
			return fmt.Errorf("short positions are supported only in margin trading mode")
		}
		return t.buy(ctx, amount, clientOrderID)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (t *SimulateTrader) OrderExecuted(ctx context.Context, id string) (bool, decimal.Decimal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	o, ok := t.orders[id]
	if !ok {
		t.logger.Warn("missing order, assuming executed for simulation purposes", zap.String("id", id))
		return true, decimal.Zero, nil
	}
	return true, o.amount, nil
}

func (t *SimulateTrader) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.wallet[currency], nil
}

func (t *SimulateTrader) GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if pair != t.pair || t.position == nil {
		return nil, nil
	}
	clone := *t.position
	return &clone, nil
}

func (t *SimulateTrader) SetPositionStops(ctx context.Context, pair entity.Pair, tp, sl decimal.Decimal) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pair != t.pair || t.position == nil {
		return nil
	}
	if tp.GreaterThan(decimal.Zero) {
		t.position.TakeProfit = tp
	} else {
		t.position.TakeProfit = decimal.Zero
	}
	if sl.GreaterThan(decimal.Zero) {
		t.position.StopLoss = sl
	} else {
		t.position.StopLoss = decimal.Zero
	}
	t.persist()
	return nil
}

func (t *SimulateTrader) UnrealizedPnL(ctx context.Context, price decimal.Decimal) decimal.Decimal {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.position == nil {
		return decimal.Zero
	}
	return t.position.PnL(price)
}

// buy simulates a market buy order, fetching the price from its pricer.
// The 'amount' is expected to be in the base currency (e.g., BTC for BTC/USDT).
func (t *SimulateTrader) buy(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("buy amount must be positive, got %s", amount.String())
	}

	price, err := t.pricer.GetPrice(ctx, t.pair)
	if err != nil {
		return errors.Wrap(err, "failed to get price for simulated buy")
	}

	var actionErr error
	if t.position != nil && t.position.Side == entity.PositionSideShort {
		actionErr = t.closeShort(amount, id, price)
	} else {
		actionErr = t.openOrAddLong(amount, id, price)
	}
	if actionErr == nil {
		t.persist()
	}

	return actionErr
}

// sell simulates a market sell order, fetching the price from its pricer.
// The 'amount' is expected to be in the base currency (e.g., BTC for BTC/USDT).
func (t *SimulateTrader) sell(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("sell amount must be positive, got %s", amount.String())
	}

	price, err := t.pricer.GetPrice(ctx, t.pair)
	if err != nil {
		return errors.Wrap(err, "failed to get price for simulated sell")
	}

	var actionErr error
	if t.position != nil && t.position.Side == entity.PositionSideLong {
		actionErr = t.closeLong(amount, id, price)
	} else if t.marketType == entity.MarketTypeMargin {
		actionErr = t.openOrAddShort(amount, id, price)
	} else {
		actionErr = t.sellSpotWithoutPosition(amount, id, price)
	}
	if actionErr == nil {
		t.persist()
	}
	return actionErr
}

func (t *SimulateTrader) openOrAddLong(amount decimal.Decimal, id string, price decimal.Decimal) error {
	quoteAmount := amount.Mul(price)
	requiredQuote := t.requiredQuoteAmount(quoteAmount)

	if t.wallet[t.pair.To].LessThan(requiredQuote) {
		return errors.Errorf("insufficient %s balance: have %s need %s (with %dx leverage)",
			t.pair.To,
			t.wallet[t.pair.To].String(),
			requiredQuote.String(),
			t.leverage)
	}

	t.wallet[t.pair.To] = t.wallet[t.pair.To].Sub(requiredQuote)
	if t.marketType == entity.MarketTypeMargin {
		t.marginUsed = t.marginUsed.Add(requiredQuote)
	}
	t.wallet[t.pair.From] = t.wallet[t.pair.From].Add(amount)

	if t.position == nil {
		pos, err := entity.NewPositionFromExternalSnapshot(amount, price, time.Now(), entity.PositionSideLong)
		if err != nil {
			return errors.Wrap(err, "create position")
		}
		t.position = pos
	} else {
		if t.position.Side != entity.PositionSideLong {
			return fmt.Errorf("cannot open long while %s position is active", t.position.Side.String())
		}
		totalBase := t.position.Amount.Add(amount)
		if totalBase.GreaterThan(decimal.Zero) {
			existingNotional := t.position.EntryPrice.Mul(t.position.Amount)
			addedNotional := amount.Mul(price)
			t.position.Amount = totalBase
			t.position.EntryPrice = existingNotional.Add(addedNotional).Div(totalBase)
		}
	}

	t.orders[id] = orderInfo{amount: amount, side: "buy"}
	t.logger.Info("Simulated buy executed",
		zap.String("id", id),
		zap.String("amount", amount.String()),
		zap.String("price", price.String()),
		zap.String("context", "open_long"))
	return nil
}

func (t *SimulateTrader) closeLong(amount decimal.Decimal, id string, price decimal.Decimal) error {
	if t.position == nil || t.position.Side != entity.PositionSideLong {
		return fmt.Errorf("no long position to close")
	}

	closeAmount := amount
	if closeAmount.GreaterThan(t.position.Amount) {
		closeAmount = t.position.Amount
		t.logger.Warn("requested close amount exceeds long position size, capping to position amount",
			zap.String("requested", amount.String()),
			zap.String("position", t.position.Amount.String()))
	}
	if closeAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("close amount must be positive, got %s", closeAmount.String())
	}

	if t.wallet[t.pair.From].LessThan(closeAmount) {
		return fmt.Errorf("insufficient %s balance: have %s need %s",
			t.pair.From,
			t.wallet[t.pair.From].String(),
			closeAmount.String())
	}

	t.wallet[t.pair.From] = t.wallet[t.pair.From].Sub(closeAmount)

	prevAmount := t.position.Amount
	entryPrice := t.position.EntryPrice

	if t.marketType == entity.MarketTypeMargin {
		fraction := closeAmount.Div(prevAmount)
		one := decimal.NewFromInt(1)
		if fraction.GreaterThan(one) {
			fraction = one
		}
		marginReleased := t.marginUsed.Mul(fraction)
		realizedPnl := price.Sub(entryPrice).Mul(closeAmount)
		t.marginUsed = t.marginUsed.Sub(marginReleased)
		if t.marginUsed.LessThan(decimal.Zero) {
			t.marginUsed = decimal.Zero
		}
		t.wallet[t.pair.To] = t.wallet[t.pair.To].Add(marginReleased.Add(realizedPnl))
	} else {
		quoteReceived := closeAmount.Mul(price)
		t.wallet[t.pair.To] = t.wallet[t.pair.To].Add(quoteReceived)
	}

	t.position.Amount = t.position.Amount.Sub(closeAmount)
	if t.position.Amount.LessThanOrEqual(decimal.Zero) {
		t.position = nil
		t.marginUsed = decimal.Zero
	}

	t.orders[id] = orderInfo{amount: closeAmount, side: "sell"}
	t.logger.Info("Simulated sell executed",
		zap.String("id", id),
		zap.String("amount", closeAmount.String()),
		zap.String("price", price.String()),
		zap.String("context", "close_long"))
	return nil
}

func (t *SimulateTrader) openOrAddShort(amount decimal.Decimal, id string, price decimal.Decimal) error {
	if t.marketType != entity.MarketTypeMargin {
		return fmt.Errorf("short positions are supported only in margin trading mode")
	}

	quoteAmount := amount.Mul(price)
	requiredQuote := t.requiredQuoteAmount(quoteAmount)

	if t.wallet[t.pair.To].LessThan(requiredQuote) {
		return errors.Errorf("insufficient %s balance for short: have %s need %s (with %dx leverage)",
			t.pair.To,
			t.wallet[t.pair.To].String(),
			requiredQuote.String(),
			t.leverage)
	}

	if t.position != nil && t.position.Side == entity.PositionSideLong {
		return fmt.Errorf("cannot open short while long position is active")
	}

	t.wallet[t.pair.To] = t.wallet[t.pair.To].Sub(requiredQuote)
	t.marginUsed = t.marginUsed.Add(requiredQuote)
	t.wallet[t.pair.From] = t.wallet[t.pair.From].Sub(amount)

	if t.position == nil {
		pos, err := entity.NewPositionFromExternalSnapshot(amount, price, time.Now(), entity.PositionSideShort)
		if err != nil {
			return errors.Wrap(err, "create short position")
		}
		t.position = pos
	} else {
		totalBase := t.position.Amount.Add(amount)
		existingNotional := t.position.EntryPrice.Mul(t.position.Amount)
		addedNotional := amount.Mul(price)
		t.position.Amount = totalBase
		t.position.EntryPrice = existingNotional.Add(addedNotional).Div(totalBase)
	}

	t.orders[id] = orderInfo{amount: amount, side: "sell"}
	t.logger.Info("Simulated sell executed",
		zap.String("id", id),
		zap.String("amount", amount.String()),
		zap.String("price", price.String()),
		zap.String("context", "open_short"))
	return nil
}

func (t *SimulateTrader) closeShort(amount decimal.Decimal, id string, price decimal.Decimal) error {
	if t.position == nil || t.position.Side != entity.PositionSideShort {
		return fmt.Errorf("no short position to close")
	}

	closeAmount := amount
	if closeAmount.GreaterThan(t.position.Amount) {
		closeAmount = t.position.Amount
		t.logger.Warn("requested close amount exceeds short position size, capping to position amount",
			zap.String("requested", amount.String()),
			zap.String("position", t.position.Amount.String()))
	}
	if closeAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("close amount must be positive, got %s", closeAmount.String())
	}

	t.wallet[t.pair.From] = t.wallet[t.pair.From].Add(closeAmount)

	prevAmount := t.position.Amount
	entryPrice := t.position.EntryPrice
	one := decimal.NewFromInt(1)
	fraction := closeAmount.Div(prevAmount)
	if fraction.GreaterThan(one) {
		fraction = one
	}
	marginReleased := t.marginUsed.Mul(fraction)
	realizedPnl := entryPrice.Sub(price).Mul(closeAmount)
	t.marginUsed = t.marginUsed.Sub(marginReleased)
	if t.marginUsed.LessThan(decimal.Zero) {
		t.marginUsed = decimal.Zero
	}
	t.wallet[t.pair.To] = t.wallet[t.pair.To].Add(marginReleased.Add(realizedPnl))

	t.position.Amount = t.position.Amount.Sub(closeAmount)
	if t.position.Amount.LessThanOrEqual(decimal.Zero) {
		t.position = nil
		t.marginUsed = decimal.Zero
	}

	t.orders[id] = orderInfo{amount: closeAmount, side: "buy"}
	t.logger.Info("Simulated buy executed",
		zap.String("id", id),
		zap.String("amount", closeAmount.String()),
		zap.String("price", price.String()),
		zap.String("context", "close_short"))
	return nil
}

func (t *SimulateTrader) sellSpotWithoutPosition(amount decimal.Decimal, id string, price decimal.Decimal) error {
	if t.wallet[t.pair.From].LessThan(amount) {
		return fmt.Errorf("insufficient %s balance: have %s need %s",
			t.pair.From,
			t.wallet[t.pair.From].String(),
			amount.String())
	}

	quoteReceived := amount.Mul(price)
	t.wallet[t.pair.From] = t.wallet[t.pair.From].Sub(amount)
	t.wallet[t.pair.To] = t.wallet[t.pair.To].Add(quoteReceived)

	t.orders[id] = orderInfo{amount: amount, side: "sell"}
	t.logger.Info("Simulated sell executed",
		zap.String("id", id),
		zap.String("amount", amount.String()),
		zap.String("price", price.String()),
		zap.String("context", "spot"))
	return nil
}

func (t *SimulateTrader) requiredQuoteAmount(notional decimal.Decimal) decimal.Decimal {
	if t.marketType != entity.MarketTypeMargin {
		return notional
	}
	leverage := t.leverage
	if leverage < 1 {
		leverage = 1
	}
	return notional.Div(decimal.NewFromInt(int64(leverage)))
}

func (t *SimulateTrader) restoreState() error {
	if t.stateStore == nil {
		return nil
	}
	state, err := t.stateStore.Load()
	if err != nil || state == nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	wallet := make(map[string]decimal.Decimal, len(t.wallet)+len(state.Wallet))
	for currency, balance := range t.wallet {
		wallet[currency] = balance
	}
	for currency, balanceStr := range state.Wallet {
		if balanceStr == "" {
			wallet[currency] = decimal.Zero
			continue
		}
		parsed, err := decimal.NewFromString(balanceStr)
		if err != nil {
			return errors.Wrapf(err, "decode %s balance", currency)
		}
		wallet[currency] = parsed
	}
	t.wallet = wallet

	margin := decimal.Zero
	if state.MarginUsed != "" {
		margin, err = decimal.NewFromString(state.MarginUsed)
		if err != nil {
			return errors.Wrap(err, "decode margin used")
		}
	}
	t.marginUsed = margin

	if state.Position != nil {
		pos, err := state.Position.ToPosition()
		if err != nil {
			return err
		}
		t.position = pos
	} else {
		t.position = nil
	}

	return nil
}

func (t *SimulateTrader) persist() {
	if t.stateStore == nil {
		return
	}

	state := simstate.State{
		Pair:       t.pair.String(),
		Wallet:     make(map[string]string, len(t.wallet)),
		MarginUsed: t.marginUsed.String(),
	}
	for currency, balance := range t.wallet {
		state.Wallet[currency] = balance.String()
	}
	if t.position != nil {
		state.Position = simstate.NewStoredPosition(t.position)
	}

	if err := t.stateStore.Save(state); err != nil {
		t.logger.Warn("failed to persist simulate state", zap.Error(err))
	}
}
