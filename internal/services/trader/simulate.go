package trader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"go.uber.org/zap"
)

// SimulateTrader — простой спот симулятор.
type SimulateTrader struct {
	mu       sync.RWMutex
	pair     entity.Pair
	logger   *zap.Logger
	wallet   map[string]decimal.Decimal
	orders   map[string]orderInfo
	position *entity.Position
}

type orderInfo struct {
	amount decimal.Decimal
	side   string
}

func NewSimulateTrader(pair entity.Pair, logger *zap.Logger) (*SimulateTrader, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	wallet := map[string]decimal.Decimal{pair.From: decimal.Zero, pair.To: decimal.NewFromInt(10000)}
	logger.Info("simulate init", zap.String("pair", pair.String()), zap.String("base", wallet[pair.From].String()), zap.String("quote", wallet[pair.To].String()))
	return &SimulateTrader{pair: pair, logger: logger, wallet: wallet, orders: make(map[string]orderInfo)}, nil
}

func (t *SimulateTrader) Buy(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.orders[id] = orderInfo{amount: amount, side: "buy"}
	t.logger.Info("order buy", zap.String("id", id), zap.String("amount", amount.String()))
	return nil
}

func (t *SimulateTrader) Sell(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wallet[t.pair.From].LessThan(amount) {
		return fmt.Errorf("insufficient %s balance: have %s need %s", t.pair.From, t.wallet[t.pair.From].String(), amount.String())
	}
	t.orders[id] = orderInfo{amount: amount, side: "sell"}
	t.logger.Info("order sell", zap.String("id", id), zap.String("amount", amount.String()))
	return nil
}

func (t *SimulateTrader) OrderExecuted(ctx context.Context, id string) (bool, decimal.Decimal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	o, ok := t.orders[id]
	if !ok {
		t.logger.Warn("missing order assume executed", zap.String("id", id))
		return true, decimal.Zero, nil
	}
	return true, o.amount, nil
}

func (t *SimulateTrader) ApplyTrade(price decimal.Decimal, amount decimal.Decimal, side string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch side {
	case "buy":
		if t.wallet[t.pair.To].LessThan(amount) {
			return errors.Errorf("insufficient %s balance: have %s need %s", t.pair.To, t.wallet[t.pair.To].String(), amount.String())
		}
		baseAdded := amount.Div(price)
		t.wallet[t.pair.To] = t.wallet[t.pair.To].Sub(amount)
		t.wallet[t.pair.From] = t.wallet[t.pair.From].Add(baseAdded)
		if t.position == nil {
			pos, err := entity.NewPositionFromExternalSnapshot(baseAdded, price, time.Now())
			if err != nil {
				return errors.Wrap(err, "create position")
			}
			t.position = pos
		} else {
			totalBase := t.position.Amount.Add(baseAdded)
			if totalBase.GreaterThan(decimal.Zero) {
				existingNotional := t.position.EntryPrice.Mul(t.position.Amount)
				addedNotional := baseAdded.Mul(price)
				t.position.Amount = totalBase
				t.position.EntryPrice = existingNotional.Add(addedNotional).Div(totalBase)
			}
		}
		return nil
	case "sell":
		if t.wallet[t.pair.From].LessThan(amount) {
			return errors.Errorf("insufficient %s balance: have %s need %s", t.pair.From, t.wallet[t.pair.From].String(), amount.String())
		}
		quoteReceived := amount.Mul(price)
		t.wallet[t.pair.From] = t.wallet[t.pair.From].Sub(amount)
		t.wallet[t.pair.To] = t.wallet[t.pair.To].Add(quoteReceived)
		if t.position != nil {
			remaining := t.position.Amount.Sub(amount)
			if remaining.LessThanOrEqual(decimal.Zero) {
				t.position = nil
			} else {
				t.position.Amount = remaining
			}
		}
		return nil
	default:
		return errors.Errorf("unknown side: %s", side)
	}
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
