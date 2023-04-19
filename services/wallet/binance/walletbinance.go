package binance

import (
	"errors"
	"github.com/vadimInshakov/marti/services/wallet"
	"math/big"
)

type InMemWallet struct {
	transactor wallet.Tx
	balances   map[string]*big.Float
}

func NewInMemWallet(transactor wallet.Tx, balancesStore map[string]*big.Float) *InMemWallet {
	return &InMemWallet{transactor: transactor, balances: balancesStore}
}

func (w *InMemWallet) BeginTx() wallet.Tx {
	return w.transactor
}

func (w *InMemWallet) Add(tx wallet.Tx, currency string, amount *big.Float) error {
	w.balances[currency] = amount
	return nil
}

func (w *InMemWallet) Sub(tx wallet.Tx, currency string, amount *big.Float) error {
	_, ok := w.balances[currency]
	if ok {
		w.balances[currency].Sub(w.balances[currency], amount)
	}
	return nil
}

func (w *InMemWallet) Balance(currency string) (*big.Float, error) {
	if v, ok := w.balances[currency]; ok {
		return v, nil
	}

	return nil, errors.New("no such balance")
}
