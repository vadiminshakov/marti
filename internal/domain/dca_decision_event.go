package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// DCADecisionEvent trading decision made by DCA strategy.
type DCADecisionEvent struct {
	Timestamp         time.Time       `json:"ts"`
	Pair              string          `json:"pair"`
	Action            string          `json:"action"` // "buy" or "sell"
	CurrentPrice      decimal.Decimal `json:"current_price"`
	AverageEntryPrice decimal.Decimal `json:"avg_entry_price,omitempty"`
	TradePart         int             `json:"trade_part"`
	QuoteBalance      decimal.Decimal `json:"quote_balance,omitempty"`
}
