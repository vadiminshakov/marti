package domain
import "github.com/shopspring/decimal"

// Kliner interface for accessing kline price data.
type Kliner interface {
	// OpenPrice returns the opening price.
	OpenPrice() decimal.Decimal
	// ClosePrice returns the closing price.
	ClosePrice() decimal.Decimal
}

// Kline candlestick data point.
type Kline struct {
	// Open is the opening price.
	Open decimal.Decimal
	// Close is the closing price.
	Close decimal.Decimal
}

// OpenPrice returns the opening price.
func (k *Kline) OpenPrice() decimal.Decimal {
	return k.Open
}

// ClosePrice returns the closing price.
func (k *Kline) ClosePrice() decimal.Decimal {
	return k.Close
}
