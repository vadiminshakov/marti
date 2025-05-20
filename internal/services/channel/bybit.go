package channel

import (
	"time"

	"github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

const (
	bybitKlineInterval = "240" // 4h interval
	bybitCategory      = "spot"
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
	if b.statHours == 0 {
		return decimal.Decimal{}, decimal.Decimal{}, errors.New("statHours must be greater than 0")
	}

	startTime := time.Now().Add(-time.Duration(b.statHours)*time.Hour).Unix() * 1000
	endTime := time.Now().Unix() * 1000

	klines, err := b.client.V5().Market().GetKline(bybit.V5GetKlineParam{
		Category: bybitCategory,
		Symbol:   bybit.SymbolV5(b.pair.Symbol()),
		Interval: bybit.Interval240,
		Start:    &startTime,
		End:      &endTime,
		Limit:    nil,
	})
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "failed to get klines from Bybit")
	}

	if len(klines.Result.List) == 0 {
		return decimal.Decimal{}, decimal.Decimal{}, errors.New("no klines data received from Bybit")
	}

	klinesconv, err := convertBybitKlines(klines.Result.List)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "failed to convert Bybit klines")
	}

	buyprice, window, err := CalcBuyPriceAndChannel(klinesconv)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, errors.Wrap(err, "failed to calculate buy price and channel")
	}

	return buyprice, window, nil
}

func convertBybitKlines(klines bybit.V5GetKlineList) ([]*entity.Kline, error) {
	var res []*entity.Kline
	for _, k := range klines {
		openPrice, err := decimal.NewFromString(k.Open)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse open price: %s", k.Open)
		}

		closePrice, err := decimal.NewFromString(k.Close)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse close price: %s", k.Close)
		}

		res = append(res, &entity.Kline{
			Open:  openPrice,
			Close: closePrice,
		})
	}
	return res, nil
}
