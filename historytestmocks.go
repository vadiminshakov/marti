package main

import (
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services/detector"
)

type pricerCsv struct {
	pricesCh chan decimal.Decimal
}

func (p *pricerCsv) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
	return <-p.pricesCh, nil
}

type detectorCsv struct {
	lastaction entity.Action
	buypoint   decimal.Decimal
	window     decimal.Decimal
}

func (d *detectorCsv) NeedAction(price decimal.Decimal) (entity.Action, error) {
	lastact, err := detector.Detect(d.lastaction, d.buypoint, d.window, price)
	if err != nil {
		return entity.ActionNull, err
	}
	if d.lastaction != entity.ActionNull {
		d.lastaction = lastact
	}

	return lastact, nil
}

func (d *detectorCsv) LastAction() entity.Action {
	return d.lastaction
}

type traderCsv struct {
	pair          *entity.Pair
	balance1      decimal.Decimal
	balance2      decimal.Decimal
	oldbalance2   decimal.Decimal
	firstbalance2 decimal.Decimal
	pricesCh      chan decimal.Decimal
	fee           decimal.Decimal
	dealsCount    uint
}

// Buy buys amount of asset in trade pair.
func (t *traderCsv) Buy(amount decimal.Decimal) error {
	price := <-t.pricesCh
	result := t.balance2.Sub(price.Mul(amount))

	if result.LessThan(decimal.Zero) {
		return nil
	}

	t.balance1 = t.balance1.Add(amount)

	t.balance2 = t.balance2.Sub(price.Mul(amount))
	t.fee = t.fee.Add(decimal.NewFromInt(33))

	t.dealsCount++

	return nil
}

// Sell sells amount of asset in trade pair.
func (t *traderCsv) Sell(amount decimal.Decimal) error {
	if t.balance1.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	t.balance1 = t.balance1.Sub(amount)
	price := <-t.pricesCh
	t.balance2 = t.balance2.Add(price.Mul(amount))
	t.fee = t.fee.Add(decimal.NewFromInt(4))

	t.oldbalance2 = t.balance2
	if t.firstbalance2.IsZero() {
		t.firstbalance2 = t.balance2
	}

	t.dealsCount++

	return nil
}
