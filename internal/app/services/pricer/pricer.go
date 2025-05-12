package pricer

import (
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

type Pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}
