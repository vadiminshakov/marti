package wallet

import (
	"github.com/shopspring/decimal"
)

type InmemTx struct {
	Balances map[string]decimal.Decimal
}

func (tx *InmemTx) Rollback() error {
	return nil
}

func (tx *InmemTx) Commit() error {
	return nil
}
