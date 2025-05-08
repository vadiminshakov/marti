package entity

import (
	"fmt"
	"github.com/shopspring/decimal"
)

type TradeEvent struct {
	Action Action
	Pair   Pair
	Amount decimal.Decimal
	Price  decimal.Decimal
}

func (t *TradeEvent) String() string {
	return fmt.Sprintf("%s action: %s amount: %s", t.Pair.String(), t.Action.String(), t.Amount.String())
}
