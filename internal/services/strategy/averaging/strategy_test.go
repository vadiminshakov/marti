package averaging

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
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/internal/domain"
	recorderMock "github.com/vadiminshakov/marti/mocks/decisionrecorder"
	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
)

func decimalMatcher(expected decimal.Decimal) interface{} {
	return mock.MatchedBy(expected.Equal)
}

// mockStandardBalances sets up standard balance mocks for tests.
func mockStandardBalances(mockTrader *traderMock.Trader) {
	mockTrader.On("GetBalance", mock.Anything, "USDT").Return(decimal.NewFromInt(10000), nil).Maybe()
	mockTrader.On("GetBalance", mock.Anything, "BTC").Return(decimal.Zero, nil).Maybe()
}

func createTestStrategy(t *testing.T, pricer Pricer, trader Tradersvc) (*Strategy, *recorderMock.DecisionRecorder) {
	logger := zap.NewNop()
	pair := domain.Pair{From: "BTC", To: "USDT"}
	amountPercent := decimal.NewFromInt(10) // 10% of balance

	tempDir, err := os.MkdirTemp("", "test_wal_*")
	require.NoError(t, err, "failed to create temp directory")

	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	recorder := recorderMock.NewDecisionRecorder(t)

	ts, err := createStrategyWithWALDir(logger, pair, amountPercent, pricer, trader, recorder, 4, decimal.NewFromInt(5), decimal.NewFromInt(10), tempDir)
	require.NoError(t, err, "failed to create strategy")

	return ts, recorder
}

func createStrategyWithWALDir(l *zap.Logger, pair domain.Pair, amountPercent decimal.Decimal, pricer Pricer, trader Tradersvc, recorder DecisionRecorder,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal, walDir string,
) (*Strategy, error) {
	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("maxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	if amountPercent.LessThan(decimal.NewFromInt(1)) || amountPercent.GreaterThan(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("amountPercent must be between 1 and 100, got %s", amountPercent.String())
	}

	stateKey := sanitizeStateKey(pair.String())
	seriesKey := fmt.Sprintf("%s%s", dcaSeriesKeyPrefix, stateKey)

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

	dcaSeries := domain.NewDCASeries()

	thresholds, err := domain.NewDCAThresholds(dcaPercentThresholdBuy, dcaPercentThresholdSell, maxDcaTrades)
	if err != nil {
		return nil, fmt.Errorf("failed to create thresholds: %w", err)
	}

	thresholdsCopy := thresholds

	return &Strategy{
		pair:               pair,
		amountPercent:      amountPercent,
		tradePart:          decimal.Zero,
		pricer:             pricer,
		trader:             trader,
		recorder:           recorder,
		l:                  l,
		wal:                wal,
		journal:            newTradeJournal(wal, []*tradeIntentRecord{}),
		dcaSeries:          dcaSeries,
		thresholds:         &thresholdsCopy,
		orderCheckInterval: time.Millisecond,
		seriesKey:          seriesKey,
		positionSizer:      EqualSizer,
		strategyName:       "dca",
	}, nil
}

func TestStrategy_Initialize(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50000), nil)
	mockTrader.On("GetBalance", mock.Anything, "USDT").Return(decimal.NewFromInt(10000), nil)
	mockTrader.On("GetBalance", mock.Anything, "BTC").Return(decimal.Zero, nil)
	// maxDcaTrades is 4, so initial buy is 1000 / 4 = 250
	expectedBuyBase := decimal.NewFromInt(250).Div(decimal.NewFromInt(50000)).RoundFloor(8)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(expectedBuyBase), mock.AnythingOfType("string")).Return(nil)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.Anything).Return(nil)
	defer ts.Close()

	ctx := context.Background()
	err := ts.Initialize(ctx)
	require.NoError(t, err, "Initialize should succeed")
	require.Equal(t, 1, len(ts.GetDCASeries().Purchases), "Should have one purchase after initialize")
}

func TestStrategy_Trade_NoPriceData(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.Zero, errors.New("price fetch error"))

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)

	require.Error(t, err, "expected error when pricer fails")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when pricer fails")
}

func TestSanitizeStateKey(t *testing.T) {
	t.Parallel()

	require.Equal(t, "binance_btc_usdt_dca_spot", sanitizeStateKey("binance__BTC_USDT__dca__spot"))
	require.Equal(t, "default", sanitizeStateKey(" /// "))
}

func TestStrategy_Trade_NoExistingPurchases(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50000), nil)

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "expected no error when no existing purchases")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when no existing purchases")
}

