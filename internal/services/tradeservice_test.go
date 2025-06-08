package services

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestIsPercentDifferenceSignificant(t *testing.T) {
	tests := []struct {
		name             string
		currentPrice     decimal.Decimal
		referencePrice   decimal.Decimal
		thresholdPercent decimal.Decimal
		expected         bool
	}{
		{
			name:             "ref zero, current zero, threshold positive",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // no difference (0 is not > threshold)
		},
		{
			name:             "ref zero, current non-zero, threshold positive",
			currentPrice:     decimal.NewFromInt(10),
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // reference price is zero, so false returned
		},
		{
			name:             "ref zero, current non-zero, threshold zero",
			currentPrice:     decimal.NewFromInt(10),
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.Zero,
			expected:         false, // reference price is zero, so false returned
		},
		{
			name:             "ref zero, current zero, threshold zero",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.Zero,
			expected:         false, // 0 is not > 0
		},
		{
			name:             "current zero, ref non-zero, threshold allows -100% (abs 100%)",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.NewFromInt(10),
			thresholdPercent: decimal.NewFromInt(99), // abs diff is 100%, 100 > 99 is true
			expected:         true,
		},
		{
			name:             "current zero, ref non-zero, threshold exactly 100%",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.NewFromInt(10),
			thresholdPercent: decimal.NewFromInt(100), // abs diff is 100%, 100 > 100 is false
			expected:         false,
		},
		{
			name:             "no change",
			currentPrice:     decimal.NewFromInt(100),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // 0% diff is not > 1%
		},
		{
			name:             "increase, below threshold",
			currentPrice:     decimal.NewFromFloat(100.5),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 0.5% change, 0.5 > 1 is false
			expected:         false,
		},
		{
			name:             "increase, at threshold (using > logic, so false)",
			currentPrice:     decimal.NewFromInt(101),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 1% change, 1 > 1 is false
			expected:         false,
		},
		{
			name:             "increase, above threshold",
			currentPrice:     decimal.NewFromFloat(101.1),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 1.1% change, 1.1 > 1 is true
			expected:         true,
		},
		{
			name:             "decrease, below threshold",
			currentPrice:     decimal.NewFromFloat(99.5),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 0.5% change, 0.5 > 1 is false
			expected:         false,
		},
		{
			name:             "decrease, at threshold (using > logic, so false)",
			currentPrice:     decimal.NewFromInt(99),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 1% change, 1 > 1 is false
			expected:         false,
		},
		{
			name:             "decrease, above threshold",
			currentPrice:     decimal.NewFromFloat(98.9),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 1.1% change, 1.1 > 1 is true
			expected:         true,
		},
		{
			name:             "larger threshold, significant change",
			currentPrice:     decimal.NewFromInt(115),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(10), // 15% change, 15 > 10 is true
			expected:         true,
		},
		{
			name:             "larger threshold, insignificant change",
			currentPrice:     decimal.NewFromInt(105),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(10), // 5% change, 5 > 10 is false
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPercentDifferenceSignificant(tt.currentPrice, tt.referencePrice, tt.thresholdPercent); got != tt.expected {
				t.Errorf("isPercentDifferenceSignificant(%s, %s, %s) = %v, want %v", tt.currentPrice.String(), tt.referencePrice.String(), tt.thresholdPercent.String(), got, tt.expected)
			}
		})
	}
}
