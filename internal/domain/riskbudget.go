package domain

import "github.com/shopspring/decimal"

// RiskBudget risk-based allocation logic.
type RiskBudget struct {
	percent float64
}

// NewRiskBudget returns a risk budget.
func NewRiskBudget(percent float64) RiskBudget {
	return RiskBudget{percent: percent}
}

// Allocate calculates position value and base asset size.
func (r RiskBudget) Allocate(balance, price decimal.Decimal) (positionValue decimal.Decimal, amount decimal.Decimal) {
	if r.percent <= 0 {
		return decimal.Zero, decimal.Zero
	}
	if price.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero
	}

	fraction := decimal.NewFromFloat(r.percent / 100)
	positionValue = balance.Mul(fraction)
	if positionValue.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, decimal.Zero
	}

	amount = positionValue.Div(price)
	return positionValue, amount
}
