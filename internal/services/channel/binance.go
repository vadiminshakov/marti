package channel

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"time"
)

const klinesize = "4h"

type BinanceWindowFinder struct {
	client    *binance.Client
	pair      entity.Pair
	statHours uint64
}

func NewBinanceChannelFinder(client *binance.Client, pair entity.Pair, statHours uint64) *BinanceWindowFinder {
	return &BinanceWindowFinder{client: client, pair: pair, statHours: statHours}
}

func (b *BinanceWindowFinder) GetTradingChannel() (decimal.Decimal, decimal.Decimal, error) {
	startTime := time.Now().Add(-time.Duration(b.statHours)*time.Hour).Unix() * 1000
	endTime := time.Now().Unix() * 1000

	klines, err := b.client.NewKlinesService().Symbol(b.pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(klinesize).Do(context.Background())
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, err
	}

	klinesconv, err := convertBinanceKlines(klines)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "error converting Binance klines")
	}
	buyprice, window, err := CalcBuyPriceAndChannel(klinesconv)
	return buyprice, window, err
}

func convertBinanceKlines(klines []*binance.Kline) ([]*entity.Kline, error) {
	var res []*entity.Kline
	for _, k := range klines {
		openPrice, _ := decimal.NewFromString(k.Open)
		closePrice, _ := decimal.NewFromString(k.Close)
		res = append(res, &entity.Kline{
			Open:  openPrice,
			Close: closePrice,
		})
	}
	return res, nil
}
