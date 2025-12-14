package historytest

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
)

const (
	feePercent = 0.001
)

type pricerCsv struct {
	feed *priceFeed
}

type priceFeed struct {
	mu    sync.RWMutex
	price decimal.Decimal
}

func newPriceFeed() *priceFeed {
	return &priceFeed{price: decimal.Zero}
}

func (f *priceFeed) Set(price decimal.Decimal) {
	f.mu.Lock()
	f.price = price
	f.mu.Unlock()
}

func (f *priceFeed) Get() decimal.Decimal {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.price
}

func (p *pricerCsv) GetPrice(ctx context.Context, pair domain.Pair) (decimal.Decimal, error) {
	price := p.feed.Get()
	if price.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, errors.New("price is not set")
	}

	return price, nil
}

type traderCsv struct {
	pair          *domain.Pair
	balance1      decimal.Decimal
	balance2      decimal.Decimal
	oldbalance2   decimal.Decimal
	firstbalance2 decimal.Decimal
	fee           decimal.Decimal
	dealsCount    uint
	executed      map[string]decimal.Decimal
	feed          *priceFeed
}

// Buy buys amount of asset in trade pair.
func (t *traderCsv) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	price := t.feed.Get()
	if price.LessThanOrEqual(decimal.Zero) {
		return errors.New("price is not set")
	}

	// In production, DCA sends BASE amount to buy.
	btcAmount := amount
	usdtAmount := btcAmount.Mul(price)

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
	price := t.feed.Get()
	if price.LessThanOrEqual(decimal.Zero) {
		return errors.New("price is not set")
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

// ExecuteAction routes the action to Buy/Sell methods for testing.
func (t *traderCsv) ExecuteAction(ctx context.Context, action domain.Action, amount decimal.Decimal, clientOrderID string) error {
	switch action {
	case domain.ActionOpenLong:
		return t.Buy(ctx, amount, clientOrderID)
	case domain.ActionCloseLong:
		return t.Sell(ctx, amount, clientOrderID)
	case domain.ActionOpenShort, domain.ActionCloseShort:
		return fmt.Errorf("short positions not supported by traderCsv")
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

type dummyRecorder struct{}

func (d *dummyRecorder) SaveDCA(event domain.DCADecisionEvent) error {
	return nil
}