package collector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/vadiminshakov/marti/internal/domain"
)

// HyperliquidKlineProvider implements KlineProvider for Hyperliquid exchange.
type HyperliquidKlineProvider struct {
	info *hyperliquid.Info
}

// NewHyperliquidKlineProvider creates a new Hyperliquid kline provider.
func NewHyperliquidKlineProvider(info *hyperliquid.Info) *HyperliquidKlineProvider {
	return &HyperliquidKlineProvider{info: info}
}

func parseIntervalToDuration(interval string) (time.Duration, error) {
	if interval == "" {
		return 0, fmt.Errorf("empty interval")
	}
	// supported formats: e.g., "1m", "3m", "5m", "15m", "1h", "4h", "1d"
	unit := interval[len(interval)-1]
	value := interval[:len(interval)-1]
	if value == "" {
		return 0, fmt.Errorf("invalid interval: %s", interval)
	}
	// simple atoi without extra deps
	var n int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid interval number: %s", interval)
		}
		n = n*10 + int64(r-'0')
	}
	switch unit {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported interval unit: %c", unit)
	}
}

// GetKlines fetches kline data.
func (p *HyperliquidKlineProvider) GetKlines(ctx context.Context, pair domain.Pair, interval string, limit int) ([]domain.MarketCandle, error) {
	if p.info == nil {
		return nil, fmt.Errorf("hyperliquid info is nil")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0")
	}
	dur, err := parseIntervalToDuration(interval)
	if err != nil {
		return nil, err
	}

	endMs := time.Now().UnixMilli()
	// Fetch a bit more window to account for rounding; add +2 extra candles worth of duration
	startMs := endMs - (int64(limit)+2)*dur.Milliseconds()

	// Hyperliquid requires the coin name (base), e.g. "BTC"
	coin := pair.From
	// On Hyperliquid USD-quoted is standard; if quote isn't USD, still pass base symbol only.
	coin = strings.ToUpper(coin)

	candles, err := p.info.CandlesSnapshot(ctx, coin, interval, startMs, endMs)
	if err != nil {
		return nil, err
	}

	if len(candles) == 0 {
		return nil, fmt.Errorf("no candles from hyperliquid for %s %s", coin, interval)
	}

	// Keep only the last `limit` candles if more returned
	if len(candles) > limit {
		candles = candles[len(candles)-limit:]
	}

	out := make([]domain.MarketCandle, 0, len(candles))
	for i, c := range candles {
		open, err := decimal.NewFromString(c.Open)
		if err != nil {
			return nil, fmt.Errorf("parse open at %d: %w", i, err)
		}
		high, err := decimal.NewFromString(c.High)
		if err != nil {
			return nil, fmt.Errorf("parse high at %d: %w", i, err)
		}
		low, err := decimal.NewFromString(c.Low)
		if err != nil {
			return nil, fmt.Errorf("parse low at %d: %w", i, err)
		}
		closeP, err := decimal.NewFromString(c.Close)
		if err != nil {
			return nil, fmt.Errorf("parse close at %d: %w", i, err)
		}
		volume, err := decimal.NewFromString(c.Volume)
		if err != nil {
			return nil, fmt.Errorf("parse volume at %d: %w", i, err)
		}

		out = append(out, domain.MarketCandle{
			OpenTime:  time.UnixMilli(c.TimeOpen),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			Volume:    volume,
			CloseTime: time.UnixMilli(c.TimeClose),
		})
	}

	return out, nil
}
