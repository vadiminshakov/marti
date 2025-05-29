package historytest

import (
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

func (p *pricerCsv) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
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
}

// Buy buys amount of asset in trade pair.
func (t *traderCsv) Buy(amount decimal.Decimal) error {
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	// When starting with USDT, the amount parameter is in USDT
	// We need to calculate how much BTC we can buy with this amount of USDT
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

	return nil
}

// Sell sells amount of asset in trade pair.
func (t *traderCsv) Sell(amount decimal.Decimal) error {
	// If we don't have any BTC, we can't sell
	if t.balance1.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("cannot sell BTC, balance is zero")
	}

	// Make sure we don't try to sell more than we have
	amountToSell := amount
	if amountToSell.GreaterThan(t.balance1) {
		amountToSell = t.balance1
	}

	// Subtract the BTC amount from our balance
	t.balance1 = t.balance1.Sub(amountToSell)
	price, ok := <-t.pricesCh
	if !ok && price.IsZero() {
		return errors.New("prices channel is closed")
	}

	// Calculate how much USDT we get for the BTC
	usdtAmount := price.Mul(amountToSell)

	// Calculate fee (0.1% of the trade amount)
	tradeFee := usdtAmount.Mul(decimal.NewFromFloat(feePercent))
	
	// Add the USDT to our balance (minus fee)
	t.balance2 = t.balance2.Add(usdtAmount)

	// Track the fee
	t.fee = t.fee.Add(tradeFee)

	// Keep track of balance history
	t.oldbalance2 = t.balance2
	if t.firstbalance2.IsZero() {
		t.firstbalance2 = t.balance2
	}

	// Increment deal counter
	t.dealsCount++

	return nil
}
