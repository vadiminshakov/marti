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

// SimulationTrader is an interface for traders that support simulation mode
// This allows the strategy to apply virtual trades without modifying the main Trader interface
type SimulationTrader interface {
	ApplyTrade(price decimal.Decimal, amount decimal.Decimal, side string) error
}

// SimulateTrader executes virtual trades with internal balance tracking
type SimulateTrader struct {
	mu sync.RWMutex

	pair   entity.Pair
	logger *zap.Logger

	// Virtual balances for simulation
	balances map[string]decimal.Decimal

	// Track executed orders
	executedOrders map[string]orderInfo

	// Simulated open position (long-only)
	position *entity.Position
}

type orderInfo struct {
	amount decimal.Decimal
	side   string // "buy" or "sell"
}

// NewSimulateTrader creates a new simulate trader with initial virtual balance
func NewSimulateTrader(pair entity.Pair, logger *zap.Logger) (*SimulateTrader, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Initialize with default virtual balance
	balances := make(map[string]decimal.Decimal)
	balances[pair.From] = decimal.Zero            // e.g., BTC: 0
	balances[pair.To] = decimal.NewFromInt(10000) // e.g., USDT: 10000

	logger.Info("Initialized simulate trader",
		zap.String("pair", pair.String()),
		zap.String("initial_balance_quote", balances[pair.To].String()),
		zap.String("initial_balance_base", balances[pair.From].String()),
	)

	return &SimulateTrader{
		pair:           pair,
		logger:         logger,
		balances:       balances,
		executedOrders: make(map[string]orderInfo),
		position:       nil,
	}, nil
}

// Buy simulates a buy order by updating virtual balances
func (t *SimulateTrader) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Get current price from context or use a placeholder
	// In real execution, the strategy will provide the price
	// For simulation, we'll validate balance in the quote currency (e.g., USDT)

	// For market buy, amount is in base currency (e.g., BTC amount to buy)
	// We need to check if we have enough quote currency (USDT)
	// Since we don't know the exact price here, we'll store the order
	// and mark it as executed immediately

	t.executedOrders[clientOrderID] = orderInfo{
		amount: amount,
		side:   "buy",
	}

	t.logger.Info("Simulated buy order",
		zap.String("pair", t.pair.String()),
		zap.String("amount", amount.String()),
		zap.String("client_order_id", clientOrderID),
	)

	return nil
}

// Sell simulates a sell order by updating virtual balances
func (t *SimulateTrader) Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if we have enough base currency to sell
	baseBalance := t.balances[t.pair.From]
	if baseBalance.LessThan(amount) {
		return fmt.Errorf("insufficient %s balance for sell: have %s, need %s",
			t.pair.From, baseBalance.String(), amount.String())
	}

	t.executedOrders[clientOrderID] = orderInfo{
		amount: amount,
		side:   "sell",
	}

	t.logger.Info("Simulated sell order",
		zap.String("pair", t.pair.String()),
		zap.String("amount", amount.String()),
		zap.String("client_order_id", clientOrderID),
	)

	return nil
}

// OrderExecuted returns execution status for simulated orders
// In simulation mode, orders are executed immediately
func (t *SimulateTrader) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	order, exists := t.executedOrders[clientOrderID]
	if !exists {
		// In simulation mode, if order not found in memory (e.g., after restart),
		// we assume it was executed in a previous session. The validation logic
		// will check if the filled amount is valid and may mark it as failed if zero.
		t.logger.Warn("Order not found in simulation trader - assuming executed from previous session",
			zap.String("client_order_id", clientOrderID),
		)
		return true, decimal.Zero, nil
	}

	// In simulation, orders execute immediately
	t.logger.Debug("Simulated order execution check",
		zap.String("client_order_id", clientOrderID),
		zap.String("amount", order.amount.String()),
		zap.String("side", order.side),
	)

	return true, order.amount, nil
}

