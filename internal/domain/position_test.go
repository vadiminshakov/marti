package entity

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestPosition_CalculateTotalEquity(t *testing.T) {
	tests := []struct {
		name           string
		position       *Position
		currentPrice   decimal.Decimal
		baseBalance    decimal.Decimal
		quoteBalance   decimal.Decimal
		leverage       int
		expectedEquity decimal.Decimal
	}{
		{
			name:           "No position",
			position:       nil,
			currentPrice:   decimal.NewFromInt(50000),
			baseBalance:    decimal.NewFromInt(1),
			quoteBalance:   decimal.NewFromInt(1000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(51000),
		},
		{
			name: "Long position, price up",
			position: &Position{
				Amount:     decimal.NewFromInt(1),
				EntryPrice: decimal.NewFromInt(40000),
				Side:       PositionSideLong,
			},
			currentPrice:   decimal.NewFromInt(50000),
			baseBalance:    decimal.NewFromInt(1),
			quoteBalance:   decimal.NewFromInt(10000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(60000),
		},
		{
			name:           "Long position, equity calculation check",
			position:       &Position{Amount: decimal.NewFromInt(1), EntryPrice: decimal.NewFromInt(40000), Side: PositionSideLong},
			currentPrice:   decimal.NewFromInt(50000),
			baseBalance:    decimal.NewFromInt(1),
			quoteBalance:   decimal.NewFromInt(10000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(60000),
		},
		{
			name:           "Long position, balance slightly less than amount",
			position:       &Position{Amount: decimal.NewFromInt(1), EntryPrice: decimal.NewFromInt(40000), Side: PositionSideLong},
			currentPrice:   decimal.NewFromInt(50000),
			baseBalance:    decimal.NewFromFloat(0.999999),
			quoteBalance:   decimal.NewFromInt(10000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(60000),
		},
		{
			name: "Long position, price down",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideLong,
			},
			currentPrice:   decimal.NewFromInt(45000),
			baseBalance:    decimal.NewFromInt(1),
			quoteBalance:   decimal.NewFromInt(1000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(46000),
		},
		{
			name: "Long position with leverage",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideLong,
			},
			currentPrice:   decimal.NewFromInt(55000),
			baseBalance:    decimal.NewFromInt(1),
			quoteBalance:   decimal.NewFromInt(1000),
			leverage:       2,
			expectedEquity: decimal.NewFromInt(31000),
		},
		{
			name: "Short position",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideShort,
			},
			currentPrice:   decimal.NewFromInt(40000),
			baseBalance:    decimal.NewFromInt(-1),
			quoteBalance:   decimal.NewFromInt(10000),
			leverage:       1,
			expectedEquity: decimal.NewFromInt(70000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equity := tt.position.CalculateTotalEquity(tt.currentPrice, tt.baseBalance, tt.quoteBalance, tt.leverage)
			assert.True(t, tt.expectedEquity.Equal(equity), "expected %s, got %s", tt.expectedEquity, equity)
		})
	}

	t.Run("Short position (Net Balance Logic)", func(t *testing.T) {
		pos := &Position{
			EntryPrice: decimal.NewFromInt(50000),
			Amount:     decimal.NewFromInt(1),
			Side:       PositionSideShort,
		}
		currentPrice := decimal.NewFromInt(40000)
		baseBalance := decimal.NewFromInt(-1)
		quoteBalance := decimal.NewFromInt(-40000)
		leverage := 1

		equity := pos.CalculateTotalEquity(currentPrice, baseBalance, quoteBalance, leverage)
		assert.Equal(t, "20000", equity.String())
	})
}
