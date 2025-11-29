package domain
import "github.com/shopspring/decimal"

const (
	defaultVolumePeriod     = 20
	volumeSpikeThreshold    = 1.5
	highVolumeThreshold     = 1.5
	veryHighVolumeThreshold = 2.0
)

// VolumeAnalysis volume metrics and patterns.
type VolumeAnalysis struct {
	// CurrentVolume volume of the most recent candle.
	CurrentVolume decimal.Decimal
	// AverageVolume 20-period simple moving average of volume.
	AverageVolume decimal.Decimal
	// RelativeVolume ratio of current volume to average.
	RelativeVolume decimal.Decimal
	// VolumeSpikes indices of candles where volume exceeded 1.5x average.
	VolumeSpikes []int
}

// NewVolumeAnalysis creates a new VolumeAnalysis.
func NewVolumeAnalysis(candles []MarketCandle) VolumeAnalysis {
	if len(candles) == 0 {
		return VolumeAnalysis{
			CurrentVolume:  decimal.Zero,
			AverageVolume:  decimal.Zero,
			RelativeVolume: decimal.Zero,
			VolumeSpikes:   []int{},
		}
	}

	// Calculate 20-period simple moving average of volume
	period := defaultVolumePeriod
	if len(candles) < period {
		period = len(candles)
	}

	sum := decimal.Zero
	for i := len(candles) - period; i < len(candles); i++ {
		sum = sum.Add(candles[i].Volume)
	}
	avgVolume := sum.Div(decimal.NewFromInt(int64(period)))

	// Current volume is the most recent candle
	currentVolume := candles[len(candles)-1].Volume

	// Calculate relative volume
	relativeVolume := decimal.Zero
	if avgVolume.GreaterThan(decimal.Zero) {
		relativeVolume = currentVolume.Div(avgVolume)
	}

	// Identify volume spikes (volume > 1.5x average)
	spikeThreshold := avgVolume.Mul(decimal.NewFromFloat(volumeSpikeThreshold))
	var spikes []int

	for i := 0; i < len(candles); i++ {
		if candles[i].Volume.GreaterThan(spikeThreshold) {
			spikes = append(spikes, i)
		}
	}

	return VolumeAnalysis{
		CurrentVolume:  currentVolume,
		AverageVolume:  avgVolume,
		RelativeVolume: relativeVolume,
		VolumeSpikes:   spikes,
	}
}

// HasSpike returns true if the current volume is significantly higher than average.
func (v VolumeAnalysis) HasSpike() bool {
	return v.RelativeVolume.GreaterThan(decimal.NewFromFloat(volumeSpikeThreshold))
}

// IsHighVolume returns true if volume is notably elevated.
func (v VolumeAnalysis) IsHighVolume() bool {
	return v.RelativeVolume.GreaterThan(decimal.NewFromFloat(highVolumeThreshold))
}

// IsVeryHighVolume returns true if volume is exceptionally high.
func (v VolumeAnalysis) IsVeryHighVolume() bool {
	return v.RelativeVolume.GreaterThan(decimal.NewFromFloat(veryHighVolumeThreshold))
}

// IsLowVolume returns true if volume is below average.
func (v VolumeAnalysis) IsLowVolume() bool {
	return v.RelativeVolume.LessThan(decimal.NewFromInt(1))
}

// HasRecentSpike returns true if there was a volume spike.
func (v VolumeAnalysis) HasRecentSpike(lastN int) bool {
	if len(v.VolumeSpikes) == 0 {
		return false
	}
	// Check if any spike occurred in the last N indices
	for _, spikeIdx := range v.VolumeSpikes {
		if spikeIdx >= len(v.VolumeSpikes)-lastN {
			return true
		}
	}
	return false
}
