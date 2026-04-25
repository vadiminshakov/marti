package averaging

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestEqualSizer(t *testing.T) {
	allocated := decimal.NewFromInt(1000)
	maxTrades := 4

	for step := range maxTrades {
		got := EqualSizer(allocated, maxTrades, step)
		require.True(t, got.Equal(decimal.NewFromInt(250)), "step %d: expected 250, got %s", step, got)
	}
}

func TestMartingaleSizer_SumEqualsAllocated(t *testing.T) {
	multiplier := decimal.NewFromInt(2)
	allocated := decimal.NewFromInt(1000)
	maxTrades := 4

	sizer := MartingaleSizer(multiplier)
	sum := decimal.Zero
	for step := range maxTrades {
		sum = sum.Add(sizer(allocated, maxTrades, step))
	}

	require.True(t, sum.Round(8).Equal(allocated.Round(8)),
		"sum of all steps should equal allocated: got %s", sum)
}

func TestMartingaleSizer_IncreasingSteps(t *testing.T) {
	multiplier := decimal.NewFromInt(2)
	allocated := decimal.NewFromInt(1000)
	maxTrades := 4

	sizer := MartingaleSizer(multiplier)
	prev := decimal.Zero
	for step := range maxTrades {
		cur := sizer(allocated, maxTrades, step)
		if step > 0 {
			require.True(t, cur.GreaterThan(prev),
				"step %d should be larger than step %d: %s <= %s", step, step-1, cur, prev)
		}
		prev = cur
	}
}

func TestMartingaleSizer_MultiplierOne_EqualsEqualSizer(t *testing.T) {
	multiplier := decimal.NewFromInt(1)
	allocated := decimal.NewFromInt(1000)
	maxTrades := 5

	sizer := MartingaleSizer(multiplier)
	equalPart := EqualSizer(allocated, maxTrades, 0)

	for step := range maxTrades {
		got := sizer(allocated, maxTrades, step)
		require.True(t, got.Round(8).Equal(equalPart.Round(8)),
			"multiplier=1 step %d: expected %s, got %s", step, equalPart, got)
	}
}
