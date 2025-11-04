// Package analysis provides market analysis utilities such as volume studies.
package analysis

import (
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"go.uber.org/zap"
)

// MarketAnalyzer analyzes market structure and patterns
type MarketAnalyzer struct {
	logger *zap.Logger
}

// NewMarketAnalyzer creates a new MarketAnalyzer instance
func NewMarketAnalyzer(logger *zap.Logger) *MarketAnalyzer {
	return &MarketAnalyzer{
		logger: logger,
	}
}

// AnalyzeVolume calculates volume metrics and identifies spikes
func (m *MarketAnalyzer) AnalyzeVolume(klines []entity.MarketCandle) entity.VolumeAnalysis {
	if len(klines) == 0 {
		m.logger.Warn("no kline data for volume analysis")
		return entity.VolumeAnalysis{
			CurrentVolume:  decimal.Zero,
			AverageVolume:  decimal.Zero,
			RelativeVolume: decimal.Zero,
			VolumeSpikes:   []int{},
		}
	}

	// Calculate 20-period simple moving average of volume
	period := 20
	if len(klines) < period {
		period = len(klines)
	}

	sum := decimal.Zero
	for i := len(klines) - period; i < len(klines); i++ {
		sum = sum.Add(klines[i].Volume)
	}
	avgVolume := sum.Div(decimal.NewFromInt(int64(period)))

	// Current volume is the most recent candle
	currentVolume := klines[len(klines)-1].Volume

	// Calculate relative volume
	relativeVolume := decimal.Zero
	if avgVolume.GreaterThan(decimal.Zero) {
		relativeVolume = currentVolume.Div(avgVolume)
	}

	// Identify volume spikes (volume > 1.5x average)
	spikeThreshold := avgVolume.Mul(decimal.NewFromFloat(1.5))
	var spikes []int

	for i := 0; i < len(klines); i++ {
		if klines[i].Volume.GreaterThan(spikeThreshold) {
			spikes = append(spikes, i)
		}
	}

	return entity.VolumeAnalysis{
		CurrentVolume:  currentVolume,
		AverageVolume:  avgVolume,
		RelativeVolume: relativeVolume,
		VolumeSpikes:   spikes,
	}
}
