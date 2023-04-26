package detector

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/adshao/go-binance/v2"
	"github.com/vadimInshakov/marti/entity"
)

type Repository interface {
	SaveBuyPoint(pair string, price *big.Float) error
}

type Detector struct {
	pair       entity.Pair
	buypoint   *big.Float
	window     *big.Float
	lastAction entity.Action
}

func NewDetector(client *binance.Client, usebalance float64, pair entity.Pair, buypoint, window *big.Float) (*Detector, error) {
	res, err := client.NewGetAccountService().Do(context.Background())
	if err != nil {
		return nil, err
	}

	var fromBalance *big.Float
	var toBalance *big.Float
	for _, b := range res.Balances {
		if b.Asset == pair.To {
			toBalance, _ = new(big.Float).SetString(b.Free)
		}
		if b.Asset == pair.From {
			fromBalance, _ = new(big.Float).SetString(b.Free)
		}
	}

	d := &Detector{pair: pair, buypoint: buypoint, window: window}

	// определим курс
	p, err := client.NewListPricesService().Symbol(pair.Symbol()).Do(context.Background())
	if err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("failed to get price for %s", pair.String())
	}

	price, _ := new(big.Float).SetString(p[0].Price)

	// определяем процент доступной для операций второй валюты
	percent := new(big.Float).Quo(big.NewFloat(usebalance), big.NewFloat(100))
	toBalance.Mul(toBalance, percent)

	// если больше первой валюты, то продаем, если больше второй, то покупаем
	fromBalanceInSecondCoinsForm := new(big.Float).Mul(fromBalance, price)
	if fromBalanceInSecondCoinsForm.Cmp(toBalance) < 0 {
		d.lastAction = entity.ActionSell
	} else {
		d.lastAction = entity.ActionBuy
	}

	log.Println("last action:", d.lastAction.String())

	return d, nil
}

func (d *Detector) NeedAction(price *big.Float) (entity.Action, error) {
	nevermindChange := new(big.Float).Quo(d.window, big.NewFloat(2))
	// check need to sell
	{
		if d.lastAction == entity.ActionBuy {
			sellPoint := new(big.Float).Add(d.buypoint, nevermindChange)
			comparison := price.Cmp(sellPoint)
			if comparison >= 0 {
				d.lastAction = entity.ActionSell
				return entity.ActionSell, nil
			}
		}
	}

	// check need to buy
	{
		if d.lastAction == entity.ActionSell {
			buyPoint := new(big.Float).Sub(d.buypoint, nevermindChange)
			comparison := price.Cmp(buyPoint)
			if comparison <= 0 {
				d.lastAction = entity.ActionBuy
				return entity.ActionBuy, nil
			}
		}
	}

	return entity.ActionNull, nil
}

func (d *Detector) LastAction() entity.Action {
	return d.lastAction
}
