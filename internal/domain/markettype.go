// Package domain defines core data structures used throughout the trading bot.
package domain

// MarketType type of market for trading.
type MarketType string

const (
	// MarketTypeSpot spot trading.
	MarketTypeSpot MarketType = "spot"
	// MarketTypeMargin margin trading.
	MarketTypeMargin MarketType = "margin"
)

// String returns the string representation.
func (m MarketType) String() string {
	return string(m)
}

// IsValid checks if the MarketType value is valid.
func (m MarketType) IsValid() bool {
	return m == MarketTypeSpot || m == MarketTypeMargin
}
