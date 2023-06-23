package entity

import "github.com/shopspring/decimal"

type Kliner interface {
	OpenPrice() decimal.Decimal
	ClosePrice() decimal.Decimal
}

type Kline struct {
	Open  decimal.Decimal
	Close decimal.Decimal
}

func (k *Kline) OpenPrice() decimal.Decimal {
	return k.Open
}

func (k *Kline) ClosePrice() decimal.Decimal {
	return k.Close
}
