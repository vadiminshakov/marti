package entity

import "github.com/shopspring/decimal"

// VolumeAnalysis contains volume metrics and patterns identified in market data.
// This is a value object representing statistical analysis of trading volume.
type VolumeAnalysis struct {
	// CurrentVolume is the volume of the most recent candle
	CurrentVolume decimal.Decimal
	// AverageVolume is the 20-period simple moving average of volume
	AverageVolume decimal.Decimal
	// RelativeVolume is the ratio of current volume to average (CurrentVolume / AverageVolume)
	RelativeVolume decimal.Decimal
	// VolumeSpikes contains indices of candles where volume exceeded 1.5x average
	VolumeSpikes []int
}
