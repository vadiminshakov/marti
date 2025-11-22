package domain

import (
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
)

//go:generate stringer -type=PositionSide

type PositionSide int

const (
	PositionSideLong PositionSide = iota
	PositionSideShort
)

// Position represents an open trading position tracked in memory.
type Position struct {
	EntryPrice   decimal.Decimal
	Amount       decimal.Decimal
	StopLoss     decimal.Decimal
	TakeProfit   decimal.Decimal
	Invalidation string
	EntryTime    time.Time
	Side         PositionSide // Long or Short
}

// NewPosition constructs a position opened by the strategy.
func NewPosition(amount, entryPrice decimal.Decimal, entryTime time.Time, exit ExitPlan, side PositionSide) (*Position, error) {
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
		Side:         side,
	}, nil
}

// NewPositionFromExternalSnapshot builds an in-memory position based on external (exchange) balance snapshot.
func NewPositionFromExternalSnapshot(amount, entryPrice decimal.Decimal, entryTime time.Time, side PositionSide) (*Position, error) {
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
		Side:       side,
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

	// for long positions: PnL = (currentPrice - entryPrice) * amount
	// for short positions: PnL = (entryPrice - currentPrice) * amount
	if p.Side == PositionSideShort {
		return p.EntryPrice.Sub(currentPrice).Mul(p.Amount)
	}
	return currentPrice.Sub(p.EntryPrice).Mul(p.Amount)
}

// IsPositive returns true if the position is open and has a positive amount.
func (p *Position) IsPositive() bool {
	return p != nil && p.Amount.IsPositive()
}

// CalculateTotalEquity calculates the total equity including collateral and PnL.
func (p *Position) CalculateTotalEquity(currentPrice decimal.Decimal, baseBalance, quoteBalance decimal.Decimal, leverage int) decimal.Decimal {
	if p == nil || !p.IsPositive() {
		return quoteBalance.Add(baseBalance.Mul(currentPrice))
	}

	lev := int64(leverage)
	if lev < 1 {
		lev = 1
	}

	// calculate collateral and PnL
	notional := p.Amount.Abs().Mul(p.EntryPrice)
	collateral := notional.Div(decimal.NewFromInt(lev))
	pnl := p.PnL(currentPrice)

	// calculate free base balance
	// for longs: freeBase = baseBalance - Amount (can be negative if deficit)
	// for shorts: freeBase = baseBalance + Amount (can be positive if surplus)
	var freeBase decimal.Decimal
	switch p.Side {
	case PositionSideLong:
		freeBase = baseBalance.Sub(p.Amount)
		if freeBase.LessThan(decimal.Zero) {
			freeBase = decimal.Zero
		}
	case PositionSideShort:
		freeBase = baseBalance.Add(p.Amount)
	}

	freeBaseValue := freeBase.Mul(currentPrice)

	return quoteBalance.Add(freeBaseValue).Add(collateral).Add(pnl)
}
