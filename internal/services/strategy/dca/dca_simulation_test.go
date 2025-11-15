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

func setupSimStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("MARTI_SIMULATE_STATE_DIR", t.TempDir())
}

// mockSimulatePricer is a simple pricer for testing simulation
type mockSimulatePricer struct {
	price decimal.Decimal
}

func (m *mockSimulatePricer) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	return m.price, nil
}

func TestDCAStrategy_WithSimulationTrader(t *testing.T) {
	setupSimStateDir(t)
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	// pricer for simulation
	price := decimal.NewFromInt(50000)
	pr := &mockSimulatePricer{price: price}

	// create simulation trader
	simTrader, err := trader.NewSimulateTrader(pair, entity.MarketTypeSpot, 1, logger, pr, "")
	require.NoError(t, err)

	// verify initial balances
	initialBTC, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	initialUSDT, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, initialBTC.Equal(decimal.Zero))
	assert.True(t, initialUSDT.Equal(decimal.NewFromInt(10000)))

	// simulate a buy trade with 5000 USDT -> 0.1 BTC at 50000
	usdtAmount := decimal.NewFromInt(5000)
	baseAmount := usdtAmount.Div(price) // 0.1 BTC

	err = simTrader.ExecuteAction(context.Background(), entity.ActionOpenLong, baseAmount, "order-1")
	require.NoError(t, err)

	// verify balance updated
	btcBalance, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	expectedBTC := baseAmount                                            // 0.1
	expectedUSDT := decimal.NewFromInt(10000).Sub(baseAmount.Mul(price)) // 10000 - 0.1*50000 = 5000

	assert.True(t, btcBalance.Equal(expectedBTC), "BTC balance should be %s, got %s", expectedBTC, btcBalance)
	assert.True(t, usdtBalance.Equal(expectedUSDT), "USDT balance should be %s, got %s", expectedUSDT, usdtBalance)
}

func TestDCAStrategy_SimulationApplyTrade(t *testing.T) {
	setupSimStateDir(t)
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	// pricer for simulation
	pr := &mockSimulatePricer{}

	simTrader, err := trader.NewSimulateTrader(pair, entity.MarketTypeSpot, 1, logger, pr, "")
	require.NoError(t, err)

	// test buy with 5000 USDT (0.1 BTC at 50000)
	buyPrice := decimal.NewFromInt(50000)
	pr.price = buyPrice
	buyUSDT := decimal.NewFromInt(5000)
	buyBase := buyUSDT.Div(buyPrice) // 0.1
	err = simTrader.ExecuteAction(context.Background(), entity.ActionOpenLong, buyBase, "order-buy")
	require.NoError(t, err)

	btcAfterBuy, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterBuy, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	expectedBTC := buyBase // 0.1
	assert.True(t, btcAfterBuy.Equal(expectedBTC))
	assert.True(t, usdtAfterBuy.Equal(decimal.NewFromInt(5000))) // 10000 - 5000

	// test sell 0.05 BTC at 60000
	sellPrice := decimal.NewFromInt(60000)
	pr.price = sellPrice
	sellAmount := decimal.NewFromFloat(0.05)
	err = simTrader.ExecuteAction(context.Background(), entity.ActionCloseLong, sellAmount, "order-sell")
	require.NoError(t, err)

	btcAfterSell, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterSell, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, btcAfterSell.Equal(decimal.NewFromFloat(0.05))) // 0.1 - 0.05
	assert.True(t, usdtAfterSell.Equal(decimal.NewFromInt(8000)))  // 5000 + 60000*0.05
}
