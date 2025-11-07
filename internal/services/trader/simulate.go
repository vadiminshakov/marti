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

// Pricer defines an interface for getting the price of a trading pair.
type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// SimulateTrader is a simple spot/margin simulator.
type SimulateTrader struct {
	mu       sync.RWMutex
	pair     entity.Pair
	logger   *zap.Logger
	wallet   map[string]decimal.Decimal
	orders   map[string]orderInfo
	position *entity.Position
	pricer   Pricer
}

type orderInfo struct {
	amount decimal.Decimal
	side   string
}

// NewSimulateTrader creates a new SimulateTrader.
func NewSimulateTrader(pair entity.Pair, logger *zap.Logger, pricer Pricer) (*SimulateTrader, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if pricer == nil {
		return nil, errors.New("pricer is required for SimulateTrader")
	}
	wallet := map[string]decimal.Decimal{pair.From: decimal.Zero, pair.To: decimal.NewFromInt(10000)}
	logger.Info("simulate init", zap.String("pair", pair.String()), zap.String("base", wallet[pair.From].String()), zap.String("quote", wallet[pair.To].String()))
	return &SimulateTrader{pair: pair, logger: logger, wallet: wallet, orders: make(map[string]orderInfo), pricer: pricer}, nil
}

// Buy simulates a market buy order, fetching the price from its pricer.
// The 'amount' is expected to be in the base currency (e.g., BTC for BTC/USDT).
func (t *SimulateTrader) Buy(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	price, err := t.pricer.GetPrice(ctx, t.pair)
	if err != nil {
		return errors.Wrap(err, "failed to get price for simulated buy")
	}

	quoteAmount := amount.Mul(price)

	if t.wallet[t.pair.To].LessThan(quoteAmount) {
		return errors.Errorf("insufficient %s balance: have %s need %s", t.pair.To, t.wallet[t.pair.To].String(), quoteAmount.String())
	}

	t.wallet[t.pair.To] = t.wallet[t.pair.To].Sub(quoteAmount)
	t.wallet[t.pair.From] = t.wallet[t.pair.From].Add(amount)

	if t.position == nil {
		pos, err := entity.NewPositionFromExternalSnapshot(amount, price, time.Now())
		if err != nil {
			return errors.Wrap(err, "create position")
		}
		t.position = pos
	} else {
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
		zap.String("price", price.String()))
	return nil
}

// Sell simulates a market sell order, fetching the price from its pricer.
// The 'amount' is expected to be in the base currency (e.g., BTC for BTC/USDT).
func (t *SimulateTrader) Sell(ctx context.Context, amount decimal.Decimal, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	price, err := t.pricer.GetPrice(ctx, t.pair)
	if err != nil {
		return errors.Wrap(err, "failed to get price for simulated sell")
	}

	if t.wallet[t.pair.From].LessThan(amount) {
		return fmt.Errorf("insufficient %s balance: have %s need %s", t.pair.From, t.wallet[t.pair.From].String(), amount.String())
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

	t.orders[id] = orderInfo{amount: amount, side: "sell"}
	t.logger.Info("Simulated sell executed",
		zap.String("id", id),
		zap.String("amount", amount.String()),
		zap.String("price", price.String()))
	return nil
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
