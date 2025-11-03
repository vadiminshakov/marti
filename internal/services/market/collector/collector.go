// Package collector provides utilities for collecting and managing market data
// such as klines (candlestick data) from cryptocurrency exchanges.
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/market/indicators"
)

const minCandlesForIndicators = 50

// KlineProvider defines the interface for fetching kline (candlestick) data
type KlineProvider interface {
	// GetKlines fetches historical kline data for a trading pair
	// limit specifies the maximum number of klines to fetch
	// interval specifies the kline interval (e.g., "1m", "3m", "5m", "1h", "4h")
	GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]entity.MarketCandle, error)
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
func (p *BinanceKlineProvider) GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]entity.MarketCandle, error) {
	symbol := pair.Symbol()

	klines, err := p.client.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch klines from Binance for %s", pair.String())
	}

	result := make([]entity.MarketCandle, len(klines))
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

		result[i] = entity.MarketCandle{
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
func (p *BybitKlineProvider) GetKlines(ctx context.Context, pair entity.Pair, interval string, limit int) ([]entity.MarketCandle, error) {
	// Note: Bybit kline API implementation is pending
	// For now, return an error indicating this feature is not yet supported
	return nil, fmt.Errorf("Bybit kline provider for AI strategy is not yet implemented - please use Binance or Simulate platform for AI strategy")
}

// MarketDataCollector manages market data collection and indicator calculation
type MarketDataCollector struct {
	provider KlineProvider
	pair     entity.Pair
}

// NewMarketDataCollector creates a new market data collector
func NewMarketDataCollector(provider KlineProvider, pair entity.Pair) *MarketDataCollector {
	return &MarketDataCollector{
		provider: provider,
		pair:     pair,
	}
}

// FetchTimeframeData fetches raw candles and derives indicator values for the requested timeframe.
func (c *MarketDataCollector) FetchTimeframeData(
	ctx context.Context,
	interval string,
	limit int,
) (*entity.Timeframe, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	candles, err := c.provider.GetKlines(ctxWithTimeout, c.pair, interval, limit)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch klines for timeframe %s", interval)
	}

	if len(candles) == 0 {
		return nil, errors.Errorf("no kline data returned for timeframe %s", interval)
	}

	if len(candles) < minCandlesForIndicators {
		return nil, errors.Errorf(
			"insufficient kline data for timeframe %s (need at least %d, change 'lookback_periods' in config)",
			interval,
			minCandlesForIndicators,
		)
	}

	priceData := make([]indicators.PriceData, len(candles))
	for i, k := range candles {
		priceData[i] = indicators.PriceData{
			Open:  k.Open,
			High:  k.High,
			Low:   k.Low,
			Close: k.Close,
		}
	}

	indicatorSnapshots, err := indicators.CalculateAllIndicators(priceData)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to calculate indicators for timeframe %s", interval)
	}

	return entity.NewTimeframe(interval, candles, indicatorSnapshots), nil
}
