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
			expectedEquity: decimal.NewFromInt(51000), // 1000 + 1 * 50000
		},
		{
			name: "Long position, price up",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideLong,
			},
			currentPrice: decimal.NewFromInt(55000),
			baseBalance:  decimal.NewFromInt(1),
			quoteBalance: decimal.NewFromInt(1000),
			leverage:     1,
			// Notional = 1 * 50000 = 50000
			// Collateral = 50000 / 1 = 50000
			// PnL = (55000 - 50000) * 1 = 5000
			// Quote = 1000
			// Total = 1000 + 50000 + 5000 = 56000
			expectedEquity: decimal.NewFromInt(56000),
		},
		{
			name: "Long position, price down",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideLong,
			},
			currentPrice: decimal.NewFromInt(45000),
			baseBalance:  decimal.NewFromInt(1),
			quoteBalance: decimal.NewFromInt(1000),
			leverage:     1,
			// Notional = 50000
			// Collateral = 50000
			// PnL = (45000 - 50000) * 1 = -5000
			// Total = 1000 + 50000 - 5000 = 46000
			expectedEquity: decimal.NewFromInt(46000),
		},
		{
			name: "Long position with leverage",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideLong,
			},
			currentPrice: decimal.NewFromInt(55000),
			baseBalance:  decimal.NewFromInt(1),
			quoteBalance: decimal.NewFromInt(1000),
			leverage:     2,
			// Notional = 50000
			// Collateral = 50000 / 2 = 25000
			// Collateral Amount = 1 / 2 = 0.5 BTC
			// FreeBase = 1 - 0.5 = 0.5 BTC
			// FreeBaseValue = 0.5 * 55000 = 27500
			// PnL = (55000 - 50000) * 1 = 5000
			// Quote = 1000
			// Total = 1000 + 27500 + 25000 + 5000 = 58500
			expectedEquity: decimal.NewFromInt(58500),
		},
		{
			name: "Short position",
			position: &Position{
				EntryPrice: decimal.NewFromInt(50000),
				Amount:     decimal.NewFromInt(1),
				Side:       PositionSideShort,
			},
			currentPrice: decimal.NewFromInt(40000),
			baseBalance:  decimal.NewFromInt(0),
			quoteBalance: decimal.NewFromInt(10000), // 10k free, 50k locked as collateral
			leverage:     1,
			// Notional = 1 * 50000 = 50000
			// Collateral = 50000 / 1 = 50000
			// PnL = (50000 - 40000) * 1 = 10000 (profit because price dropped)
			// Quote = 10000
			// Total = 10000 + 50000 + 10000 = 70000
			expectedEquity: decimal.NewFromInt(70000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equity := tt.position.CalculateTotalEquity(tt.currentPrice, tt.baseBalance, tt.quoteBalance, tt.leverage)
			assert.True(t, tt.expectedEquity.Equal(equity), "expected %s, got %s", tt.expectedEquity, equity)
		})
	}
}
