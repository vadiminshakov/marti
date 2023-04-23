package windowfinder

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/vadimInshakov/marti/entity"
	"math/big"
	"time"
)

type BinanceWindowFinder struct {
	client    *binance.Client
	pair      entity.Pair
	klineSize string
	koeff     float64
}

func NewBinanceWindowFinder(client *binance.Client, pair entity.Pair, klineSize string, koeff float64) *BinanceWindowFinder {
	return &BinanceWindowFinder{client: client, pair: pair, klineSize: klineSize, koeff: koeff}
}

func (b *BinanceWindowFinder) GetBuyPriceAndWindow() (*big.Float, *big.Float, error) {
	startTime := time.Now().AddDate(0, 0, -1).Unix() * 1000
	endTime := time.Now().Unix() * 1000
	klines, err := b.client.NewKlinesService().Symbol(b.pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(b.klineSize).Do(context.Background())
	if err != nil {
		return nil, nil, err
	}

	cumulativeBuyPrice, cumulativeWindow := big.NewFloat(0), big.NewFloat(0)

	for _, k := range klines {
		klineOpen, _ := new(big.Float).SetString(k.Open)
		klineClose, _ := new(big.Float).SetString(k.Close)

		klinesum := new(big.Float).Add(klineOpen, klineClose)
		buyprice := klinesum.Quo(klinesum, big.NewFloat(2))
		cumulativeBuyPrice.Add(cumulativeBuyPrice, buyprice)

		klinewindow := new(big.Float).Abs(new(big.Float).Sub(klineOpen, klineClose))
		cumulativeWindow.Add(cumulativeWindow, klinewindow)
	}
	cumulativeBuyPrice.Quo(cumulativeBuyPrice, big.NewFloat(float64(len(klines))))
	cumulativeBuyPrice.Mul(cumulativeBuyPrice, big.NewFloat(b.koeff))

	cumulativeWindow.Quo(cumulativeWindow, big.NewFloat(float64(len(klines))))
	cumulativeWindow.Mul(cumulativeWindow, big.NewFloat(b.koeff))

	return cumulativeBuyPrice, cumulativeWindow, nil
}
