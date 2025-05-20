package main

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/detector"
)

const (
	feeBuy  = 8
	feeSell = 8
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
	if lastact != entity.ActionNull {
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
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	result := t.balance2.Sub(price.Mul(amount))
	if result.LessThan(decimal.Zero) {
		return fmt.Errorf("failed to buy, insufficient balance %s USDT, trying to buy BTC for %s USDT",
			t.balance2.StringFixed(3),
			result.StringFixed(3))
	}

	t.balance1 = t.balance1.Add(amount)

	t.balance2 = result
	t.fee = t.fee.Add(decimal.NewFromInt(feeBuy))

	t.dealsCount++

	return nil
}

// Sell sells amount of asset in trade pair.
func (t *traderCsv) Sell(amount decimal.Decimal) error {
	if t.balance1.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	t.balance1 = t.balance1.Sub(amount)
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	profit := price.Mul(amount)

	t.balance2 = t.balance2.Add(profit)

	t.fee = t.fee.Add(decimal.NewFromInt(feeSell))

	t.oldbalance2 = t.balance2
	if t.firstbalance2.IsZero() {
		t.firstbalance2 = t.balance2
	}

	t.dealsCount++

	return nil
}
