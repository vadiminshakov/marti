package averaging

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/exchange/trader"
	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
)

func setupSimStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("MARTI_SIMULATE_STATE_DIR", t.TempDir())
}

func TestStrategy_WithSimulationTrader(t *testing.T) {
	setupSimStateDir(t)

	pair := domain.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	price := decimal.NewFromInt(50000)
	pr := pricerMock.NewPricer(t)
	pr.On("GetPrice", mock.Anything, pair).Return(func(context.Context, domain.Pair) decimal.Decimal {
		return price
	}, nil)

	simTrader, err := trader.NewSimulateTrader(pair, domain.MarketTypeSpot, 1, logger, pr, t.Name())
	require.NoError(t, err)

	initialBTC, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	initialUSDT, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, initialBTC.Equal(decimal.Zero))
	assert.True(t, initialUSDT.Equal(decimal.NewFromInt(10000)))

	usdtAmount := decimal.NewFromInt(5000)
	baseAmount := usdtAmount.Div(price)

	err = simTrader.ExecuteAction(context.Background(), domain.ActionOpenLong, baseAmount, "order-1")
	require.NoError(t, err)

	btcBalance, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	expectedBTC := baseAmount
	expectedUSDT := decimal.NewFromInt(10000).Sub(baseAmount.Mul(price))

	assert.True(t, btcBalance.Equal(expectedBTC), "BTC balance should be %s, got %s", expectedBTC, btcBalance)
	assert.True(t, usdtBalance.Equal(expectedUSDT), "USDT balance should be %s, got %s", expectedUSDT, usdtBalance)
}

func TestStrategy_SimulationApplyTrade(t *testing.T) {
	setupSimStateDir(t)

	pair := domain.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	price := decimal.Zero
	pr := pricerMock.NewPricer(t)
	pr.On("GetPrice", mock.Anything, pair).Return(func(context.Context, domain.Pair) decimal.Decimal {
		return price
	}, nil)

	simTrader, err := trader.NewSimulateTrader(pair, domain.MarketTypeSpot, 1, logger, pr, t.Name())
	require.NoError(t, err)

	buyPrice := decimal.NewFromInt(50000)
	price = buyPrice
	buyUSDT := decimal.NewFromInt(5000)
	buyBase := buyUSDT.Div(buyPrice)
	err = simTrader.ExecuteAction(context.Background(), domain.ActionOpenLong, buyBase, "order-buy")
	require.NoError(t, err)

	btcAfterBuy, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterBuy, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	assert.True(t, btcAfterBuy.Equal(buyBase))
	assert.True(t, usdtAfterBuy.Equal(decimal.NewFromInt(5000)))

	sellPrice := decimal.NewFromInt(60000)
	price = sellPrice
	sellAmount := decimal.NewFromFloat(0.05)
	err = simTrader.ExecuteAction(context.Background(), domain.ActionCloseLong, sellAmount, "order-sell")
	require.NoError(t, err)

	btcAfterSell, err := simTrader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtAfterSell, err := simTrader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, btcAfterSell.Equal(decimal.NewFromFloat(0.05)))
	assert.True(t, usdtAfterSell.Equal(decimal.NewFromInt(8000)))
}
