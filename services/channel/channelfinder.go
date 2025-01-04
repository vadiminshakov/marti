package channel

import (
	"github.com/shopspring/decimal"
)

type ChannelFinder interface {
	GetTradingChannel() (decimal.Decimal, decimal.Decimal, error)
}
