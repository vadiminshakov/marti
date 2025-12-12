package dca

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
)

// testBuyAmount is the standard buy amount for tests (10% of 10000 USDT = 1000 USDT).
var testBuyAmount = decimal.NewFromInt(1000)

func TestDCAStrategy_ReconcileExecutedBuyIntent(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:        "intent-buy-1",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    testBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// OrderExecuted returns filledAmount in BASE currency (e.g., BTC).
	// intent.Amount is in QUOTE (1000 USDT), price is 48000, so base = 1000/48000.
	filledBaseAmount := intent.Amount.Div(intent.Price)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, filledBaseAmount, nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Len(t, ts.dcaSeries.Purchases, 1, "executed buy intent should add purchase")
	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent status should be marked done")
	require.True(t, ts.isTradeProcessed(intent.ID), "intent should be marked processed")

	// ensure idempotence on subsequent reconciliation
	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)
	require.Len(t, ts.dcaSeries.Purchases, 1, "purchase count should remain unchanged after second reconciliation")
}

func TestDCAStrategy_ReconcileExecutedSellIntentFullReset(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// seed series with one purchase
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), testBuyAmount, time.Now(), 1)
	require.NoError(t, err)
	require.Equal(t, testBuyAmount, ts.dcaSeries.TotalAmount)

	intent := &tradeIntentRecord{
		ID:         "intent-sell-full",
		Status:     tradeIntentStatusPending,
		Action:     intentActionSell,
		Amount:     ts.dcaSeries.TotalAmount,
		Price:      decimal.NewFromInt(60000),
		Time:       time.Now(),
		IsFullSell: true,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// OrderExecuted returns filledAmount in BASE currency.
	filledBaseAmount := intent.Amount.Div(intent.Price)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, filledBaseAmount, nil)

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent status should be done")
	require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset after full sell")
	require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after full sell")
	require.True(t, ts.dcaSeries.LastSellPrice.Equal(intent.Price), "last sell price should be updated")
	require.True(t, ts.dcaSeries.TotalAmount.IsZero(), "total amount should be zero after reset")
}

func TestDCAStrategy_ReconcilePendingIntentPartialFillThenComplete(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), testBuyAmount, time.Now(), 1)
	require.NoError(t, err)

	intent := &tradeIntentRecord{
		ID:     "intent-partial-done",
		Status: tradeIntentStatusPending,
		Action: intentActionSell,
		Amount: testBuyAmount,
		Price:  decimal.NewFromInt(55000),
		Time:   time.Now(),
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	partialFillQuote := testBuyAmount.Div(decimal.NewFromInt(2))
	partialFillBase := partialFillQuote.Div(intent.Price)
	fullFillBase := testBuyAmount.Div(intent.Price)

	// return partial fill a few times, then complete.
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(false, partialFillBase, nil).Times(3)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, fullFillBase, nil).Once()

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be marked done after completion")
	require.True(t, ts.isTradeProcessed(intent.ID), "intent should be marked processed after completion")
	require.True(t, ts.journal.index[intent.ID].Amount.Equal(testBuyAmount), "intent amount should be updated to full quote amount")
	require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset after full sell is executed")
	require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after completing sell")
}

func TestDCAStrategy_ReconcilePartialSell(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add two purchases to have enough balance
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), testBuyAmount, time.Now(), 1)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(50000), testBuyAmount, time.Now(), 2)
	require.NoError(t, err)

	initialTotal := ts.dcaSeries.TotalAmount
	sellAmount := testBuyAmount // sell only one purchase worth

	intent := &tradeIntentRecord{
		ID:         "intent-partial-sell",
		Status:     tradeIntentStatusPending,
		Action:     intentActionSell,
		Amount:     sellAmount,
		Price:      decimal.NewFromInt(55000),
		Time:       time.Now(),
		IsFullSell: false,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// OrderExecuted returns filledAmount in BASE currency.
	// sellAmount is in QUOTE, convert to base.
	filledBaseAmount := sellAmount.Div(intent.Price)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, filledBaseAmount, nil)

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent status should be done")
	require.False(t, ts.dcaSeries.WaitingForDip, "strategy should not wait for dip after partial sell")
	require.True(t, ts.dcaSeries.TotalAmount.Equal(initialTotal.Sub(sellAmount)), "total amount should be reduced by sell amount")
	require.Greater(t, len(ts.dcaSeries.Purchases), 0, "purchases should remain after partial sell")
}

