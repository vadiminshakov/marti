package entity

import "github.com/shopspring/decimal"

// Kliner defines the interface for accessing kline (candlestick) price data.
// Implementations should provide access to open and close prices.
type Kliner interface {
	// OpenPrice returns the opening price of the kline period
	OpenPrice() decimal.Decimal
	// ClosePrice returns the closing price of the kline period
	ClosePrice() decimal.Decimal
}

// Kline represents a candlestick data point with open and close prices.
// This is a simplified version focusing on the essential price data needed for trading decisions.
type Kline struct {
	// Open is the opening price of the kline period
	Open decimal.Decimal
	// Close is the closing price of the kline period
	Close decimal.Decimal
}

// OpenPrice returns the opening price of the kline period.
func (k *Kline) OpenPrice() decimal.Decimal {
	return k.Open
}

// ClosePrice returns the closing price of the kline period.
func (k *Kline) ClosePrice() decimal.Decimal {
	return k.Close
}
