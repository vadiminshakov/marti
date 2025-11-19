// Package entity defines core data structures used throughout the trading bot.
package entity

// MarketType represents the type of market for trading (spot or margin).
type MarketType string

const (
	// MarketTypeSpot represents spot trading where assets are bought and sold for immediate delivery.
	MarketTypeSpot MarketType = "spot"
	// MarketTypeMargin represents margin trading with leverage.
	// For Binance: uses margin account with borrowed funds.
	// For Bybit: uses linear perpetual contracts (USDT-margined futures).
	MarketTypeMargin MarketType = "margin"
)

// String returns the string representation of the MarketType.
func (m MarketType) String() string {
	return string(m)
}

// IsValid checks if the MarketType value is valid.
func (m MarketType) IsValid() bool {
	return m == MarketTypeSpot || m == MarketTypeMargin
}
