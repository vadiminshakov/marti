package channel

import (
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
	"time"
)

type BybitWindowFinder struct {
	client    *bybit.Client
	pair      entity.Pair
	minwindow decimal.Decimal
	statHours uint64
}

func NewBybitChannelFinder(client *bybit.Client, minwindow decimal.Decimal, pair entity.Pair, statHours uint64) *BybitWindowFinder {
	return &BybitWindowFinder{client: client, pair: pair, statHours: statHours, minwindow: minwindow}
}

func (b *BybitWindowFinder) GetTradingChannel() (decimal.Decimal, decimal.Decimal, error) {
	startTime := time.Now().Add(-time.Duration(b.statHours)*time.Hour).Unix() * 1000
	endTime := time.Now().Unix() * 1000

	klines, err := b.client.V5().Market().GetKline(bybit.V5GetKlineParam{
		Category: "spot",
		Symbol:   bybit.SymbolV5(b.pair.Symbol()),
		Interval: bybit.Interval240,
		Start:    &startTime,
		End:      &endTime,
		Limit:    nil,
	})
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, err
	}

	klinesconv, err := convertBybitKlines(klines.Result.List)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "error converting Binance klines")
	}
	buyprice, window, err := CalcBuyPriceAndWindow(klinesconv, b.minwindow)
	return buyprice, window, err
}

func convertBybitKlines(klines bybit.V5GetKlineList) ([]*entity.Kline, error) {
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
