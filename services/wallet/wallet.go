package wallet

import (
	"github.com/shopspring/decimal"
)

type Wallet interface {
	BeginTx() Tx
	Add(tx Tx, currency string, amount decimal.Decimal) error
	Sub(tx Tx, currency string, amount decimal.Decimal) error

	Balance(currency string) (decimal.Decimal, error)
}
