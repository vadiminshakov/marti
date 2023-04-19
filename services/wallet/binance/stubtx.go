package binance

import "math/big"

type InmemTx struct {
	Balances map[string]*big.Float
}

func (tx *InmemTx) Rollback() error {
	return nil
}

func (tx *InmemTx) Commit() error {
	return nil
}
