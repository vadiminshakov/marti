package services

import (
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
)

var ErrNoData = errors.New("no data in WAL")

type BuyMetaData struct {
	price  decimal.Decimal
	amount decimal.Decimal
}

type WrappedWal struct {
	wal *gowal.Wal
}

func NewWrappedWal() (*WrappedWal, error) {
	w, err := gowal.NewWAL(gowal.Config{
		Dir:              "waldata",
		Prefix:           "seg_",
		SegmentThreshold: 1000,
		MaxSegments:      10,
		IsInSyncDiskMode: true,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error init wal")
	}

	return &WrappedWal{w}, nil
}

func (w *WrappedWal) Write(key string, data decimal.Decimal) error {
	b, _ := data.MarshalBinary()

	err := w.wal.Write(w.wal.CurrentIndex()+1, key, b)
	if err != nil {
		panic(err)
	}

	return nil
}

func (w *WrappedWal) GetLastBuyMeta() (BuyMetaData, error) {
	if w.wal.CurrentIndex() == 0 {
		return BuyMetaData{}, ErrNoData
	}

	lastBuyPrice, lastAmount := decimal.Zero, decimal.Zero
	noData := true
	for m := range w.wal.Iterator() {
		noData = false

		if m.Key == "lastbuy" {
			if err := lastBuyPrice.UnmarshalBinary(m.Value); err != nil {
				return BuyMetaData{}, errors.Wrap(err, "error unmarshal last buy price")
			}
		}
		if m.Key == "lastamount" {
			if err := lastAmount.UnmarshalBinary(m.Value); err != nil {
				return BuyMetaData{}, errors.Wrap(err, "error unmarshal last amount")
			}
		}
	}

	if noData {
		return BuyMetaData{}, ErrNoData
	}

	return BuyMetaData{lastBuyPrice, lastAmount}, nil
}

func (w *WrappedWal) Close() error {
	return w.wal.Close()
}
