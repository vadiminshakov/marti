package detector

import (
	"context"
	"fmt"
	"github.com/shopspring/decimal"
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
	buypoint   decimal.Decimal
	window     decimal.Decimal
	lastAction entity.Action
}

func NewDetector(client *binance.Client, usebalance decimal.Decimal, pair entity.Pair, buypoint, window decimal.Decimal) (*Detector, error) {
	res, err := client.NewGetAccountService().Do(context.Background())
	if err != nil {
		return nil, err
	}

	var fromBalance decimal.Decimal
	var toBalance decimal.Decimal
	for _, b := range res.Balances {
		if b.Asset == pair.To {
			toBalance, _ = decimal.NewFromString(b.Free)
		}
		if b.Asset == pair.From {
			fromBalance, _ = decimal.NewFromString(b.Free)
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

	price, _ := decimal.NewFromString(p[0].Price)

	// определяем процент доступной для операций второй валюты
	percent := usebalance.Div(decimal.NewFromInt(100))
	toBalance = toBalance.Mul(percent)

	// если больше первой валюты, то продаем, если больше второй, то покупаем
	fromBalanceInSecondCoinsForm := fromBalance.Mul(price)
	if fromBalanceInSecondCoinsForm.Cmp(toBalance) < 0 {
		d.lastAction = entity.ActionSell
	} else {
		d.lastAction = entity.ActionBuy
	}

	log.Println("last action:", d.lastAction.String())

	return d, nil
}

func (d *Detector) NeedAction(price decimal.Decimal) (entity.Action, error) {
	nevermindChange := d.window.Div(decimal.NewFromInt(2))
	// check need to sell
	{
		if d.lastAction == entity.ActionBuy {
			sellPoint := d.buypoint.Add(nevermindChange)
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
			buyPoint := d.buypoint.Sub(nevermindChange)
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
