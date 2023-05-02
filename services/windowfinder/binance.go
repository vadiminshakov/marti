package windowfinder

import (
	"context"
	"fmt"
	"github.com/adshao/go-binance/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"time"
)

type BinanceWindowFinder struct {
	client    *binance.Client
	pair      entity.Pair
	klineSize string
	koeff     decimal.Decimal
	minwindow decimal.Decimal
}

func NewBinanceWindowFinder(client *binance.Client, minwindow decimal.Decimal, pair entity.Pair, klineSize string, koeff decimal.Decimal) *BinanceWindowFinder {
	return &BinanceWindowFinder{client: client, pair: pair, klineSize: klineSize, koeff: koeff, minwindow: minwindow}
}

func (b *BinanceWindowFinder) GetBuyPriceAndWindow() (decimal.Decimal, decimal.Decimal, error) {
	ks, err := time.ParseDuration(b.klineSize)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "*BinanceWindowFinder.GetBuyPriceAndWindow: failed to parse klineSize")
	}

	startTime := time.Now().Add(-ks*5).Unix() * 1000
	endTime := time.Now().Unix() * 1000
	klines, err := b.client.NewKlinesService().Symbol(b.pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(b.klineSize).Do(context.Background())
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
	cumulativeBuyPrice = cumulativeBuyPrice.Mul(b.koeff)
	cumulativeWindow = cumulativeWindow.Div(decimal.NewFromInt(int64(len(klines))))
	cumulativeWindow = cumulativeWindow.Mul(b.koeff)

	if cumulativeWindow.Cmp(b.minwindow) < 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("window less then min (found %s, min %s)", cumulativeWindow.String(), b.minwindow.String())
	}
	return cumulativeBuyPrice, cumulativeWindow, nil
}
