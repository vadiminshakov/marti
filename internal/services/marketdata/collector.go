// Package marketdata provides utilities for collecting and managing market data
// such as klines (candlestick data) from cryptocurrency exchanges.
package marketdata

import (
	"context"
	"fmt"
	"time"

	"github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/indicators"
)

// KlineData represents a single candlestick data point
type KlineData struct {
	OpenTime  time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
	CloseTime time.Time
}

// KlineProvider defines the interface for fetching kline (candlestick) data
type KlineProvider interface {
	// GetKlines fetches historical kline data for a trading pair
	// limit specifies the maximum number of klines to fetch
	// interval specifies the kline interval (e.g., "1m", "3m", "5m", "1h", "4h")
	GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]KlineData, error)
}

// BinanceKlineProvider implements KlineProvider for Binance exchange
type BinanceKlineProvider struct {
	client *binance.Client
}

// NewBinanceKlineProvider creates a new Binance kline provider
func NewBinanceKlineProvider(client *binance.Client) *BinanceKlineProvider {
	return &BinanceKlineProvider{client: client}
}

// GetKlines fetches kline data from Binance
func (p *BinanceKlineProvider) GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]KlineData, error) {
	symbol := pair.Symbol()

	klines, err := p.client.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch klines from Binance for %s", pair.String())
	}

	result := make([]KlineData, len(klines))
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

		result[i] = KlineData{
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

// BybitKlineProvider implements KlineProvider for Bybit exchange
type BybitKlineProvider struct {
	client *bybit.Client
}

// NewBybitKlineProvider creates a new Bybit kline provider
func NewBybitKlineProvider(client *bybit.Client) *BybitKlineProvider {
	return &BybitKlineProvider{client: client}
}

// GetKlines fetches kline data from Bybit
func (p *BybitKlineProvider) GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]KlineData, error) {
	// Note: Bybit kline API implementation is pending
	// For now, return an error indicating this feature is not yet supported
	return nil, fmt.Errorf("Bybit kline provider for AI strategy is not yet implemented - please use Binance or Simulate platform for AI strategy")
}


// MarketDataCollector manages market data collection and indicator calculation
type MarketDataCollector struct {
	provider KlineProvider
	pair     entity.Pair
	interval string
	limit    int
}

// NewMarketDataCollector creates a new market data collector
func NewMarketDataCollector(provider KlineProvider, pair entity.Pair, interval string, limit int) *MarketDataCollector {
	return &MarketDataCollector{
		provider: provider,
		pair:     pair,
		interval: interval,
		limit:    limit,
	}
}

// GetMarketData fetches klines and calculates technical indicators
func (c *MarketDataCollector) GetMarketData(ctx context.Context) ([]KlineData, []indicators.IndicatorData, error) {
	// Create a context with timeout for API calls
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	klines, err := c.provider.GetKlines(ctxWithTimeout, c.pair, c.interval, c.limit)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to fetch klines")
	}

	if len(klines) == 0 {
		return nil, nil, errors.New("no kline data returned")
	}

	// Convert klines to PriceData format for indicator calculation
	priceData := make([]indicators.PriceData, len(klines))
	for i, k := range klines {
		priceData[i] = indicators.PriceData{
			Open:  k.Open,
			High:  k.High,
			Low:   k.Low,
			Close: k.Close,
		}
	}

	// Calculate indicators
	indicatorData, err := indicators.CalculateAllIndicators(priceData)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to calculate indicators")
	}

	return klines, indicatorData, nil
}

// GetCurrentPrice returns the most recent close price from klines
func (c *MarketDataCollector) GetCurrentPrice(ctx context.Context) (decimal.Decimal, error) {
	klines, err := c.provider.GetKlines(ctx, c.pair, c.interval, 1)
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "failed to fetch current price")
	}

	if len(klines) == 0 {
		return decimal.Zero, errors.New("no kline data for current price")
	}

	return klines[0].Close, nil
}
