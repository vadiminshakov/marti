package entity

import (
	"fmt"
	"math/big"
)

type TradeEvent struct {
	Action Action
	Pair   Pair
	Amount *big.Float
}

func (t *TradeEvent) String() string {
	return fmt.Sprintf("%s action: %s amount: %s", t.Pair.String(), t.Action.String(), t.Amount.String())
}
