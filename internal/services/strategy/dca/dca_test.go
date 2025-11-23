package dca

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/domain"
	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
	"go.uber.org/zap"
)

func decimalMatcher(expected decimal.Decimal) interface{} {
	return mock.MatchedBy(func(actual decimal.Decimal) bool {
		return expected.Equal(actual)
	})
}

// mockStandardBalances sets up standard balance mocks for tests
// Returns 10000 USDT (so that 10% = 1000 USDT per trade)
// Returns 0 BTC initially
func mockStandardBalances(mockTrader *traderMock.Trader) {
	mockTrader.On("GetBalance", mock.Anything, "USDT").Return(decimal.NewFromInt(10000), nil).Maybe()
	mockTrader.On("GetBalance", mock.Anything, "BTC").Return(decimal.Zero, nil).Maybe()
}

func createTestDCAStrategy(t *testing.T, pricer pricer, trader tradersvc) *DCAStrategy {
	logger := zap.NewNop()
	pair := domain.Pair{From: "BTC", To: "USDT"}
	amountPercent := decimal.NewFromInt(10) // 10% of balance

	tempDir, err := os.MkdirTemp("", "test_wal_*")
	require.NoError(t, err, "Failed to create temp directory")

	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	ts, err := createDCAStrategyWithWALDir(logger, pair, amountPercent, pricer, trader, 4, decimal.NewFromInt(5), decimal.NewFromInt(10), tempDir)
	require.NoError(t, err, "Failed to create DCAStrategy")

	return ts
}

func TestDCAStrategy_Initialize(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50000), nil)
	mockTrader.On("GetBalance", mock.Anything, "USDT").Return(decimal.NewFromInt(10000), nil)
	mockTrader.On("GetBalance", mock.Anything, "BTC").Return(decimal.Zero, nil)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(decimal.NewFromInt(1000)), mock.AnythingOfType("string")).Return(nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ctx := context.Background()
	err := ts.Initialize(ctx)
	require.NoError(t, err, "Initialize should succeed")
	require.Equal(t, 1, len(ts.GetDCASeries().Purchases), "Should have one purchase after initialize")
}

func createDCAStrategyWithWALDir(l *zap.Logger, pair domain.Pair, amountPercent decimal.Decimal, pricer pricer, trader tradersvc,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal, walDir string) (*DCAStrategy, error) {

	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("MaxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	if amountPercent.LessThan(decimal.NewFromInt(1)) || amountPercent.GreaterThan(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("amountPercent must be between 1 and 100, got %s", amountPercent.String())
	}

	seriesKey := fmt.Sprintf("%s%s", dcaSeriesKeyPrefix, pair.String())

	walCfg := gowal.Config{
		Dir:              walDir,
		Prefix:           "log_",
		SegmentThreshold: 1000,
		MaxSegments:      100,
		IsInSyncDiskMode: true,
	}

	wal, err := gowal.NewWAL(walCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAL: %w", err)
	}

	dcaSeries := &DCASeries{
		Purchases:         make([]DCAPurchase, 0),
		ProcessedTradeIDs: make(map[string]bool),
	}

	return &DCAStrategy{
		pair:                    pair,
		amountPercent:           amountPercent,
		tradePart:               decimal.Zero,
		pricer:                  pricer,
		trader:                  trader,
		l:                       l,
		wal:                     wal,
		journal:                 newTradeJournal(wal, []*tradeIntentRecord{}),
		dcaSeries:               dcaSeries,
		maxDcaTrades:            maxDcaTrades,
		dcaPercentThresholdBuy:  dcaPercentThresholdBuy,
		dcaPercentThresholdSell: dcaPercentThresholdSell,
		seriesKey:               seriesKey,
		orderCheckInterval:      defaultOrderCheckInterval,
	}, nil
}

func TestDCAStrategy_Trade_NoPriceData(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.Zero, errors.New("price fetch error"))

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)

	require.Error(t, err, "expected error when pricer fails")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when pricer fails")
}

func TestDCAStrategy_Trade_NoExistingPurchases(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50000), nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "expected no error when no existing purchases")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when no existing purchases")
}

func TestDCAStrategy_Trade_WaitingForDip_PriceDropped(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // 10% drop from 50000
	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(decimal.NewFromInt(1000)), mock.AnythingOfType("string")).Return(nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ts.updateSellState(decimal.NewFromInt(50000), true)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent when price drops during dip waiting")
	require.Equal(t, domain.ActionOpenLong, tradeEvent.Action, "expected OpenLong action")
	require.True(t, tradeEvent.Amount.Equal(decimal.NewFromInt(1000)), "expected amount 1000, got %v", tradeEvent.Amount)
}

func TestDCAStrategy_Trade_WaitingForDip_PriceNotDroppedEnough(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(48000), nil) // only 4% drop from 50000

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ts.updateSellState(decimal.NewFromInt(50000), true)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when price hasn't dropped enough")
}

func TestDCAStrategy_Trade_DCABuy_PriceSignificantlyLower(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // significantly lower price
	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(decimal.NewFromInt(1000)), mock.AnythingOfType("string")).Return(nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "Failed to add initial DCA purchase")
	ts.tradePart = decimal.NewFromInt(1)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for DCA buy")
	require.Equal(t, domain.ActionOpenLong, tradeEvent.Action, "expected OpenLong action")
	require.True(t, tradeEvent.Amount.Equal(decimal.NewFromInt(1000)), "expected amount 1000, got %v", tradeEvent.Amount)
}

func TestDCAStrategy_Trade_DCABuy_MaxTradesReached(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // significantly lower price

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "Failed to add initial DCA purchase")
	ts.tradePart = decimal.NewFromInt(4) // Max trades reached

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when max DCA trades reached")
}

