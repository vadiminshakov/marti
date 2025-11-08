package historytest

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

const (
	feePercent = 0.001
)

type pricerCsv struct {
	pricesCh chan decimal.Decimal
}

func (p *pricerCsv) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	return <-p.pricesCh, nil
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
	executed      map[string]decimal.Decimal
}

// Buy buys amount of asset in trade pair.
func (t *traderCsv) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	// when starting with USDT, the amount parameter is in USDT
	// we need to calculate how much BTC we can buy with this amount of USDT
	usdtAmount := amount
	btcAmount := usdtAmount.Div(price) // Convert USDT to BTC based on current price

	result := t.balance2.Sub(usdtAmount)
	if result.LessThan(decimal.Zero) {
		return fmt.Errorf("failed to buy, insufficient balance %s USDT, trying to buy BTC for %s USDT",
			t.balance2.StringFixed(3),
			usdtAmount.StringFixed(3))
	}

	t.balance1 = t.balance1.Add(btcAmount)

	tradeFee := usdtAmount.Mul(decimal.NewFromFloat(feePercent))
	t.balance2 = result
	t.fee = t.fee.Add(tradeFee)

	t.dealsCount++

	if t.executed == nil {
		t.executed = make(map[string]decimal.Decimal)
	}
	t.executed[clientOrderID] = amount

	return nil
}

// Sell sells amount of asset in trade pair.
func (t *traderCsv) Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	// if we don't have any BTC, we can't sell
	if t.balance1.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("cannot sell BTC, balance is zero")
	}

	// make sure we don't try to sell more than we have
	amountToSell := amount
	if amountToSell.GreaterThan(t.balance1) {
		amountToSell = t.balance1
	}

	// subtract the BTC amount from our balance
	t.balance1 = t.balance1.Sub(amountToSell)
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	// calculate how much USDT we get for the BTC
	usdtAmount := price.Mul(amountToSell)

	// calculate fee (0.1% of the trade amount)
	tradeFee := usdtAmount.Mul(decimal.NewFromFloat(feePercent))

	// add the USDT to our balance (minus fee)
	t.balance2 = t.balance2.Add(usdtAmount)

	// track the fee
	t.fee = t.fee.Add(tradeFee)

	// keep track of balance history
	t.oldbalance2 = t.balance2
	if t.firstbalance2.IsZero() {
		t.firstbalance2 = t.balance2
	}

	// increment deal counter
	t.dealsCount++

	if t.executed == nil {
		t.executed = make(map[string]decimal.Decimal)
	}
	t.executed[clientOrderID] = amountToSell

	return nil
}

// OrderExecuted reports execution state for deterministic CSV trader (orders are processed synchronously).
func (t *traderCsv) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	if t.executed == nil {
		return true, decimal.Zero, nil
	}
	amount, ok := t.executed[clientOrderID]
	if !ok {
		return true, decimal.Zero, nil
	}
	return true, amount, nil
}

// GetBalance returns the current balance for the specified currency.
func (t *traderCsv) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	switch currency {
	case t.pair.From:
		return t.balance1, nil
	case t.pair.To:
		return t.balance2, nil
	default:
		return decimal.Zero, nil
	}
}
