package strategy

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pricerMock "github.com/vadiminshakov/marti/mocks/pricer"
	traderMock "github.com/vadiminshakov/marti/mocks/trader"
)

func TestDCAStrategy_ReconcileExecutedBuyIntent(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:        "intent-buy-1",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    ts.individualBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, intent.Amount, nil)

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

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// seed series with one purchase
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), ts.individualBuyAmount, time.Now(), 1)
	require.NoError(t, err)
	require.Equal(t, ts.individualBuyAmount, ts.dcaSeries.TotalAmount)

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

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, intent.Amount, nil)

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent status should be done")
	require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset after full sell")
	require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after full sell")
	require.True(t, ts.dcaSeries.LastSellPrice.Equal(intent.Price), "last sell price should be updated")
	require.True(t, ts.dcaSeries.TotalAmount.IsZero(), "total amount should be zero after reset")
}

func TestDCAStrategy_ReconcilePendingIntentPartialFillThenComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mockPricer := pricerMock.NewPricer(t)
		mockTrader := traderMock.NewTrader(t)

		ts := createTestDCAStrategy(t, mockPricer, mockTrader)
		defer ts.Close()

		ts.orderCheckInterval = time.Millisecond

		err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), ts.individualBuyAmount, time.Now(), 1)
		require.NoError(t, err)

		intent := &tradeIntentRecord{
			ID:     "intent-partial-done",
			Status: tradeIntentStatusPending,
			Action: intentActionSell,
			Amount: ts.individualBuyAmount,
			Price:  decimal.NewFromInt(55000),
			Time:   time.Now(),
		}
		ts.journal.intents = append(ts.journal.intents, intent)
		ts.journal.index[intent.ID] = intent

		partialFill := ts.individualBuyAmount.Div(decimal.NewFromInt(2))

		// return partial fill a few times, then complete
		callCount := 0
		mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(
			func(ctx context.Context, id string) bool {
				callCount++
				return callCount > 3 // execute after 3 checks
			},
			func(ctx context.Context, id string) decimal.Decimal {
				if callCount > 3 {
					return ts.individualBuyAmount // full amount when executed
				}
				return partialFill
			},
			func(ctx context.Context, id string) error {
				return nil
			},
		)

		err = ts.reconcileTradeIntents(context.Background())
		require.NoError(t, err)

		require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be marked done after completion")
		require.True(t, ts.isTradeProcessed(intent.ID), "intent should be marked processed after completion")
		require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset after full sell is executed")
		require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after completing sell")
	})
}

func TestDCAStrategy_ReconcilePartialSell(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add two purchases to have enough balance
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), ts.individualBuyAmount, time.Now(), 1)
	require.NoError(t, err)
	err = ts.AddDCAPurchase("", decimal.NewFromInt(50000), ts.individualBuyAmount, time.Now(), 2)
	require.NoError(t, err)

	initialTotal := ts.dcaSeries.TotalAmount
	sellAmount := ts.individualBuyAmount // sell only one purchase worth

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

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, sellAmount, nil)

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

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:     "intent-zero-fill",
		Status: tradeIntentStatusPending,
		Action: intentActionBuy,
		Amount: ts.individualBuyAmount,
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

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	intent := &tradeIntentRecord{
		ID:        "intent-already-processed",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    ts.individualBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	// manually apply the intent first
	err := ts.AddDCAPurchase(intent.ID, intent.Price, intent.Amount, intent.Time, intent.TradePart)
	require.NoError(t, err)

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, intent.Amount, nil)

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

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	requestedAmount := ts.individualBuyAmount
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

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, actualFilledAmount, nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be marked done")
	require.True(t, ts.journal.index[intent.ID].Amount.Equal(actualFilledAmount), "intent amount should be updated to actual filled amount")
	require.Len(t, ts.dcaSeries.Purchases, 1, "should have one purchase")
	require.True(t, ts.dcaSeries.TotalAmount.Equal(actualFilledAmount), "total amount should match actual filled amount")
}

func TestDCAStrategy_ReconcileMultiplePendingIntents(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// create three buy intents
	intent1 := &tradeIntentRecord{
		ID:        "intent-buy-1",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    ts.individualBuyAmount,
		Price:     decimal.NewFromInt(48000),
		Time:      time.Now(),
		TradePart: 1,
	}
	intent2 := &tradeIntentRecord{
		ID:        "intent-buy-2",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    ts.individualBuyAmount,
		Price:     decimal.NewFromInt(47000),
		Time:      time.Now(),
		TradePart: 2,
	}
	intent3 := &tradeIntentRecord{
		ID:        "intent-buy-3",
		Status:    tradeIntentStatusPending,
		Action:    intentActionBuy,
		Amount:    ts.individualBuyAmount,
		Price:     decimal.NewFromInt(46000),
		Time:      time.Now(),
		TradePart: 3,
	}

	ts.journal.intents = append(ts.journal.intents, intent1, intent2, intent3)
	ts.journal.index[intent1.ID] = intent1
	ts.journal.index[intent2.ID] = intent2
	ts.journal.index[intent3.ID] = intent3

	mockTrader.On("OrderExecuted", mock.Anything, intent1.ID).Return(true, intent1.Amount, nil)
	mockTrader.On("OrderExecuted", mock.Anything, intent2.ID).Return(true, intent2.Amount, nil)
	mockTrader.On("OrderExecuted", mock.Anything, intent3.ID).Return(true, intent3.Amount, nil)

	err := ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent1.ID].Status, "intent 1 should be done")
	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent2.ID].Status, "intent 2 should be done")
	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent3.ID].Status, "intent 3 should be done")
	require.Len(t, ts.dcaSeries.Purchases, 3, "should have three purchases")
	require.True(t, ts.dcaSeries.TotalAmount.Equal(ts.individualBuyAmount.Mul(decimal.NewFromInt(3))), "total should be sum of all purchases")
}

func TestDCAStrategy_ReconcilePartialSellLeadingToZeroBalance(t *testing.T) {
	mockPricer := pricerMock.NewPricer(t)
	mockTrader := traderMock.NewTrader(t)

	ts := createTestDCAStrategy(t, mockPricer, mockTrader)
	defer ts.Close()

	// add one purchase
	err := ts.AddDCAPurchase("", decimal.NewFromInt(50000), ts.individualBuyAmount, time.Now(), 1)
	require.NoError(t, err)

	// partial sell intent but amount equals total (edge case: not marked as full sell but sells everything)
	intent := &tradeIntentRecord{
		ID:         "intent-partial-to-zero",
		Status:     tradeIntentStatusPending,
		Action:     intentActionSell,
		Amount:     ts.individualBuyAmount,
		Price:      decimal.NewFromInt(55000),
		Time:       time.Now(),
		IsFullSell: false, // intentionally not marked as full sell
	}
	ts.journal.intents = append(ts.journal.intents, intent)
	ts.journal.index[intent.ID] = intent

	mockTrader.On("OrderExecuted", mock.Anything, intent.ID).Return(true, intent.Amount, nil)

	err = ts.reconcileTradeIntents(context.Background())
	require.NoError(t, err)

	require.Equal(t, tradeIntentStatusDone, ts.journal.index[intent.ID].Status, "intent should be done")
	require.Len(t, ts.dcaSeries.Purchases, 0, "series should reset when partial sell brings balance to zero")
	require.True(t, ts.dcaSeries.WaitingForDip, "strategy should wait for dip after selling everything")
	require.True(t, ts.dcaSeries.TotalAmount.IsZero(), "total amount should be zero")
	require.True(t, ts.dcaSeries.LastSellPrice.Equal(intent.Price), "last sell price should be set")
}
