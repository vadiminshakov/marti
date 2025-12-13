package domain

import (
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestShouldBuyAtPrice_EmptySeries(t *testing.T) {
	series := NewDCASeries()
	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	price := decimal.NewFromInt(100)
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.False(t, decision.ShouldBuy)
	require.Equal(t, "empty_series", decision.Reason)
}

func TestShouldBuyAtPrice_NoLastBuyPrice(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	series.LastBuyPrice = decimal.Zero

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	price := decimal.NewFromInt(90)
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.False(t, decision.ShouldBuy)
	require.Equal(t, "no_last_buy_price", decision.Reason)
}

func TestShouldBuyAtPrice_DipBelowLastBuy(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5), // 5% dip required
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// 100 * (1 - 0.05) = 95, so price at 94 should trigger buy
	price, _ := decimal.NewFromString("94")
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.True(t, decision.ShouldBuy)
	require.Equal(t, "price_dipped_below_last_buy", decision.Reason)
}

func TestShouldBuyAtPrice_NoDipBelowLastBuy(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5), // 5% dip required
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// 100 * (1 - 0.05) = 95, so price at 96 should NOT trigger buy (insufficient dip)
	price, _ := decimal.NewFromString("96")
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.False(t, decision.ShouldBuy)
	require.Equal(t, "price_not_below_threshold", decision.Reason)
}

func TestShouldBuyAtPrice_PriceNotBelowLastBuy(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// Price equal to or above last buy should not trigger
	price := decimal.NewFromInt(100)
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.False(t, decision.ShouldBuy)
	require.Equal(t, "price_not_below_last_buy", decision.Reason)
}

func TestShouldBuyAtPrice_MaxTradesReached(t *testing.T) {
	series := NewDCASeries()
	// Add 5 purchases (maxTrades = 5)
	for i := 1; i <= 5; i++ {
		price := decimal.NewFromInt(100 - int64(5*i))
		require.NoError(t, series.AddPurchase(
			fmt.Sprintf("buy%d", i),
			price, // decreasing prices
			decimal.NewFromInt(1000),
			time.Now(),
			i,
		))
	}

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// Price well below last buy, but max trades reached
	price, _ := decimal.NewFromString("50")
	decision := series.ShouldBuyAtPrice(price, thresholds)

	require.False(t, decision.ShouldBuy)
	require.Equal(t, "max_trades_reached", decision.Reason)
}

func TestShouldTakeProfitAtPrice_FirstSellFromAvg(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10), // 10% gain required
		MaxTrades:            5,
	}

	// 100 * (1 + 0.10) = 110, so price at 111 should trigger first sell
	price, _ := decimal.NewFromString("111")
	decision := series.ShouldTakeProfitAtPrice(price, thresholds)

	require.True(t, decision.ShouldSell)
	// With 1 purchase, one "part" = total_base / 1 = total_base (full sell)
	require.Equal(t, "full_sell_by_cap", decision.Reason)
	require.True(t, decision.IsFullSell)
	// Amount should be the entire position (total_base = 1000 / 100 = 10)
	expectedAmount, _ := decimal.NewFromString("10")
	require.True(t, expectedAmount.Equal(decision.Amount), "Expected %s, got %s", expectedAmount.String(), decision.Amount.String())
}

func TestShouldTakeProfitAtPrice_SecondSellFromLastSell(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))
	require.NoError(t, series.AddPurchase("buy2", decimal.NewFromInt(95), decimal.NewFromInt(1000), time.Now(), 2))

	// Simulate first sell: set LastSellPrice
	series.LastSellPrice, _ = decimal.NewFromString("110")

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// Next sell should be from LastSellPrice (110)
	// 110 * (1 + 0.10) = 121, so price at 122 should trigger
	price, _ := decimal.NewFromString("122")
	decision := series.ShouldTakeProfitAtPrice(price, thresholds)

	require.True(t, decision.ShouldSell)
	require.Equal(t, "partial_sell_step", decision.Reason)
	require.False(t, decision.IsFullSell)
	// Amount should be one "part" = total_base / num_purchases
	// total_base = (1000/100) + (1000/95) = 10 + 10.526... = 20.526...
	// one part = 20.526... / 2 = 10.263...
	minAmount, _ := decimal.NewFromString("10")
	maxAmount, _ := decimal.NewFromString("11")
	require.True(t, decision.Amount.GreaterThan(minAmount))
	require.True(t, decision.Amount.LessThan(maxAmount))
}

