package services

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services/mocks"
	"github.com/vadimInshakov/marti/services/wallet"
	mocks2 "github.com/vadimInshakov/marti/services/wallet/mocks"
	"math/big"
	"testing"
)

type pricemock struct {
	n float64
}

func (p *pricemock) GetPrice(_ entity.Pair) (*big.Float, error) {
	p.n += 1
	return big.NewFloat(p.n), nil
}

type walletmock struct {
	t      *testing.T
	amount map[string]*big.Float
}

func (w *walletmock) Add(tx wallet.Tx, currency string, amount *big.Float) error {
	w.amount[currency].Add(w.amount[currency], amount)
	return nil
}
func (w *walletmock) Sub(tx wallet.Tx, currency string, amount *big.Float) error {
	w.amount[currency].Sub(w.amount[currency], amount)
	return nil
}

func (w *walletmock) Balance(currency string) (*big.Float, error) {
	return w.amount[currency], nil
}

func (w *walletmock) BeginTx() wallet.Tx {
	tx := mocks2.NewTx(w.t)
	tx.On("Commit").Return(nil)
	return tx
}

func TestTrade(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USD"}
	balanceBTC := big.NewFloat(0)
	balanceUSD := big.NewFloat(1)

	pricer := &pricemock{}

	trader := mocks.NewTrader(t)
	trader.On("Buy", mock.Anything, mock.Anything).Return(nil)
	trader.On("Sell", mock.Anything, mock.Anything).Return(nil)

	detector := mocks.NewDetector(t)
	detector.On("NeedAction", mock.Anything, big.NewFloat(1)).Return(entity.ActionBuy, nil)
	detector.On("NeedAction", mock.Anything, big.NewFloat(3)).Return(entity.ActionSell, nil)
	detector.On("NeedAction", mock.Anything, big.NewFloat(2)).Return(entity.ActionNull, nil)
	detector.On("NeedAction", mock.Anything, big.NewFloat(4)).Return(entity.ActionNull, nil)
	detector.On("NeedAction", mock.Anything, big.NewFloat(5)).Return(entity.ActionNull, nil)

	mockedWallet := &walletmock{
		t:      t,
		amount: map[string]*big.Float{"BTC": balanceBTC, "USD": balanceUSD},
	}

	ts := NewTradeService(pair, mockedWallet, pricer, detector, trader)
	event, err := ts.Trade()
	assert.NoError(t, err)
	assert.Equal(t, entity.ActionBuy, event.Action)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Equal(t, entity.ActionSell, event.Action)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	event, err = ts.Trade()
	assert.NoError(t, err)
	assert.Nil(t, event)

	btcb, err := mockedWallet.Balance("BTC")
	assert.NoError(t, err)
	assert.Equal(t, "0", btcb.String())

	usdb, err := mockedWallet.Balance("USD")
	assert.NoError(t, err)
	assert.Equal(t, "3", usdb.String())
}
