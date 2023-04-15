package services

import (
	"math/big"
	"migrator/entity"
)

type Detector interface {
	NeedAction(pair entity.Pair, price *big.Float) (entity.Action, error)
}
