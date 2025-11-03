package dca

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

// mockSimulatePricer is a simple pricer for testing simulation
type mockSimulatePricer struct {
	price decimal.Decimal
}

func (m *mockSimulatePricer) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	return m.price, nil
}

func TestDCAStrategy_WithSimulationTrader(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	// create simulation trader
	simTrader, err := trader.NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	// verify initial balances
	initialBTC, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	initialUSDT, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, initialBTC.Equal(decimal.Zero))
	assert.True(t, initialUSDT.Equal(decimal.NewFromInt(10000)))

	// simulate a buy trade with 5000 USDT
	price := decimal.NewFromInt(50000)
	usdtAmount := decimal.NewFromInt(5000)
	err = simTrader.ApplyTrade(price, usdtAmount, "buy")
	require.NoError(t, err)

	// verify balance updated
	btcBalance, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	expectedBTC := usdtAmount.Div(price) // 5000 / 50000 = 0.1
	expectedUSDT := decimal.NewFromInt(10000).Sub(usdtAmount) // 10000 - 5000 = 5000

	assert.True(t, btcBalance.Equal(expectedBTC), "BTC balance should be %s, got %s", expectedBTC, btcBalance)
	assert.True(t, usdtBalance.Equal(expectedUSDT), "USDT balance should be %s, got %s", expectedUSDT, usdtBalance)
}

func TestDCAStrategy_SimulationApplyTrade(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	simTrader, err := trader.NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	// test buy with 5000 USDT
	buyPrice := decimal.NewFromInt(50000)
	buyUSDT := decimal.NewFromInt(5000)
	err = simTrader.ApplyTrade(buyPrice, buyUSDT, "buy")
	require.NoError(t, err)

	btcAfterBuy, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterBuy, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	expectedBTC := buyUSDT.Div(buyPrice) // 5000 / 50000 = 0.1
	assert.True(t, btcAfterBuy.Equal(expectedBTC))
	assert.True(t, usdtAfterBuy.Equal(decimal.NewFromInt(5000))) // 10000 - 5000

	// test sell
	sellPrice := decimal.NewFromInt(60000)
	sellAmount := decimal.NewFromFloat(0.05)
	err = simTrader.ApplyTrade(sellPrice, sellAmount, "sell")
	require.NoError(t, err)

	btcAfterSell, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterSell, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, btcAfterSell.Equal(decimal.NewFromFloat(0.05))) // 0.1 - 0.05
	assert.True(t, usdtAfterSell.Equal(decimal.NewFromInt(8000)))  // 5000 + 60000*0.05
}