// ApplyTrade updates virtual balances after a trade is confirmed
// This should be called by the strategy after order execution
func (t *SimulateTrader) ApplyTrade(price decimal.Decimal, amount decimal.Decimal, side string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if side == "buy" {
		// Buy: amount is in quote currency (USDT) to spend
		// We convert USDT amount to BTC based on price
		quoteSpent := amount
		baseAmount := amount.Div(price) // Convert USDT to BTC

		if t.balances[t.pair.To].LessThan(quoteSpent) {
			return errors.Errorf("insufficient %s balance: have %s, need %s",
				t.pair.To, t.balances[t.pair.To].String(), quoteSpent.String())
		}

		t.balances[t.pair.To] = t.balances[t.pair.To].Sub(quoteSpent)
		t.balances[t.pair.From] = t.balances[t.pair.From].Add(baseAmount)

		entryTime := time.Now()

		if t.position == nil {
			pos, err := entity.NewPositionFromExternalSnapshot(baseAmount, price, entryTime)
			if err != nil {
				return errors.Wrap(err, "failed to record simulated position")
			}
			t.position = pos
		} else {
			totalBase := t.position.Amount.Add(baseAmount)
			if totalBase.GreaterThan(decimal.Zero) {
				// Weighted average price using total quote spent
				existingQuote := t.position.EntryPrice.Mul(t.position.Amount)
				totalQuote := existingQuote.Add(quoteSpent)

				t.position.Amount = totalBase
				t.position.EntryPrice = totalQuote.Div(totalBase)
				t.position.EntryTime = entryTime
			}
		}

		t.logger.Info("Applied simulated buy trade",
			zap.String("pair", t.pair.String()),
			zap.String("price", price.String()),
			zap.String("base_amount", baseAmount.String()),
			zap.String("quote_spent", quoteSpent.String()),
			zap.String("new_base_balance", t.balances[t.pair.From].String()),
			zap.String("new_quote_balance", t.balances[t.pair.To].String()),
		)
	} else if side == "sell" {
		// Sell: amount is in base currency (BTC), receive quote currency (USDT)
		// For sell, amount represents how much BTC we want to sell
		// We receive amount * price USDT
		baseAmount := amount
		quoteReceived := amount.Mul(price)

		if t.balances[t.pair.From].LessThan(baseAmount) {
			return errors.Errorf("insufficient %s balance: have %s, need %s",
				t.pair.From, t.balances[t.pair.From].String(), baseAmount.String())
		}

		t.balances[t.pair.From] = t.balances[t.pair.From].Sub(baseAmount)
		t.balances[t.pair.To] = t.balances[t.pair.To].Add(quoteReceived)

		if t.position != nil {
			remaining := t.position.Amount.Sub(baseAmount)
			if remaining.LessThanOrEqual(decimal.Zero) {
				t.position = nil
			} else {
				t.position.Amount = remaining
			}
		}

		t.logger.Info("Applied simulated sell trade",
			zap.String("pair", t.pair.String()),
			zap.String("price", price.String()),
			zap.String("base_amount", baseAmount.String()),
			zap.String("quote_received", quoteReceived.String()),
			zap.String("new_base_balance", t.balances[t.pair.From].String()),
			zap.String("new_quote_balance", t.balances[t.pair.To].String()),
		)
	} else {
		return errors.Errorf("unknown side: %s", side)
	}

	return nil
}

// GetBalance returns current virtual balance for a currency
func (t *SimulateTrader) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.balances[currency], nil
}

// GetPosition returns the current simulated position (if any).
func (t *SimulateTrader) GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if pair != t.pair || t.position == nil {
		return nil, nil
	}

	clone := *t.position
	return &clone, nil
}

// SetPositionStops sets the simulated stop loss / take profit levels.
func (t *SimulateTrader) SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if pair != t.pair || t.position == nil {
		return nil
	}

	if takeProfit.GreaterThan(decimal.Zero) {
		t.position.TakeProfit = takeProfit
	} else {
		t.position.TakeProfit = decimal.Zero
	}

	if stopLoss.GreaterThan(decimal.Zero) {
		t.position.StopLoss = stopLoss
	} else {
		t.position.StopLoss = decimal.Zero
	}

	return nil
}
