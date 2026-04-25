// Package averaging provides the shared engine for position-averaging strategies
// (DCA, Martingale, etc.) that buy into dips and sell on profit.
package averaging

import "github.com/shopspring/decimal"

// PositionSizer computes the quote-currency amount to spend on a single buy step.
//   - allocatedQuoteAmount: total budget for the entire series.
//   - maxTrades: maximum number of buy steps.
//   - step: 0-indexed current step (0 for the first buy, 1 for the second, …).
type PositionSizer func(allocatedQuoteAmount decimal.Decimal, maxTrades int, step int) decimal.Decimal

// EqualSizer divides the allocated amount equally across all trades (DCA behaviour).
func EqualSizer(allocated decimal.Decimal, maxTrades int, _ int) decimal.Decimal {
	return allocated.Div(decimal.NewFromInt(int64(maxTrades)))
}

// MartingaleSizer returns a PositionSizer that distributes the allocated amount
// using a geometric progression controlled by multiplier.
//
// Each step gets: allocated * multiplier^step / sum(multiplier^i for i=0..maxTrades-1).
// The sum of all steps equals allocated exactly.
//
// Example (multiplier=2, maxTrades=4, allocated=1000):
//
//	step 0: 1000 * 1/15 ≈ 66.67
//	step 1: 1000 * 2/15 ≈ 133.33
//	step 2: 1000 * 4/15 ≈ 266.67
//	step 3: 1000 * 8/15 ≈ 533.33
func MartingaleSizer(multiplier decimal.Decimal) PositionSizer {
	return func(allocated decimal.Decimal, maxTrades int, step int) decimal.Decimal {
		// normaliser: sum of multiplier^i for i = 0 .. maxTrades-1
		sum := decimal.Zero
		for i := range maxTrades {
			sum = sum.Add(multiplier.Pow(decimal.NewFromInt(int64(i))))
		}

		weight := multiplier.Pow(decimal.NewFromInt(int64(step)))

		return allocated.Mul(weight).Div(sum)
	}
}
