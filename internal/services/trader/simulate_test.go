package trader

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/entity"
	"go.uber.org/zap"
)

func TestSimulateTrader_NewSimulateTrader(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()

	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)
	require.NotNil(t, trader)

	// check initial balances
	btcBalance, err := trader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := trader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)
	assert.True(t, btcBalance.Equal(decimal.Zero))
	assert.True(t, usdtBalance.Equal(decimal.NewFromInt(10000)))
}

func TestSimulateTrader_Buy(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	ctx := context.Background()
	amount := decimal.NewFromFloat(0.1)
	orderID := "test-order-1"

	err = trader.Buy(ctx, amount, orderID)
	require.NoError(t, err)

	// verify order is recorded
	executed, filledAmount, err := trader.OrderExecuted(ctx, orderID)
	require.NoError(t, err)
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(amount))
}

func TestSimulateTrader_Sell_InsufficientBalance(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	ctx := context.Background()
	amount := decimal.NewFromFloat(1.0)
	orderID := "test-order-sell"

	// try to sell without having any BTC
	err = trader.Sell(ctx, amount, orderID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
}

func TestSimulateTrader_ApplyTrade_Buy(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	price := decimal.NewFromInt(50000)
	usdtAmount := decimal.NewFromInt(5000) // spending 5000 USDT

	err = trader.ApplyTrade(price, usdtAmount, "buy")
	require.NoError(t, err)

	// verify balances updated correctly
	btcBalance, err := trader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := trader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	expectedBTC := usdtAmount.Div(price) // 5000 / 50000 = 0.1 BTC
	expectedUSDT := decimal.NewFromInt(10000).Sub(usdtAmount) // 10000 - 5000 = 5000

	assert.True(t, btcBalance.Equal(expectedBTC), "BTC balance should be %s, got %s", expectedBTC, btcBalance)
	assert.True(t, usdtBalance.Equal(expectedUSDT), "USDT balance should be %s, got %s", expectedUSDT, usdtBalance)
}

func TestSimulateTrader_ApplyTrade_Sell(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	// first buy some BTC with 5000 USDT
	buyPrice := decimal.NewFromInt(50000)
	buyUSDT := decimal.NewFromInt(5000)
	err = trader.ApplyTrade(buyPrice, buyUSDT, "buy")
	require.NoError(t, err)

	// now sell some BTC (0.05 BTC)
	sellPrice := decimal.NewFromInt(55000)
	sellBTC := decimal.NewFromFloat(0.05)
	err = trader.ApplyTrade(sellPrice, sellBTC, "sell")
	require.NoError(t, err)

	// verify balances
	btcBalance, err := trader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := trader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	// bought: 5000/50000 = 0.1 BTC, spent 5000 USDT
	// sold: 0.05 BTC, received 0.05 * 55000 = 2750 USDT
	expectedBTC := buyUSDT.Div(buyPrice).Sub(sellBTC) // 0.1 - 0.05 = 0.05
	expectedUSDT := decimal.NewFromInt(10000).Sub(buyUSDT).Add(sellPrice.Mul(sellBTC)) // 10000 - 5000 + 2750 = 7750

	assert.True(t, btcBalance.Equal(expectedBTC), "BTC balance should be %s, got %s", expectedBTC, btcBalance)
	assert.True(t, usdtBalance.Equal(expectedUSDT), "USDT balance should be %s, got %s", expectedUSDT, usdtBalance)
}

func TestSimulateTrader_ApplyTrade_InsufficientBalance(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	// try to buy more USDT worth than we have
	price := decimal.NewFromInt(50000)
	usdtAmount := decimal.NewFromInt(50000) // trying to spend 50000 USDT, but we only have 10000
	err = trader.ApplyTrade(price, usdtAmount, "buy")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
}

func TestSimulateTrader_OrderExecuted_NotFound(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	ctx := context.Background()
	executed, filledAmount, err := trader.OrderExecuted(ctx, "non-existent-order")
	require.NoError(t, err)
	// In simulation mode, unknown orders (e.g., from previous session) are assumed executed with zero amount
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(decimal.Zero))
}

func TestSimulateTrader_FullTradeCycle(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	trader, err := NewSimulateTrader(pair, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// initial balances
	initialBTC, err := trader.GetBalance(ctx, "BTC")
	require.NoError(t, err)
	initialUSDT, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)
	assert.True(t, initialBTC.Equal(decimal.Zero))
	assert.True(t, initialUSDT.Equal(decimal.NewFromInt(10000)))

	// buy cycle - buy with 5000 USDT
	buyPrice := decimal.NewFromInt(50000)
	buyUSDT := decimal.NewFromInt(5000)
	buyOrderID := "buy-order-1"

	err = trader.Buy(ctx, buyUSDT, buyOrderID)
	require.NoError(t, err)

	err = trader.ApplyTrade(buyPrice, buyUSDT, "buy")
	require.NoError(t, err)

	// verify buy executed
	executed, filledAmount, err := trader.OrderExecuted(ctx, buyOrderID)
	require.NoError(t, err)
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(buyUSDT))

	// check balances after buy
	btcAfterBuy, err := trader.GetBalance(ctx, "BTC")
	require.NoError(t, err)
	usdtAfterBuy, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)
	expectedBTC := buyUSDT.Div(buyPrice) // 5000 / 50000 = 0.1
	assert.True(t, btcAfterBuy.Equal(expectedBTC))
	assert.True(t, usdtAfterBuy.Equal(decimal.NewFromInt(5000))) // 10000 - 5000

	// sell cycle
	sellPrice := decimal.NewFromInt(60000)
	sellAmount := decimal.NewFromFloat(0.05)
	sellOrderID := "sell-order-1"

	err = trader.Sell(ctx, sellAmount, sellOrderID)
	require.NoError(t, err)

	err = trader.ApplyTrade(sellPrice, sellAmount, "sell")
	require.NoError(t, err)

	// verify sell executed
	executed, filledAmount, err = trader.OrderExecuted(ctx, sellOrderID)
	require.NoError(t, err)
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(sellAmount))

	// check final balances
	finalBTC, err := trader.GetBalance(ctx, "BTC")
	require.NoError(t, err)
	finalUSDT, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)
	expectedFinalBTC := expectedBTC.Sub(sellAmount) // 0.1 - 0.05 = 0.05
	expectedFinalUSDT := usdtAfterBuy.Add(sellPrice.Mul(sellAmount)) // 5000 + 60000*0.05 = 8000
	assert.True(t, finalBTC.Equal(expectedFinalBTC))
	assert.True(t, finalUSDT.Equal(expectedFinalUSDT))
}