func TestStrategy_Trade_WaitingForDip_PriceDropped(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // 10% drop from 50000
	mockStandardBalances(mockTrader)

	// maxDcaTrades is 4, so buy amount is 1000 / 4 = 250
	expectedBuyBase := decimal.NewFromInt(250).Div(decimal.NewFromInt(45000)).RoundFloor(8)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(expectedBuyBase), mock.AnythingOfType("string")).Return(nil)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.Anything).Return(nil)
	defer ts.Close()

	ts.updateSellState(decimal.NewFromInt(50000), true)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent when price drops during dip waiting")
	require.Equal(t, domain.ActionOpenLong, tradeEvent.Action, "expected OpenLong action")
	require.True(t, tradeEvent.Amount.Equal(expectedBuyBase), "expected amount %s, got %s", expectedBuyBase, tradeEvent.Amount)
}

func TestStrategy_Trade_WaitingForDip_PriceNotDroppedEnough(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(48000), nil) // only 4% drop from 50000

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	ts.updateSellState(decimal.NewFromInt(50000), true)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when price hasn't dropped enough")
}

func TestStrategy_Trade_DCABuy_PriceSignificantlyLower(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // significantly lower price
	mockStandardBalances(mockTrader)

	// maxDcaTrades is 4, so buy amount is 1000 / 4 = 250
	expectedBuyBase := decimal.NewFromInt(250).Div(decimal.NewFromInt(45000)).RoundFloor(8)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(expectedBuyBase), mock.AnythingOfType("string")).Return(nil)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.Anything).Return(nil)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err, "Failed to add initial DCA purchase")

	ts.tradePart = decimal.NewFromInt(1)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for DCA buy")
	require.Equal(t, domain.ActionOpenLong, tradeEvent.Action, "expected OpenLong action")
	require.True(t, tradeEvent.Amount.Equal(expectedBuyBase), "expected amount %s, got %s", expectedBuyBase, tradeEvent.Amount)
}

func TestStrategy_Trade_DCABuy_MaxTradesReached(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil) // significantly lower price

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add 4 purchases to reach max trades (maxDcaTrades = 4)
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(49000), decimal.NewFromInt(1000), time.Now(), 2)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(48000), decimal.NewFromInt(1000), time.Now(), 3)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(47000), decimal.NewFromInt(1000), time.Now(), 4)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.Nil(t, tradeEvent, "expected nil TradeEvent when max DCA trades reached")
}

func TestStrategy_Trade_Sell_PriceSignificantlyHigher(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(55500), nil) // 11% higher than avg entry (>10% threshold)

	// Purchase: 1000 USDT at price 50000 = 0.005 BTC
	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))

	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(nil)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.Anything).Return(nil)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for sell")
	require.Equal(t, domain.ActionCloseLong, tradeEvent.Action, "expected CloseLong action")
}

func TestStrategy_Trade_Sell_FullSellOnDoubleThreshold(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(61000), nil) // 22% higher

	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))

	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(nil)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.Anything).Return(nil)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err, "unexpected error")
	require.NotNil(t, tradeEvent, "expected TradeEvent for sell")
	require.Equal(t, domain.ActionCloseLong, tradeEvent.Action, "expected CloseLong action")
	require.True(t, tradeEvent.Amount.Equal(expectedSellBTC), "expected full sell amount %v BTC, got %v", expectedSellBTC, tradeEvent.Amount)
}

func TestStrategy_Trade_Sell_SavesTradePartForSoldLayer(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(55500), nil).Once()
	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(62000), nil).Once()
	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(69000), nil).Once()

	mockStandardBalances(mockTrader)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, mock.Anything, mock.AnythingOfType("string")).Return(nil).Times(3)

	ts, recorder := createTestStrategy(t, mockPricer, mockTrader)
	recorder.On("SaveAveraging", mock.MatchedBy(func(event domain.AveragingDecisionEvent) bool {
		return event.Action == "sell" && event.TradePart == 3
	})).Return(nil).Once()
	recorder.On("SaveAveraging", mock.MatchedBy(func(event domain.AveragingDecisionEvent) bool {
		return event.Action == "sell" && event.TradePart == 2
	})).Return(nil).Once()
	recorder.On("SaveAveraging", mock.MatchedBy(func(event domain.AveragingDecisionEvent) bool {
		return event.Action == "sell" && event.TradePart == 1
	})).Return(nil).Once()
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 2)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 3)
	require.NoError(t, err)

	ctx := context.Background()

	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err)
	require.NotNil(t, tradeEvent)
	require.Equal(t, 2, len(ts.GetDCASeries().Purchases))

	tradeEvent, err = ts.Trade(ctx)
	require.NoError(t, err)
	require.NotNil(t, tradeEvent)
	require.Equal(t, 1, len(ts.GetDCASeries().Purchases))

	tradeEvent, err = ts.Trade(ctx)
	require.NoError(t, err)
	require.NotNil(t, tradeEvent)
	require.Len(t, ts.GetDCASeries().Purchases, 0)
}

