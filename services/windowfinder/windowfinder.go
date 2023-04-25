package windowfinder

import "math/big"

type WindowFinder interface {
	GetBuyPriceAndWindow() (*big.Float, *big.Float, error)
}
