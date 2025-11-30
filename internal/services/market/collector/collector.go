package collector

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/pkg/indicators"
)

const minCandlesForIndicators = 50

// klineProvider defines the interface for fetching historical candles.
type klineProvider interface {
	GetKlines(ctx context.Context, pair domain.Pair, interval string, limit int) ([]domain.MarketCandle, error)
}

// MarketDataCollector manages market data collection and indicator calculation.
type MarketDataCollector struct {
	provider klineProvider
	pair     domain.Pair
}

// NewMarketDataCollector creates a new market data collector.
func NewMarketDataCollector(provider klineProvider, pair domain.Pair) *MarketDataCollector {
	return &MarketDataCollector{
		provider: provider,
		pair:     pair,
	}
}

// FetchTimeframeData fetches raw candles and derives indicator values.
func (c *MarketDataCollector) FetchTimeframeData(
	ctx context.Context,
	interval string,
	limit int,
) (*domain.Timeframe, error) {
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

	return domain.NewTimeframe(interval, candles, indicatorSnapshots), nil
}
