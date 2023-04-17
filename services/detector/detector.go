package detector

import (
	"github.com/vadimInshakov/marti/entity"
	"math/big"
)

type Repository interface {
	SaveBuyPoint(pair string, price *big.Float) error
}

type Detector struct {
	pair     entity.Pair
	buypoint *big.Float
	window   *big.Float
}

func NewDetector(pair entity.Pair, buypoint, window *big.Float) *Detector {
	return &Detector{pair: pair, buypoint: buypoint, window: window}
}

func (d *Detector) NeedAction(price *big.Float) (entity.Action, error) {
	nevermindChange := new(big.Float).Quo(d.window, big.NewFloat(2))
	// check need to sell
	{
		sellPoint := new(big.Float).Add(d.buypoint, nevermindChange)
		comparison := price.Cmp(sellPoint)
		if comparison >= 0 {
			return entity.ActionSell, nil
		}
	}

	// check need to buy
	{
		buyPoint := new(big.Float).Sub(d.buypoint, nevermindChange)
		comparison := price.Cmp(buyPoint)
		if comparison <= 0 {
			return entity.ActionBuy, nil
		}
	}

	return entity.ActionNull, nil
}
