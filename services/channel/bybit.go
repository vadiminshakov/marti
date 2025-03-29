package channel

import (
	"github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
	"time"
)

type BybitWindowFinder struct {
	client    *bybit.Client
	pair      entity.Pair
	statHours uint64
}

func NewBybitChannelFinder(client *bybit.Client, pair entity.Pair, statHours uint64) *BybitWindowFinder {
	return &BybitWindowFinder{client: client, pair: pair, statHours: statHours}
}

// GetTradingChannel calculates the trading channel (price range) for a given trading pair using historical kline (candlestick) data.
// It performs the following steps:
// 1. Determines the time range for fetching historical data based on the configured statHours (number of hours to look back).
// 2. Fetches kline data from the Bybit API for the specified trading pair and time range.
// 3. Calculates the optimal buy price and trading channel (price range) using the converted kline data.
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
	buyprice, window, err := CalcBuyPriceAndChannel(klinesconv)
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
