package trader

import (
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

// Trader defines the interface for trading operations
type Trader interface {
	Buy(amount decimal.Decimal) error
	Sell(amount decimal.Decimal) error
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}