func TestDCAStrategy_Trade_Sell_PriceSignificantlyHigher(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(55500), nil) // 11% higher than avg entry (>10% threshold)

	// Purchase: 1000 USDT at price 50000 = 0.005 BTC
	// Expected sell amount: 0.005 BTC (1000 USDT / 50000 avg entry price)
	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))
	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "failed to add initial DCA purchase")

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for sell")
	require.Equal(t, domain.ActionCloseLong, tradeEvent.Action, "expected CloseLong action")
}

func TestDCAStrategy_Trade_Sell_FullSellOnDoubleThreshold(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(61000), nil) // 22% higher than avg entry (>20% double threshold)

	// Purchase: 1000 USDT at price 50000 = 0.005 BTC
	// Expected sell amount: 0.005 BTC (full position)
	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))
	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(nil)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "failed to add initial DCA purchase")

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for sell")
	require.Equal(t, domain.ActionCloseLong, tradeEvent.Action, "expected CloseLong action")
	require.True(t, tradeEvent.Amount.Equal(expectedSellBTC), "expected full sell amount %v BTC, got %v", expectedSellBTC, tradeEvent.Amount)
}

func TestDCAStrategy_Trade_NoAction_PriceInRange(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50500), nil) // only 1% change

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "failed to add initial DCA purchase")

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when price change is not significant")
}

func TestDCAStrategy_Trade_BuyError(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // significantly lower price
	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(decimal.NewFromInt(1000)), mock.AnythingOfType("string")).Return(errors.New("buy failed"))

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add initial purchase
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "failed to add initial DCA purchase")

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.Error(t, err, "expected error when buy fails")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when buy fails")
}

func TestDCAStrategy_Trade_SellError(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(55500), nil) // 11% higher than avg entry (>10% threshold)

	// Purchase: 1000 USDT at price 50000 = 0.005 BTC
	// Expected sell amount: 0.005 BTC
	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(errors.New("sell failed"))

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "failed to add initial DCA purchase")

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.Error(t, err, "expected error when sell fails")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when sell fails")
}

func TestIsPercentDifferenceSignificant(t *testing.T) {
	tests := []struct {
		name             string
		currentPrice     decimal.Decimal
		referencePrice   decimal.Decimal
		thresholdPercent decimal.Decimal
		expected         bool
	}{
		{
			name:             "ref zero, current zero, threshold positive",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // no difference (0 is not > threshold)
		},
		{
			name:             "ref zero, current non-zero, threshold positive",
			currentPrice:     decimal.NewFromInt(10),
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // reference price is zero, so false returned
		},
		{
			name:             "ref zero, current non-zero, threshold zero",
			currentPrice:     decimal.NewFromInt(10),
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.Zero,
			expected:         false, // reference price is zero, so false returned
		},
		{
			name:             "ref zero, current zero, threshold zero",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.Zero,
			thresholdPercent: decimal.Zero,
			expected:         false, // 0 is not > 0
		},
		{
			name:             "current zero, ref non-zero, threshold allows -100% (abs 100%)",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.NewFromInt(10),
			thresholdPercent: decimal.NewFromInt(99), // abs diff is 100%, 100 > 99 is true
			expected:         true,
		},
		{
			name:             "current zero, ref non-zero, threshold exactly 100%",
			currentPrice:     decimal.Zero,
			referencePrice:   decimal.NewFromInt(10),
			thresholdPercent: decimal.NewFromInt(100), // abs diff is 100%, threshold reached
			expected:         true,
		},
		{
			name:             "no change",
			currentPrice:     decimal.NewFromInt(100),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1),
			expected:         false, // 0% diff is not > 1%
		},
		{
			name:             "increase, below threshold",
			currentPrice:     decimal.NewFromFloat(100.5),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 0.5% change, 0.5 > 1 is false
			expected:         false,
		},
		{
			name:             "increase, at threshold (using > logic, so false)",
			currentPrice:     decimal.NewFromInt(101),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 1% change meets threshold
			expected:         true,
		},
		{
			name:             "increase, above threshold",
			currentPrice:     decimal.NewFromFloat(101.1),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // 1.1% change, 1.1 > 1 is true
			expected:         true,
		},
		{
			name:             "decrease, below threshold",
			currentPrice:     decimal.NewFromFloat(99.5),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 0.5% change, 0.5 > 1 is false
			expected:         false,
		},
		{
			name:             "decrease, at threshold (using > logic, so false)",
			currentPrice:     decimal.NewFromInt(99),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 1% change meets threshold
			expected:         true,
		},
		{
			name:             "decrease, above threshold",
			currentPrice:     decimal.NewFromFloat(98.9),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(1), // abs 1.1% change, 1.1 > 1 is true
			expected:         true,
		},
		{
			name:             "larger threshold, significant change",
			currentPrice:     decimal.NewFromInt(115),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(10), // 15% change, 15 > 10 is true
			expected:         true,
		},
		{
			name:             "larger threshold, insignificant change",
			currentPrice:     decimal.NewFromInt(105),
			referencePrice:   decimal.NewFromInt(100),
			thresholdPercent: decimal.NewFromInt(10), // 5% change, 5 > 10 is false
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPercentDifferenceSignificant(tt.currentPrice, tt.referencePrice, tt.thresholdPercent)
			require.Equal(t, tt.expected, got, "isPercentDifferenceSignificant(%s, %s, %s) = %v, want %v",
				tt.currentPrice.String(), tt.referencePrice.String(), tt.thresholdPercent.String(), got, tt.expected)
		})
	}
}
