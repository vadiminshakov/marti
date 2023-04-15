package services

import (
	"math/big"
	"migrator/entity"
)

type Pricer interface {
	GetPrice(pair entity.Pair) (*big.Float, error)
}