func TestShouldTakeProfitAtPrice_NoGainAboveAvg(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// Price below or at average should not trigger sell
	price := decimal.NewFromInt(100)
	decision := series.ShouldTakeProfitAtPrice(price, thresholds)

	require.False(t, decision.ShouldSell)
	require.Equal(t, "price_not_above_avg", decision.Reason)
}

func TestShouldTakeProfitAtPrice_FullSellByCap(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// With only 1 purchase, one "part" = total_base / 1 = total_base (full sell)
	price, _ := decimal.NewFromString("111")
	decision := series.ShouldTakeProfitAtPrice(price, thresholds)

	require.True(t, decision.ShouldSell)
	require.True(t, decision.IsFullSell)
	require.Equal(t, "full_sell_by_cap", decision.Reason)
}

func TestShouldTakeProfitAtPrice_NotEnoughGain(t *testing.T) {
	series := NewDCASeries()
	require.NoError(t, series.AddPurchase("buy1", decimal.NewFromInt(100), decimal.NewFromInt(1000), time.Now(), 1))

	thresholds := DCAThresholds{
		BuyThresholdPercent:  decimal.NewFromInt(5),
		SellThresholdPercent: decimal.NewFromInt(10),
		MaxTrades:            5,
	}

	// 100 * (1 + 0.10) = 110, so price at 105 should NOT trigger (insufficient gain)
	price, _ := decimal.NewFromString("105")
	decision := series.ShouldTakeProfitAtPrice(price, thresholds)

	require.False(t, decision.ShouldSell)
	require.Equal(t, "gain_not_significant", decision.Reason)
}

func TestAddPurchase_UpdatesLastBuyPrice(t *testing.T) {
	series := NewDCASeries()

	price1 := decimal.NewFromInt(100)
	require.NoError(t, series.AddPurchase("buy1", price1, decimal.NewFromInt(1000), time.Now(), 1))
	require.Equal(t, price1, series.LastBuyPrice)

	price2, _ := decimal.NewFromString("95")
	require.NoError(t, series.AddPurchase("buy2", price2, decimal.NewFromInt(1000), time.Now(), 2))
	require.Equal(t, price2, series.LastBuyPrice)

	price3, _ := decimal.NewFromString("90")
	require.NoError(t, series.AddPurchase("buy3", price3, decimal.NewFromInt(1000), time.Now(), 3))
	require.Equal(t, price3, series.LastBuyPrice)
}

func TestCalculateSellAmountInternal(t *testing.T) {
	tests := []struct {
		name          string
		purchases     int
		prices        []decimal.Decimal
		amounts       []decimal.Decimal
		expectedRatio float64 // ratio of returned amount to total base
	}{
		{
			name:          "single_purchase",
			purchases:     1,
			prices:        []decimal.Decimal{decimal.NewFromInt(100)},
			amounts:       []decimal.Decimal{decimal.NewFromInt(1000)},
			expectedRatio: 1.0, // full amount (total_base / 1)
		},
		{
			name:          "two_purchases",
			purchases:     2,
			prices:        []decimal.Decimal{decimal.NewFromInt(100), mustDecimalFromString("95")},
			amounts:       []decimal.Decimal{decimal.NewFromInt(1000), decimal.NewFromInt(1000)},
			expectedRatio: 0.5, // half amount (total_base / 2)
		},
		{
			name:          "three_purchases",
			purchases:     3,
			prices:        []decimal.Decimal{decimal.NewFromInt(100), mustDecimalFromString("95"), mustDecimalFromString("90")},
			amounts:       []decimal.Decimal{decimal.NewFromInt(1000), decimal.NewFromInt(1000), decimal.NewFromInt(1000)},
			expectedRatio: 0.333333, // third amount (total_base / 3)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			series := NewDCASeries()
			for i := 0; i < tt.purchases; i++ {
				require.NoError(t, series.AddPurchase(fmt.Sprintf("buy%d", i+1), tt.prices[i], tt.amounts[i], time.Now(), i+1))
			}

			amount := series.calculateSellAmountInternal()
			totalBase := series.TotalBaseAmount()

			// Check that amount is approximately expectedRatio * totalBase
			expectedRatioDec := decimal.NewFromFloat(tt.expectedRatio)
			expectedAmountApprox := totalBase.Mul(expectedRatioDec)
			tolerance, _ := decimal.NewFromString("0.01")

			require.True(t,
				amount.Sub(expectedAmountApprox).Abs().LessThan(tolerance),
				"Expected amount ~%.6f, got %s", tt.expectedRatio, amount.String())
		})
	}
}

func mustDecimalFromString(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}
