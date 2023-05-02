package windowfinder

import (
	"github.com/shopspring/decimal"
)

type WindowFinder interface {
	GetBuyPriceAndWindow() (decimal.Decimal, decimal.Decimal, error)
}