func TestStrategy_Trade_NoAction_PriceInRange(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(50500), nil) // only 1% change

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.NoError(t, err)
	require.Nil(t, tradeEvent)
}

func TestStrategy_Trade_BuyError(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(45000), nil)
	mockStandardBalances(mockTrader)

	expectedBuyBase := decimal.NewFromInt(250).Div(decimal.NewFromInt(45000)).RoundFloor(8)
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionOpenLong, decimalMatcher(expectedBuyBase), mock.AnythingOfType("string")).Return(errors.New("buy failed"))

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.Error(t, err)
	require.Nil(t, tradeEvent)
}

func TestStrategy_Trade_SellError(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	mockPricer.On("GetPrice", mock.Anything, pair).Return(decimal.NewFromInt(55500), nil)

	expectedSellBTC := decimal.NewFromInt(1000).Div(decimal.NewFromInt(50000))
	mockTrader.On("ExecuteAction", mock.Anything, domain.ActionCloseLong, decimalMatcher(expectedSellBTC), mock.AnythingOfType("string")).Return(errors.New("sell failed"))

	ts, _ := createTestStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), decimal.NewFromInt(1000), time.Now(), 1)
	require.NoError(t, err)

	ctx := context.Background()
	tradeEvent, err := ts.Trade(ctx)
	require.Error(t, err)
	require.Nil(t, tradeEvent)
}

func TestStrategy_Initialize_WaitingForDip(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	currentPrice := decimal.NewFromInt(50000)
	mockPricer.On("GetPrice", mock.Anything, pair).Return(currentPrice, nil)
	mockTrader.On("GetBalance", mock.Anything, "BTC").Return(decimal.Zero, nil)
	mockTrader.On("GetBalance", mock.Anything, "USDT").Return(decimal.NewFromInt(1000), nil)

	tmpDir, err := os.MkdirTemp("", "wal_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	strategy, err := createStrategyWithWALDir(zap.NewNop(), pair, decimal.NewFromInt(10), mockPricer, mockTrader, recorderMock.NewDecisionRecorder(t), 10, decimal.NewFromInt(1), decimal.NewFromInt(5), tmpDir)
	require.NoError(t, err)

	strategy.dcaSeries.WaitingForDip = true
	strategy.dcaSeries.LastSellPrice = currentPrice

	err = strategy.Initialize(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, len(strategy.dcaSeries.Purchases))
}

func TestStrategy_SellAll_NoPosition_ReturnsError(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	currentPrice := decimal.NewFromInt(50000)
	mockPricer.On("GetPrice", mock.Anything, pair).Return(currentPrice, nil)
	mockTrader.On("GetPosition", mock.Anything, pair).Return(&domain.Position{Amount: decimal.Zero}, nil)

	tmpDir, err := os.MkdirTemp("", "wal_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	strategy, err := createStrategyWithWALDir(zap.NewNop(), pair, decimal.NewFromInt(10), mockPricer, mockTrader, recorderMock.NewDecisionRecorder(t), 10, decimal.NewFromInt(1), decimal.NewFromInt(5), tmpDir)
	require.NoError(t, err)

	err = strategy.SellAll(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no position to sell")
	require.False(t, strategy.dcaSeries.WaitingForDip)
}

func TestStrategy_SellAll_OutOfSync_FixesWAL(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	pair := domain.Pair{From: "BTC", To: "USDT"}

	currentPrice := decimal.NewFromInt(50000)
	mockPricer.On("GetPrice", mock.Anything, pair).Return(currentPrice, nil)
	mockTrader.On("GetPosition", mock.Anything, pair).Return(&domain.Position{Amount: decimal.Zero}, nil)

	tmpDir, err := os.MkdirTemp("", "wal_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	strategy, err := createStrategyWithWALDir(zap.NewNop(), pair, decimal.NewFromInt(10), mockPricer, mockTrader, recorderMock.NewDecisionRecorder(t), 10, decimal.NewFromInt(1), decimal.NewFromInt(5), tmpDir)
	require.NoError(t, err)

	err = strategy.AddDCAPurchase("intent-1", currentPrice, decimal.NewFromInt(100), time.Now(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, len(strategy.dcaSeries.Purchases))

	err = strategy.SellAll(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, len(strategy.dcaSeries.Purchases))
	require.True(t, strategy.dcaSeries.WaitingForDip)
}
