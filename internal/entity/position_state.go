package entity

import (
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
)

// Position represents an open trading position tracked in memory.
type Position struct {
	EntryPrice   decimal.Decimal
	Amount       decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Invalidation string
	EntryTime    time.Time
}

// NewPosition constructs a position opened by the strategy.
func NewPosition(amount, entryPrice decimal.Decimal, entryTime time.Time, exit ExitPlan) (*Position, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("position amount must be greater than zero")
	}
	if entryPrice.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("entry price must be greater than zero")
	}

	return &Position{
		EntryPrice:   entryPrice,
		Amount:       amount,
		StopLoss:     decimal.NewFromFloat(exit.StopLossPrice),
		TakeProfit:   decimal.NewFromFloat(exit.TakeProfitPrice),
		Invalidation: exit.InvalidationCondition,
		EntryTime:    entryTime,
	}, nil
}

// NewPositionFromExternalSnapshot builds an in-memory position based on external (exchange) balance snapshot.
func NewPositionFromExternalSnapshot(amount, entryPrice decimal.Decimal, entryTime time.Time) (*Position, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("position amount must be greater than zero")
	}
	if entryPrice.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("entry price must be greater than zero")
	}

	return &Position{
		EntryPrice: entryPrice,
		Amount:     amount,
		EntryTime:  entryTime,
	}, nil
}

// UpdateAmount synchronises the tracked position size with the actual balance.
// Returns true when amount changed.
func (p *Position) UpdateAmount(amount decimal.Decimal) bool {
	if p == nil {
		return false
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return false
	}
	if p.Amount.Equal(amount) {
		return false
	}

	p.Amount = amount
	return true
}

// PnL calculates profit and loss for the given market price.
func (p *Position) PnL(currentPrice decimal.Decimal) decimal.Decimal {
	if p == nil {
		return decimal.Zero
	}

	return currentPrice.Sub(p.EntryPrice).Mul(p.Amount)
}

// IsPositive returns true if the position is open and has a positive amount.
func (p *Position) IsPositive() bool {
	return p != nil && p.Amount.IsPositive()
}
