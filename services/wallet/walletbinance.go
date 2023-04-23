package wallet

import (
	"errors"
	"math/big"
)

type InMemWallet struct {
	transactor Tx
	balances   map[string]*big.Float
}

func NewInMemWallet(transactor Tx, balancesStore map[string]*big.Float) *InMemWallet {
	return &InMemWallet{transactor: transactor, balances: balancesStore}
}

func (w *InMemWallet) BeginTx() Tx {
	return w.transactor
}

func (w *InMemWallet) Add(tx Tx, currency string, amount *big.Float) error {
	w.balances[currency] = amount
	return nil
}

func (w *InMemWallet) Sub(tx Tx, currency string, amount *big.Float) error {
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
