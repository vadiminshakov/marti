// Package domain defines core data structures used throughout the trading bot.
package domain

import "fmt"

// Pair represents a cryptocurrency trading pair consisting of two currencies.
// For example, BTC/USDT where BTC is the base currency (From) and USDT is the quote currency (To).
type Pair struct {
	// From represents the base currency symbol (e.g., "BTC")
	From string
	// To represents the quote currency symbol (e.g., "USDT")
	To string
}

// String returns the string representation of the trading pair in "FROM_TO" format.
// For example, "BTC_USDT".
func (p *Pair) String() string {
	return fmt.Sprintf("%s_%s", p.From, p.To)
}

// Symbol returns the concatenated symbol representation without separator.
// For example, "BTCUSDT". This format is commonly used by exchange APIs.
func (p *Pair) Symbol() string {
	return fmt.Sprintf("%s%s", p.From, p.To)
}
