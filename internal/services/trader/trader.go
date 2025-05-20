package trader

import (
	"github.com/shopspring/decimal"
)

// Trader defines the interface for trading operations
type Trader interface {
	Buy(amount decimal.Decimal) error
	Sell(amount decimal.Decimal) error
}
