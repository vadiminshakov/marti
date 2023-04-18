package wallet

import "math/big"

type Wallet interface {
	BeginTx() Tx
	Add(tx Tx, currency string, amount *big.Float) error
	Sub(tx Tx, currency string, amount *big.Float) error

	Balance(currency string) (*big.Float, error)
}
