package anomalydetector

import (
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
)

type AnomalyDetector struct {
	pair             entity.Pair
	buffer           []decimal.Decimal // buffer of prices
	cap              uint              // buffer capacity
	percentThreshold decimal.Decimal   // percent threshold for anomaly detection
}

// NewAnomalyDetector is a detector of price anomalies.
func NewAnomalyDetector(pair entity.Pair, buffercap uint, percentThreshold decimal.Decimal) *AnomalyDetector {
	buffer := make([]decimal.Decimal, 0, buffercap)
	return &AnomalyDetector{pair: pair, buffer: buffer, cap: buffercap, percentThreshold: percentThreshold}
}

// IsAnomaly calculates average price for last N prices and check if current price differs from average price for more than X percents.
// Returns true if price is anomaly.
func (d *AnomalyDetector) IsAnomaly(price decimal.Decimal) bool {
	if len(d.buffer) < int(d.cap) {
		d.buffer = append(d.buffer, price)
		return false
	}

	calcAvgPrice := func() decimal.Decimal {
		var sum decimal.Decimal
		for _, p := range d.buffer {
			sum = sum.Add(p)
		}
		return sum.Div(decimal.NewFromInt(int64(len(d.buffer))))
	}

	currentPriceDifferForPercent := func(currentPrice, avgPrice decimal.Decimal) decimal.Decimal {
		if currentPrice.GreaterThan(avgPrice) {
			return currentPrice.Sub(avgPrice).Div(avgPrice).Mul(decimal.NewFromInt(100))
		}
		return avgPrice.Sub(currentPrice).Div(avgPrice).Mul(decimal.NewFromInt(100))
	}

	percent := currentPriceDifferForPercent(price, calcAvgPrice())

	if len(d.buffer) >= int(d.cap) {
		d.buffer = d.buffer[1:] // remove first element to add new one
	}

	d.buffer = append(d.buffer, price)

	return percent.GreaterThanOrEqual(d.percentThreshold)
}
