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

// mockPricer is a simple mock for the Pricer interface.
type mockPricer struct {
	price decimal.Decimal
}

func (m *mockPricer) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	return m.price, nil
}

func TestSimulateTrader_NewSimulateTrader(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	pricer := &mockPricer{price: decimal.NewFromInt(50000)}

	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)
	require.NotNil(t, trader)
	assert.NotNil(t, trader.pricer)

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
	pricer := &mockPricer{price: decimal.NewFromInt(50000)}
	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)

	ctx := context.Background()
	amount := decimal.NewFromFloat(0.1) // 0.1 BTC
	orderID := "test-order-1"

	err = trader.Buy(ctx, amount, orderID)
	require.NoError(t, err)

	// verify order is recorded
	executed, filledAmount, err := trader.OrderExecuted(ctx, orderID)
	require.NoError(t, err)
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(amount))

	// verify balances updated correctly
	btcBalance, err := trader.GetBalance(context.Background(), "BTC")
	require.NoError(t, err)
	usdtBalance, err := trader.GetBalance(context.Background(), "USDT")
	require.NoError(t, err)

	expectedBTC := amount
	expectedUSDT := decimal.NewFromInt(10000).Sub(amount.Mul(pricer.price)) // 10000 - 0.1*50000 = 5000

	assert.True(t, btcBalance.Equal(expectedBTC))
	assert.True(t, usdtBalance.Equal(expectedUSDT))
}

func TestSimulateTrader_Sell_InsufficientBalance(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	pricer := &mockPricer{price: decimal.NewFromInt(50000)}
	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)

	ctx := context.Background()
	amount := decimal.NewFromFloat(1.0)
	orderID := "test-order-sell"

	// try to sell without having any BTC
	err = trader.Sell(ctx, amount, orderID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
}

func TestSimulateTrader_Buy_InsufficientBalance(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	pricer := &mockPricer{price: decimal.NewFromInt(50000)}
	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)

	// try to buy more than we can afford
	amount := decimal.NewFromFloat(0.3) // 0.3 * 50000 = 15000 USDT, but we only have 10000
	err = trader.Buy(context.Background(), amount, "some-order")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
}

func TestSimulateTrader_OrderExecuted_NotFound(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	pricer := &mockPricer{}
	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)

	ctx := context.Background()
	executed, filledAmount, err := trader.OrderExecuted(ctx, "non-existent-order")
	require.NoError(t, err)
	assert.True(t, executed)
	assert.True(t, filledAmount.Equal(decimal.Zero))
}

func TestSimulateTrader_FullTradeCycle(t *testing.T) {
	pair := entity.Pair{From: "BTC", To: "USDT"}
	logger := zap.NewNop()
	pricer := &mockPricer{}
	trader, err := NewSimulateTrader(pair, logger, pricer)
	require.NoError(t, err)

	ctx := context.Background()

	// initial balances
	initialUSDT, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)
	assert.True(t, initialUSDT.Equal(decimal.NewFromInt(10000)))

	// buy 0.1 BTC at 50000
	pricer.price = decimal.NewFromInt(50000)
	buyAmountBTC := decimal.NewFromFloat(0.1)
	buyOrderID := "buy-order-1"

	err = trader.Buy(ctx, buyAmountBTC, buyOrderID)
	require.NoError(t, err)

	// check balances after buy
	btcAfterBuy, err := trader.GetBalance(ctx, "BTC")
	require.NoError(t, err)
	usdtAfterBuy, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)
	assert.True(t, btcAfterBuy.Equal(buyAmountBTC))              // should have 0.1 BTC
	assert.True(t, usdtAfterBuy.Equal(decimal.NewFromInt(5000))) // 10000 - (0.1 * 50000)

	// sell 0.05 BTC at 60000
	pricer.price = decimal.NewFromInt(60000)
	sellAmountBTC := decimal.NewFromFloat(0.05)
	sellOrderID := "sell-order-1"

	err = trader.Sell(ctx, sellAmountBTC, sellOrderID)
	require.NoError(t, err)

	// check final balances
	finalBTC, err := trader.GetBalance(ctx, "BTC")
	require.NoError(t, err)
	finalUSDT, err := trader.GetBalance(ctx, "USDT")
	require.NoError(t, err)

	expectedFinalBTC := buyAmountBTC.Sub(sellAmountBTC)                      // 0.1 - 0.05 = 0.05
	expectedFinalUSDT := usdtAfterBuy.Add(pricer.price.Mul(sellAmountBTC)) // 5000 + (60000 * 0.05) = 8000
	assert.True(t, finalBTC.Equal(expectedFinalBTC))
	assert.True(t, finalUSDT.Equal(expectedFinalUSDT))
}
