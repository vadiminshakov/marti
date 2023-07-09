package detector

import (
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
)

func Detect(lastaction entity.Action, buypoint, window, price decimal.Decimal) (entity.Action, error) {
	nevermindChange := window.Div(decimal.NewFromInt(2))
	// check need to sell
	{
		if lastaction == entity.ActionBuy {
			sellPoint := buypoint.Add(nevermindChange)
			comparison := price.Cmp(sellPoint)
			if comparison >= 0 {
				return entity.ActionSell, nil
			}
		}
	}

	// check need to buy
	{
		if lastaction == entity.ActionSell {
			buyPoint := buypoint.Sub(nevermindChange)
			comparison := price.Cmp(buyPoint)
			if comparison <= 0 {
				return entity.ActionBuy, nil
			}
		}
	}

	return entity.ActionNull, nil
}
