package providers

import (
	"context"
	"fmt"
	"time"

	bybit "github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
)

// BybitKlineProvider implements KlineProvider for Bybit exchange.
type BybitKlineProvider struct {
	client *bybit.Client
}

// NewBybitKlineProvider creates a new Bybit kline provider.
func NewBybitKlineProvider(client *bybit.Client) *BybitKlineProvider {
	return &BybitKlineProvider{client: client}
}

// GetKlines fetches kline data.
func (p *BybitKlineProvider) GetKlines(ctx context.Context, pair domain.Pair, interval string, limit int) ([]domain.MarketCandle, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be > 0")
	}

	bybitInterval, err := convertIntervalToBybit(interval)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid interval: %s", interval)
	}

	symbol := bybit.SymbolV5(pair.Symbol())
	category := bybit.CategoryV5Spot

	const maxPerRequest = 200

	var allKlines []bybit.V5GetKlineItem
	remainingLimit := limit

	for remainingLimit > 0 {
		batchSize := remainingLimit
		if batchSize > maxPerRequest {
			batchSize = maxPerRequest
		}

		param := bybit.V5GetKlineParam{
			Category: category,
			Symbol:   symbol,
			Interval: bybit.Interval(bybitInterval),
			Limit:    &batchSize,
		}

		result, err := p.client.V5().Market().GetKline(param)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to fetch klines from Bybit for %s", pair.String())
		}

		if result == nil {
			return nil, errors.Errorf("empty result from Bybit API for %s", pair.String())
		}

		klines := result.Result.List
		if len(klines) == 0 {
			if len(allKlines) == 0 {
				return nil, errors.Errorf("no kline data returned from Bybit for %s", pair.String())
			}
			break
		}

		allKlines = append(allKlines, klines...)

		// if we got fewer results than requested, we've reached the end
		if len(klines) < batchSize {
			break
		}

		remainingLimit -= len(klines)

		// avoid rate limiting by small delay between requests
		if remainingLimit > 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	candles := make([]domain.MarketCandle, len(allKlines))
	for i, k := range allKlines {
		openTime, err := parseTimestamp(k.StartTime)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse start time at index %d", i)
		}

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

		candles[i] = domain.MarketCandle{
			OpenTime:  openTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: openTime, // Bybit doesn't provide close time, use open time as approximation
		}
	}

	return candles, nil
}

// convertIntervalToBybit converts standard interval format to Bybit format.
// Standard format: "1m", "5m", "15m", "1h", "4h", "1d", etc.
// Bybit format: "1", "5", "15", "60", "240", "D", etc.
func convertIntervalToBybit(interval string) (string, error) {
	if len(interval) < 2 {
		return "", fmt.Errorf("invalid interval format: %s", interval)
	}

	// extract number and unit
	unit := interval[len(interval)-1]
	numberPart := interval[:len(interval)-1]

	switch unit {
	case 'm':
		// minutes: pass through as-is (e.g., "1m" -> "1", "5m" -> "5")
		return numberPart, nil
	case 'h':
		// hours to minutes: 1h -> 60, 2h -> 120, 4h -> 240
		var n int64
		for _, r := range numberPart {
			if r < '0' || r > '9' {
				return "", fmt.Errorf("invalid interval number: %s", interval)
			}
			n = n*10 + int64(r-'0')
		}
		return fmt.Sprintf("%d", n*60), nil
	case 'd':
		// day: pass as-is (Bybit uses "D" for day)
		return "D", nil
	case 'w':
		// week: pass as-is (Bybit uses "W" for week)
		return "W", nil
	default:
		return "", fmt.Errorf("unsupported interval unit: %c", unit)
	}
}

// parseTimestamp converts Bybit timestamp string (milliseconds) to time.Time.
func parseTimestamp(ts string) (time.Time, error) {
	if ts == "" {
		return time.Time{}, errors.New("empty timestamp")
	}

	var msec int64
	_, err := fmt.Sscanf(ts, "%d", &msec)
	if err != nil {
		return time.Time{}, errors.Wrapf(err, "failed to parse timestamp: %s", ts)
	}

	return time.UnixMilli(msec), nil
}
