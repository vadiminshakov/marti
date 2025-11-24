// Package collector provides market data collection utilities.
package collector

import (
	"context"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
)

// BinanceKlineProvider implements KlineProvider for Binance exchange.
type BinanceKlineProvider struct {
	client *binance.Client
}

// NewBinanceKlineProvider creates a new Binance kline provider.
func NewBinanceKlineProvider(client *binance.Client) *BinanceKlineProvider {
	return &BinanceKlineProvider{client: client}
}

// GetKlines fetches kline data from Binance.
func (p *BinanceKlineProvider) GetKlines(ctx context.Context, pair domain.Pair, interval string, limit int) ([]domain.MarketCandle, error) {
	symbol := pair.Symbol()

	klines, err := p.client.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch klines from Binance for %s", pair.String())
	}

	result := make([]domain.MarketCandle, len(klines))
	for i, k := range klines {
		open, err := decimal.NewFromString(k.Open)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse open price at index %d", i)
		}
		high, err := decimal.NewFromString(k.High)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse high price at index %d", i)
		}
		low, err := decimal.NewFromString(k.Low)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse low price at index %d", i)
		}
		close, err := decimal.NewFromString(k.Close)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse close price at index %d", i)
		}
		volume, err := decimal.NewFromString(k.Volume)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse volume at index %d", i)
		}

		result[i] = domain.MarketCandle{
			OpenTime:  time.Unix(0, k.OpenTime*int64(time.Millisecond)),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: time.Unix(0, k.CloseTime*int64(time.Millisecond)),
		}
	}

	return result, nil
}
