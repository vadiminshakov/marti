package domain

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// TradeEvent represents a trading event containing all necessary information
// about a trade that should be executed or has been executed.
type TradeEvent struct {
	// Action specifies whether this is a buy or sell trade
	Action Action
	// Pair represents the trading pair for this trade
	Pair Pair
	// Amount is the quantity of the base currency to be traded
	Amount decimal.Decimal
	// Price is the price at which the trade should be executed
	Price decimal.Decimal
}

// String returns a human-readable string representation of the trade event.
// Format: "PAIR_NAME action: ACTION_TYPE amount: AMOUNT"
func (t *TradeEvent) String() string {
	return fmt.Sprintf("%s action: %s amount: %s", t.Pair.String(), t.Action.String(), t.Amount.String())
}
