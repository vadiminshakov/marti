package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// AveragingDecisionEvent is a trading decision made by any position-averaging
// strategy (DCA, Martingale). The Strategy field names the concrete strategy.
type AveragingDecisionEvent struct {
	Timestamp         time.Time       `json:"ts"`
	Pair              string          `json:"pair"`
	Strategy          string          `json:"strategy"` // "dca" | "martingale"
	Action            string          `json:"action"`   // "buy" or "sell"
	CurrentPrice      decimal.Decimal `json:"current_price"`
	AverageEntryPrice decimal.Decimal `json:"avg_entry_price,omitempty"`
	TradePart         int             `json:"trade_part"`
	QuoteBalance      decimal.Decimal `json:"quote_balance,omitempty"`
}