func TestDCAStrategy_ReconcileValidationFailureZeroAmount(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:     "intent-zero-fill",
		Status: tradeIntentStatusPending,
		Action: intentActionBuy,
		Amount: testBuyAmount,
		Price:  decimal.NewFromInt(48000),
		Time:   time.Now(),
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// simulate executed order with zero filled amount (invalid state)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, decimal.Zero, nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusFailed, ts.journal.index[intent.ID].Status, "intent should be marked failed when filled amount is zero")
	require.Len(t, ts.dcaSeries.Purchases, 0, "no purchases should be added for failed intent")
}

func TestDCAStrategy_ReconcileAlreadyProcessedIntent(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:        "intent-already-processed",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    testBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// manually apply the intent first
	err := ts.AddDCAPurchase(intent.ID, intent.Price, intent.Amount, intent.Time, intent.TradePart)
	require.NoError(t, err)

	// OrderExecuted should NOT be called because isTradeProcessed returns true
	// reconcile should not double-apply
	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be marked done")
	require.Len(t, ts.dcaSeries.Purchases, 1, "should have exactly one purchase, not doubled")
	require.True(t, ts.isTradeProcessed(intent.ID), "intent should be marked as processed")
}

func TestDCAStrategy_ReconcileAmountUpdateOnPartialFill(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	requestedAmount := testBuyAmount
	actualFilledAmount := requestedAmount.Mul(decimal.NewFromFloat(0.8)) // only 80% filled

	intent := &tradeIntentRecord{
		ID:        "intent-partial-amount",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    requestedAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// OrderExecuted returns filledAmount in BASE currency.
	// actualFilledAmount is in QUOTE (800 USDT), convert to base using intent.Price.
	filledBaseAmount := actualFilledAmount.Div(intent.Price)
	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, filledBaseAmount, nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be marked done")
	require.True(t, ts.journal.index[intent.ID].Amount.Equal(actualFilledAmount), "intent amount should be updated to actual filled amount (in quote)")
	require.Len(t, ts.dcaSeries.Purchases, 1, "should have one purchase")
	require.True(t, ts.dcaSeries.TotalAmount.Equal(actualFilledAmount), "total amount should match actual filled amount")
}

func TestDCAStrategy_ReconcileMultiplePendingIntents(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// create three buy intents
	intent1 := &tradeIntentRecord{
		ID:        "intent-buy-1",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    testBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	intent2 := &tradeIntentRecord{
		ID:        "intent-buy-2",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    testBuyAmount,
		Price:     decimal.NewFromInt(47000),
		Time:      time.Now(),
		TradePart: 2,
	}
	intent3 := &tradeIntentRecord{
		ID:        "intent-buy-3",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    testBuyAmount,
		Price:     decimal.NewFromInt(46000),
		Time:      time.Now(),
		TradePart: 3,
	}

	ts.journal.intents = append(ts.journal.intents, intent1, intent2, intent3)
	ts.journal.index[intent1.ID] = intent1
	ts.journal.index[intent2.ID] = intent2
	ts.journal.index[intent3.ID] = intent3

	// OrderExecuted returns filledAmount in BASE currency.
	mockTrader.On("OrderExecuted", mock.Anything, intent1.ID).Return(true, intent1.Amount.Div(intent1.Price), nil)
	mockTrader.On("OrderExecuted", mock.Anything, intent2.ID).Return(true, intent2.Amount.Div(intent2.Price), nil)
	mockTrader.On("OrderExecuted", mock.Anything, intent3.ID).Return(true, intent3.Amount.Div(intent3.Price), nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent1.ID].Status, "intent 1 should be done")
	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent2.ID].Status, "intent 2 should be done")
	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent3.ID].Status, "intent 3 should be done")
	require.Len(t, ts.dcaSeries.Purchases, 3, "should have three purchases")
	require.True(t, ts.dcaSeries.TotalAmount.Equal(testBuyAmount.Mul(decimal.NewFromInt(3))), "total should be sum of all purchases")
}

func TestDCAStrategy_ReconcilePartialSellLeadingToZeroBalance(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)
	mockStandardBalances(mockTrader)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add one purchase
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), testBuyAmount, time.Now(), 1)
	require.NoError(t, err)

	// partial sell intent but amount equals total (edge case: not marked as full sell but sells everything)
	intent := &tradeIntentRecord{
		ID:         "intent-partial-to-zero",
		Status:     tradeIntentStatusPending,
		Action:     intentActionSell,
		Amount:     testBuyAmount,
		Price:      decimal.NewFromInt(55000),
		Time:       time.Now(),
		IsFullSell: false, // intentionally not marked as full sell
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, intent.Amount.Div(intent.Price), nil)

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be done")
	require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset when partial sell brings balance to zero")
	require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after selling everything")
	require.True(t, ts.dcaSeries.TotalAmount.IsZero(), "total amount should be zero")
	require.True(t, ts.dcaSeries.LastSellPrice.Equal(intent.Price), "last sell price should be set")
}
