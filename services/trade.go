package services

import (
	"math/big"
	"migrator/entity"
)

type Trader interface {
	Buy(pair entity.Pair, amount *big.Float) error
	Sell(pair entity.Pair, amount *big.Float) error
}
