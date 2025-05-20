package channel

import (
	"github.com/shopspring/decimal"
)

// ChannelFinder defines the interface for finding trading channels
type ChannelFinder interface {
	GetTradingChannel() (decimal.Decimal, decimal.Decimal, error)
}
