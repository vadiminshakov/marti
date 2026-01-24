package domain

import "time"

// BalanceSnapshot wallet state for a trading pair.
type BalanceSnapshot struct {
	Timestamp  time.Time `json:"ts"`
	Pair       string    `json:"pair"`
	Model      string    `json:"model,omitempty"`
	Base       string    `json:"base"`
	Quote      string    `json:"quote"`
	TotalQuote string    `json:"total_quote,omitempty"`
	Price      string    `json:"price,omitempty"`
	Position   string    `json:"position,omitempty"`
	EntryPrice     string `json:"entry_price,omitempty"`
	PositionAmount string `json:"position_amount,omitempty"`
	UnrealizedPnL  string `json:"unrealized_pnl,omitempty"`
}

// NewBalanceSnapshot creates a new BalanceSnapshot.
func NewBalanceSnapshot(
	timestamp time.Time,
	pair string,
	model string,
	base string,
	quote string,
	totalQuote string,
	price string,
	position string,
	entryPrice string,
	positionAmount string,
	unrealizedPnL string,
) BalanceSnapshot {
	return BalanceSnapshot{
		Timestamp:      timestamp,
		Pair:           pair,
		Model:          normalizeModelName(model),
		Base:           base,
		Quote:          quote,
		TotalQuote:     totalQuote,
		Price:          price,
		Position:       position,
		EntryPrice:     entryPrice,
		PositionAmount: positionAmount,
		UnrealizedPnL:  unrealizedPnL,
	}
}

// BalanceSnapshotRecord bundles a snapshot.
type BalanceSnapshotRecord struct {
	Index    uint64
	Snapshot BalanceSnapshot
}
