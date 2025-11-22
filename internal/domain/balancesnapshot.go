package domain

import "time"

// BalanceSnapshot represents wallet state for a trading pair.
// String fields avoid precision issues when rendered in UI layers.
type BalanceSnapshot struct {
	Timestamp  time.Time `json:"ts"`
	Pair       string    `json:"pair"`
	Model      string    `json:"model,omitempty"`
	Base       string    `json:"base"`
	Quote      string    `json:"quote"`
	TotalQuote string    `json:"total_quote,omitempty"`
	Price      string    `json:"price,omitempty"`
	Position   string    `json:"position,omitempty"`
}

// BalanceSnapshotRecord bundles a snapshot with the log index it originated from.
type BalanceSnapshotRecord struct {
	Index    uint64
	Snapshot BalanceSnapshot
}
