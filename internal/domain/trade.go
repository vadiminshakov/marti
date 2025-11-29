package domain

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// TradeEvent trading event.
type TradeEvent struct {
	// Action buy or sell trade.
	Action Action
	// Pair trading pair.
	Pair Pair
	// Amount quantity of the base currency.
	Amount decimal.Decimal
	// Price price at which the trade should be executed.
	Price decimal.Decimal
}

// String returns a human-readable string representation.
func (t *TradeEvent) String() string {
	return fmt.Sprintf("%s action: %s amount: %s", t.Pair.String(), t.Action.String(), t.Amount.String())
}
