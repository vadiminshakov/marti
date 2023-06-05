package windowfinder

import (
	"context"
	"fmt"
	"github.com/adshao/go-binance/v2"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"time"
)

const klinesize = "4h"

type BinanceWindowFinder struct {
	client    *binance.Client
	pair      entity.Pair
	minwindow decimal.Decimal
	statHours uint64
}

func NewBinanceWindowFinder(client *binance.Client, minwindow decimal.Decimal, pair entity.Pair, statHours uint64) *BinanceWindowFinder {
	return &BinanceWindowFinder{client: client, pair: pair, statHours: statHours, minwindow: minwindow}
}

func (b *BinanceWindowFinder) GetBuyPriceAndWindow() (decimal.Decimal, decimal.Decimal, error) {
	startTime := time.Now().Add(-time.Duration(b.statHours)*time.Hour).Unix() * 1000
	endTime := time.Now().Unix() * 1000

	klines, err := b.client.NewKlinesService().Symbol(b.pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(klinesize).Do(context.Background())
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, err
	}

	cumulativeBuyPrice, cumulativeWindow := decimal.NewFromInt(0), decimal.NewFromInt(0)

	for _, k := range klines {
		klineOpen, _ := decimal.NewFromString(k.Open)
		klineClose, _ := decimal.NewFromString(k.Close)

		klinesum := klineOpen.Add(klineClose)
		buyprice := klinesum.Div(decimal.NewFromInt(2))
		cumulativeBuyPrice = cumulativeBuyPrice.Add(buyprice)

		klinewindow := klineOpen.Sub(klineClose).Abs()
		cumulativeWindow = cumulativeWindow.Add(klinewindow)
	}

	cumulativeBuyPrice = cumulativeBuyPrice.Div(decimal.NewFromInt(int64(len(klines))))
	cumulativeWindow = cumulativeWindow.Div(decimal.NewFromInt(int64(len(klines))))

	if cumulativeWindow.Cmp(b.minwindow) < 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("window less then min (found %s, min %s)", cumulativeWindow.String(), b.minwindow.String())
	}
	return cumulativeBuyPrice, cumulativeWindow, nil
}
