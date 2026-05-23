package averaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/pkg/statepath"
)

const (
	seriesKeyPrefix           = "dca_series_"
	percentageMultiplier      = 100
	defaultOrderCheckInterval = 1 * time.Minute
	walSegmentThreshold       = 1000
	walMaxSegments            = 100
	walDirPermissions         = 0o755
)

// Tradersvc is the trade-execution service used by the strategy.
type Tradersvc interface {
	// ExecuteAction executes a trade action.
	// For ActionOpenLong (buy): amount is in BASE currency (e.g., BTC to buy).
	// For ActionCloseLong (sell): amount is in BASE currency (e.g., BTC to sell).
	ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error
	// OrderExecuted checks if an order is fully executed.
	// Returns filledAmount in BASE currency (e.g., how many BTC were bought/sold).
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	// OrderExists reports whether an order with clientOrderID exists on the exchange
	// (open, partially filled, filled, or cancelled). Returns false only when the
	// exchange definitively has no record of the order.
	OrderExists(ctx context.Context, clientOrderID string) (bool, error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error)
}

// Pricer fetches current market prices.
type Pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// DecisionRecorder persists trading decisions.
type DecisionRecorder interface {
	SaveAveraging(event entity.AveragingDecisionEvent) error
}

// Strategy executes position-averaging trades (DCA, Martingale, etc.).
//
// High-level flow:
//  1. Initialize opens the first buy and recovers any interrupted intents from WAL.
//  2. Trade is called on each tick: it reconciles pending intents, then either
//     buys (price dipped), sells (price recovered), or does nothing.
//  3. After a full sell the cycle resets and Strategy waits for a configurable
//     dip below the last sell price before starting a new cycle.
//
// All state mutations are written to a WAL so the strategy can resume after a
// process restart exactly where it left off.
type Strategy struct {
	// pair is the trading pair this strategy operates on (e.g. BTC/USDT).
	pair entity.Pair

	// amountPercent is the fraction of the available quote balance (1–100) that
	// is allocated to each full cycle.  Allocated once at cycle start and stored
	// in cycle.AllocatedQuoteAmount for the lifetime of the cycle.
	amountPercent decimal.Decimal

	// tradePart tracks the 1-indexed sequence number of the next buy step within
	// the current cycle.  It is kept in sync with len(cycle.Purchases) so that
	// decision events carry the correct step number.
	tradePart decimal.Decimal

	pricer    Pricer
	trader    Tradersvc
	// recorder persists AveragingDecisionEvents after every executed buy or sell.
	// Used for analytics and audit; failures are logged but do not abort the trade.
	recorder  DecisionRecorder
	l         *zap.Logger

	// wal is the write-ahead log used to persist both the cycle state and every
	// trade intent so neither is lost on restart.
	wal *gowal.Wal

	// journal tracks every trade intent (buy/sell order submitted to the
	// exchange) and its current status (pending/done/failed).  On startup
	// reconcileTradeIntents replays any pending intents to ensure fills that
	// arrived while the process was down are applied to the cycle.
	journal *tradeJournal

	// cycle is the live state of the current averaging cycle: the ordered list of
	// purchases made so far, the weighted-average entry price, the total invested
	// amount, and metadata needed to decide the next buy/sell step.  A new cycle
	// begins after each full sell and ends when all positions are closed.  The
	// cycle is persisted to WAL after every mutation so it can be recovered on
	// restart without losing trade history.
	cycle *entity.AveragingSeries

	// thresholds holds the percentage drop required to trigger a buy, the
	// percentage rise required to trigger a sell, and the maximum number of buy
	// steps allowed per cycle.
	thresholds *entity.AveragingThresholds

	// seriesKey is the WAL key under which the cycle snapshot is stored.
	// Format: "dca_series_<sanitised-stateKey>".
	seriesKey string

	// orderCheckInterval is the polling period used in reconcileTradeIntents
	// when waiting for an exchange order to fill.  Defaults to 1 minute;
	// overridden to a short value in tests.
	orderCheckInterval time.Duration

	// positionSizer determines how much quote currency to spend on each buy step.
	// EqualSizer distributes evenly (DCA); MartingaleSizer doubles each step.
	positionSizer PositionSizer

	// strategyName labels decision events emitted to the recorder ("dca",
	// "martingale").  Has no effect on trade logic.
	strategyName string
}

// NewStrategy returns a configured averaging strategy ready to call Initialize.
func NewStrategy(
	l *zap.Logger,
	stateKey string,
	pair entity.Pair,
	amountPercent decimal.Decimal,
	pricer Pricer,
	trader Tradersvc,
	recorder DecisionRecorder,
	maxTrades int,
	percentThresholdBuy decimal.Decimal,
	percentThresholdSell decimal.Decimal,
	sizer PositionSizer,
	strategyName string,
) (*Strategy, error) {
	if amountPercent.LessThan(decimal.NewFromInt(1)) || amountPercent.GreaterThan(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("amountPercent must be between 1 and 100, got %s", amountPercent.String())
	}

	if strings.TrimSpace(stateKey) == "" {
		stateKey = strings.ToLower(pair.String())
	}

	seriesKey := fmt.Sprintf("%s%s", seriesKeyPrefix, stateKey)

	wal, err := createWAL(stateKey)
	if err != nil {
		return nil, err
	}

	// try to recover DCA cycle from WAL
	cycle := entity.NewAveragingSeries()

	tradeIntents := make([]*tradeIntentRecord, 0)

	for record := range wal.Iterator() {
		if record.Key == seriesKey {
			if unmarshalErr := json.Unmarshal(record.Value, cycle); unmarshalErr != nil {
				l.Error("failed to unmarshal DCA series", zap.Error(unmarshalErr))

				continue
			}

			// ensure ProcessedTradeIDs is not nil after unmarshaling old WAL records.
			if cycle.ProcessedTradeIDs == nil {
				cycle.ProcessedTradeIDs = make(map[string]bool)
			}

			continue
		}

		if strings.HasPrefix(record.Key, tradeIntentKeyPrefix) {
			var intent tradeIntentRecord
			if unmarshalErr := json.Unmarshal(record.Value, &intent); unmarshalErr != nil {
				l.Error("failed to unmarshal trade intent", zap.Error(unmarshalErr), zap.String("key", record.Key))

				continue
			}

			intentCopy := intent

			tradeIntents = append(tradeIntents, &intentCopy)
		}
	}

	initialTradePart := decimal.NewFromInt(int64(len(cycle.Purchases)))

	thresholds, err := entity.NewAveragingThresholds(percentThresholdBuy, percentThresholdSell, maxTrades)
	if err != nil {
		return nil, fmt.Errorf("invalid thresholds: %w", err)
	}

	return &Strategy{
		pair:               pair,
		amountPercent:      amountPercent,
		tradePart:          initialTradePart,
		pricer:             pricer,
		trader:             trader,
		recorder:           recorder,
		l:                  l,
		wal:                wal,
		journal:            newTradeJournal(wal, tradeIntents),
		cycle:              cycle,
		thresholds:         &thresholds,
		orderCheckInterval: defaultOrderCheckInterval,
		seriesKey:          seriesKey,
		positionSizer:      sizer,
		strategyName:       strategyName,
	}, nil
}

// Initialize prepares the strategy for trading.  It must be called once before
// the first Trade call.
//
// Steps:
//  1. Reconciles any trade intents that were pending when the process last
//     stopped (fills that arrived while the bot was down are applied to the
//     cycle now).
//  2. Fetches the current price to validate connectivity and calculate the
//     initial buy amount.
//  3. If no cycle exists (empty WAL or fresh start) executes the first buy
//     immediately.  If a cycle was recovered from WAL, or the strategy is
//     waiting for a dip after a previous sell, the initial buy is skipped.
//
// Returns an error if the exchange is unreachable, the price is invalid, or
// the calculated buy amount would be zero (which usually indicates that
// amountPercent or the current balance is too small).
func (d *Strategy) Initialize(ctx context.Context) error {
	// reconcile any pending trade intents from WAL.
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return err
	}

	d.logBalances(ctx, "Starting bot", zap.String("pair", d.pair.String()))

	currentPrice, err := d.getValidatedPrice(ctx)
	if err != nil {
		d.l.Error("Failed to get current price for initialization", zap.Error(err))

		return errors.Wrapf(err, "failed to get current price for %s", d.pair.String())
	}

	// validate that we can calculate initial buy amount.
	initialBuyQuoteAmount, err := d.calculateBuyQuoteAmount(ctx)
	if err != nil {
		d.l.Error("Failed to calculate initial buy amount", zap.Error(err))

		return errors.Wrap(err, "failed to calculate initial buy amount")
	}

	if initialBuyQuoteAmount.IsZero() {
		d.l.Error("Calculated initial buy amount is zero",
			zap.String("amountPercent", d.amountPercent.String()))

		return fmt.Errorf("calculated initial buy amount is zero, check AmountPercent (%s%%) and current balance", d.amountPercent.String())
	}

	// set initial reference price if not already set.
	if d.cycle.LastSellPrice.IsZero() {
		d.updateSellState(currentPrice, d.cycle.WaitingForDip)
	}

	// check if we need to execute initial buy.
	if len(d.cycle.Purchases) > 0 || d.cycle.WaitingForDip {
		if d.cycle.WaitingForDip {
			d.l.Info("Waiting for dip before starting new series.",
				zap.String("lastSellPrice", d.cycle.LastSellPrice.String()))
		} else {
			d.l.Info("Series already exists (loaded from WAL). Continuing with existing trades.",
				zap.Int("existingPurchases", len(d.cycle.Purchases)))
		}

		return nil
	}

	// execute initial buy.
	initialBuyBaseAmount := initialBuyQuoteAmount.Div(currentPrice).RoundFloor(8)
	if initialBuyBaseAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("calculated initial buy amount in base is zero, check AmountPercent (%s%%), price, and balance", d.amountPercent.String())
	}

	d.l.Info("No existing series. Executing initial buy.",
		zap.String("currentPrice", currentPrice.String()),
		zap.String("amount_quote", initialBuyQuoteAmount.String()),
		zap.String("amount_base", initialBuyBaseAmount.String()))

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionBuy, currentPrice, initialBuyQuoteAmount, initialBuyBaseAmount, operationTime, 1, false)
	if err != nil {
		return err
	}

	if err := d.trader.ExecuteAction(ctx, entity.ActionOpenLong, initialBuyBaseAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		d.l.Error("Initial buy execution failed", zap.Error(err))

		return errors.Wrapf(err, "initial buy execution failed for %s", d.pair.String())
	}

	if err := d.journal.MarkSubmitted(intent); err != nil {
		return errors.Wrap(err, "failed to mark initial buy intent as submitted")
	}

	if err := d.AddPurchase(intent.ID, currentPrice, initialBuyQuoteAmount, operationTime, 1); err != nil {
		d.l.Error("Failed to record initial purchase state", zap.Error(err))

		return errors.Wrapf(err, "failed to record initial purchase state for %s", d.pair.String())
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return err
	}

	d.l.Info("Initial buy executed successfully.",
		zap.String("amount_quote", initialBuyQuoteAmount.String()),
		zap.String("amount_base", initialBuyBaseAmount.String()))

	if err := d.recorder.SaveAveraging(entity.AveragingDecisionEvent{
		Timestamp:         operationTime,
		Pair:              d.pair.String(),
		Strategy:          d.strategyName,
		Action:            "buy",
		CurrentPrice:      currentPrice,
		AverageEntryPrice: d.cycle.AvgEntryPrice,
		TradePart:         1,
		QuoteBalance:      initialBuyQuoteAmount,
	}); err != nil {
		d.l.Error("failed to save averaging decision", zap.Error(err))
	}

	return nil
}

// Trade performs one evaluation tick: evaluates market conditions and executes trades if needed. It should be called repeatedly on a
// timer (e.g. every second or every minute).
//
// Each call:
//  1. Reconciles any pending trade intents (handles fills that arrived between
//     ticks).
//  2. Fetches the current price.
//  3. If WaitingForDip: delegates to handleWaitingForDip, which starts a new
//     cycle when the price drops far enough below the last sell price.
//  4. Otherwise evaluates buy/sell conditions against the active cycle and
//     executes the appropriate action.
//
// Returns a TradeEvent describing the executed trade, or nil if no action was
// taken this tick.
func (d *Strategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return nil, err
	}

	price, err := d.getValidatedPrice(ctx)
	if err != nil {
		return nil, err
	}

	// check if we're waiting for a dip after a sell.
	if d.cycle.WaitingForDip && !d.cycle.LastSellPrice.IsZero() {
		return d.handleWaitingForDip(ctx, price)
	}

	if d.cycle.IsEmpty() {
		return nil, nil
	}

	buy := d.cycle.ShouldBuyAtPrice(price, *d.thresholds)
	if buy.ShouldBuy {
		d.l.Info("Buy decision", zap.String("reason", buy.Reason))

		return d.executeBuy(ctx, price)
	}

	sell := d.cycle.ShouldTakeProfitAtPrice(price, *d.thresholds)
	if sell.ShouldSell {
		d.l.Info("Sell decision",
			zap.String("reason", sell.Reason),
			zap.Bool("full_sell", sell.IsFullSell))

		return d.executeSell(ctx, price, sell)
	}

	return nil, nil
}

func (d *Strategy) Close() error {
	if err := d.wal.Close(); err != nil {
		return errors.Wrap(err, "failed to close WAL")
	}

	return nil
}

// SellAll force-closes the entire open position at the current market price.
//
// The actual amount sold is taken from the exchange position, not from the WAL
// cycle, so it stays correct even if the two have drifted.  If the exchange
// reports zero position but the WAL still has a non-empty cycle (e.g. the
// position was closed manually on the exchange, liquidated, or a crash occurred
// after the exchange executed the sell but before WAL was updated),
// the cycle is reset and the WAL is written without executing a trade.
func (d *Strategy) SellAll(ctx context.Context) error {
	price, err := d.getValidatedPrice(ctx)
	if err != nil {
		return err
	}
	
	pos, err := d.trader.GetPosition(ctx, d.pair)
	if err != nil {
		return err
	}

	realAmount := decimal.Zero
	if pos != nil {
		realAmount = pos.Amount.Abs()
	}

	if d.cycle.TotalBaseAmount().IsZero() && realAmount.IsZero() {
		return errors.New("no position to sell")
	}

	if realAmount.IsZero() {
		d.l.Warn("SellAll found no position on exchange, but WAL had coins or was out of sync. Fixing state.")
		d.resetSeries(price)

		return d.saveSeries()
	}

	_, err = d.executeSell(ctx, price, entity.SellDecision{
		Reason:     "SellAll requested",
		Amount:     realAmount,
		IsFullSell: true,
	})

	return err
}

// GetCostBasis returns the weighted-average entry price and the total base
// currency amount currently held, suitable for PnL calculations by external
// callers.  Returns zeros when no cycle is active.
func (d *Strategy) GetCostBasis() (entryPrice, amount decimal.Decimal) {
	if d.cycle == nil {
		return decimal.Zero, decimal.Zero
	}

	return d.cycle.AvgEntryPrice, d.cycle.TotalBaseAmount()
}

// AddPurchase appends a confirmed buy fill to the active cycle and persists the
// updated cycle snapshot to WAL.
//
// intentID is used for deduplication: if the same ID has already been applied
// (e.g. the fill arrived twice during reconciliation) the call is a no-op.
// Pass an empty string to skip dedup (used in tests and manual adjustments).
//
// tradePartValue sets the step index explicitly; if < 1 it defaults to
// len(cycle.Purchases)+1.
func (d *Strategy) AddPurchase(intentID string, price, amountQuote decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	if intentID != "" && d.isTradeProcessed(intentID) {
		return nil
	}

	partNumber := tradePartValue
	if partNumber < 1 {
		partNumber = len(d.cycle.Purchases) + 1
	}

	if err := d.cycle.AddPurchase(intentID, price, amountQuote, purchaseTime, partNumber); err != nil {
		return errors.Wrap(err, "failed to add purchase to series")
	}

	d.tradePart = decimal.NewFromInt(int64(len(d.cycle.Purchases)))
	d.markTradeProcessed(intentID)

	return d.saveSeries()
}

func (d *Strategy) getValidatedPrice(ctx context.Context) (decimal.Decimal, error) {
	price, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		return decimal.Zero, errors.Wrapf(err, "pricer failed for pair %s", d.pair.String())
	}

	if err := d.validatePrice(price, "price fetch"); err != nil {
		return decimal.Zero, err
	}

	return price, nil
}

func (d *Strategy) validatePrice(price decimal.Decimal, operation string) error {
	if price.LessThanOrEqual(decimal.Zero) {
		return errors.Errorf("invalid price %s for %s (%s)", price.String(), operation, d.pair.String())
	}

	return nil
}

// handleWaitingForDip is called on every tick while cycle.WaitingForDip is
// true.  It compares the current price against cycle.LastSellPrice:
//   - If the price has NOT dropped by at least BuyThresholdPercent, returns nil
//     (stay idle).
//   - If the dip threshold is reached, clears WaitingForDip and executes the
//     first buy of a new cycle.  On buy failure, WaitingForDip is restored so
//     the strategy retries on the next tick.
func (d *Strategy) handleWaitingForDip(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	percentChange := entity.PercentageDiff(price, d.cycle.LastSellPrice)
	if percentChange.GreaterThan(d.thresholds.BuyThresholdPercent.Neg()) {
		return nil, nil
	}

	d.l.Info("Price dropped from last sell price, initiating new DCA series.",
		zap.String("currentPrice", price.String()),
		zap.String("lastSellPrice", d.cycle.LastSellPrice.String()),
		zap.String("percentChange", percentChange.String()))

	wasWaitingForDip := d.cycle.WaitingForDip
	d.updateSellState(d.cycle.LastSellPrice, false)

	tradeEvent, err := d.executeBuy(ctx, price)
	if err != nil {
		d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
		d.updateSellState(d.cycle.LastSellPrice, wasWaitingForDip)

		return tradeEvent, err
	}

	d.l.Info("Initial purchase recorded successfully",
		zap.String("price", price.String()),
		zap.String("amount_base", tradeEvent.Amount.String()))

	return tradeEvent, nil
}

// executeBuy calculates the step amount, persists a pending intent, submits
// the buy order to the exchange, then records the fill in the cycle.
//
// Order of operations ensures crash-safety: the intent is written to WAL
// before the order is sent, so if the process dies after the exchange accepts
// the order but before the cycle is updated, reconcileTradeIntents will pick
// up the pending intent on the next start and apply the fill.
func (d *Strategy) executeBuy(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	if err := d.validatePrice(price, "buy"); err != nil {
		return nil, err
	}

	buyQuoteAmount, err := d.calculateBuyQuoteAmount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to calculate buy amount")
	}

	buyBaseAmount := buyQuoteAmount.Div(price).RoundFloor(8)
	if buyBaseAmount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("calculated buy amount in base is zero, check AmountPercent (%s%%), price, and balance", d.amountPercent.String())
	}

	d.l.Info("Price significantly lower than average, attempting DCA buy.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.cycle.AvgEntryPrice.String()))

	operationTime := time.Now()
	tradePartValue := int(d.tradePart.IntPart()) + 1

	intent, err := d.journal.Prepare(intentActionBuy, price, buyQuoteAmount, buyBaseAmount, operationTime, tradePartValue, false)
	if err != nil {
		return nil, err
	}

	if err := d.trader.ExecuteAction(ctx, entity.ActionOpenLong, buyBaseAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)

		return nil, errors.Wrapf(err, "trader buy failed for pair %s with base amount %s", d.pair.String(), buyBaseAmount.String())
	}

	if err := d.journal.MarkSubmitted(intent); err != nil {
		return nil, errors.Wrap(err, "failed to mark buy intent as submitted")
	}

	if err := d.AddPurchase(intent.ID, price, buyQuoteAmount, operationTime, tradePartValue); err != nil {
		d.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("intent_id", intent.ID))

		return &entity.TradeEvent{
			Action: entity.ActionOpenLong,
			Amount: buyBaseAmount,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	d.logBalances(ctx, "DCA buy executed",
		zap.Int("trade_part", int(d.tradePart.IntPart())),
		zap.String("price", price.String()),
		zap.String("amount_quote", buyQuoteAmount.String()),
		zap.String("amount_base", buyBaseAmount.String()),
		zap.String("avg_entry_price", d.cycle.AvgEntryPrice.String()))

	if err := d.recorder.SaveAveraging(entity.AveragingDecisionEvent{
		Timestamp:         operationTime,
		Pair:              d.pair.String(),
		Strategy:          d.strategyName,
		Action:            "buy",
		CurrentPrice:      price,
		AverageEntryPrice: d.cycle.AvgEntryPrice,
		TradePart:         tradePartValue,
		QuoteBalance:      buyQuoteAmount,
	}); err != nil {
		d.l.Error("failed to save averaging decision", zap.Error(err))
	}

	return &entity.TradeEvent{
		Action: entity.ActionOpenLong,
		Amount: buyBaseAmount,
		Pair:   d.pair,
		Price:  price,
	}, nil
}

// executeSell submits a sell order for the amount described by sell, then
// delegates cycle cleanup to applyExecutedSell.
//
// For a partial sell, only the step amount is sold and the cycle continues.
// For a full sell (sell.IsFullSell == true), the cycle is reset via
// resetSeries and the strategy enters WaitingForDip.
//
// Same WAL-first crash-safety pattern as executeBuy: the intent is persisted
// before the order is sent to the exchange.
func (d *Strategy) executeSell(ctx context.Context, price decimal.Decimal, sell entity.SellDecision) (*entity.TradeEvent, error) {
	d.l.Info("Price significantly higher than average, attempting sell.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.cycle.AvgEntryPrice.String()))

	amountBaseCurrency := sell.Amount
	amountQuoteProceeds := amountBaseCurrency.Mul(price)
	tradePartValue := d.cycle.CurrentSellTradePart()

	amountQuoteCost := amountBaseCurrency.Mul(d.cycle.AvgEntryPrice).Round(8)
	if sell.IsFullSell {
		amountQuoteCost = d.cycle.TotalAmount
	}

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionSell, price, amountQuoteCost, amountBaseCurrency, operationTime, tradePartValue, sell.IsFullSell)
	if err != nil {
		return nil, err
	}

	// sell uses base currency amount (e.g., BTC)
	if err := d.trader.ExecuteAction(ctx, entity.ActionCloseLong, amountBaseCurrency, intent.ID); err != nil {
		d.markIntentFailed(intent, err)

		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", d.pair.String(), amountBaseCurrency.String())
	}

	if err := d.journal.MarkSubmitted(intent); err != nil {
		return nil, errors.Wrap(err, "failed to mark sell intent as submitted")
	}

	if err := d.applyExecutedSell(intent); err != nil {
		d.l.Error("failed to apply sell intent to series",
			zap.Error(err),
			zap.String("intent_id", intent.ID))

		return &entity.TradeEvent{
			Action: entity.ActionCloseLong,
			Amount: amountBaseCurrency,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionCloseLong,
		Amount: amountBaseCurrency,
		Pair:   d.pair,
		Price:  price,
	}

	profit := entity.PercentageDiff(price, d.cycle.AvgEntryPrice)
	d.logBalances(ctx, "sell executed",
		zap.Int("trade_part", tradePartValue),
		zap.String("price", price.String()),
		zap.String("amountBase", amountBaseCurrency.String()),
		zap.String("amountQuoteProceeds", amountQuoteProceeds.String()),
		zap.String("amountQuoteCost", amountQuoteCost.String()),
		zap.String("profit_percent", profit.String()))

	if err := d.recorder.SaveAveraging(entity.AveragingDecisionEvent{
		Timestamp:         operationTime,
		Pair:              d.pair.String(),
		Strategy:          d.strategyName,
		Action:            "sell",
		CurrentPrice:      price,
		AverageEntryPrice: d.cycle.AvgEntryPrice,
		TradePart:         tradePartValue,
		QuoteBalance:      amountQuoteProceeds,
	}); err != nil {
		d.l.Error("failed to save averaging decision", zap.Error(err))
	}

	return tradeEvent, nil
}

func (d *Strategy) logBalances(ctx context.Context, action string, extraFields ...zap.Field) {
	baseBalance, _ := d.trader.GetBalance(ctx, d.pair.From)
	quoteBalance, _ := d.trader.GetBalance(ctx, d.pair.To)

	extraFields = append(extraFields,
		zap.String(d.pair.From+"_balance", baseBalance.String()),
		zap.String(d.pair.To+"_balance", quoteBalance.String()))

	d.l.Info(action, extraFields...)
}

// updateSellState atomically sets LastSellPrice and WaitingForDip on the
// active cycle.  Called both after a completed sell and on rollback when a
// subsequent buy fails during WaitingForDip handling.
func (d *Strategy) updateSellState(price decimal.Decimal, waitingForDip bool) {
	d.cycle.LastSellPrice = price
	d.cycle.WaitingForDip = waitingForDip
}

// calculateBuyQuoteAmount returns the quote-currency amount to spend on the
// current buy step.
//
// On the first buy of a cycle, the full balance allocation is calculated
// (balance × amountPercent / 100) and stored in cycle.AllocatedQuoteAmount.
// All subsequent steps within the same cycle reuse that stored value so the
// step size is stable even if the balance changes between steps.
//
// The per-step amount is derived by passing the allocated total through
// positionSizer, which applies the DCA or Martingale distribution logic.
func (d *Strategy) calculateBuyQuoteAmount(ctx context.Context) (decimal.Decimal, error) {
	// if ongoing series, use stored allocated balance
	if d.cycle.AllocatedQuoteAmount.GreaterThan(decimal.Zero) {
		return d.positionSizer(d.cycle.AllocatedQuoteAmount, d.thresholds.MaxTrades, len(d.cycle.Purchases)), nil
	}

	// new series: fetch current balance
	quoteBalance, err := d.trader.GetBalance(ctx, d.pair.To)
	if err != nil {
		return decimal.Decimal{}, errors.Wrapf(err, "failed to get %s balance", d.pair.To)
	}

	// calculate allocated amount for the entire series
	// allocated = total balance * amount percent / 100
	allocatedAmount := quoteBalance.Mul(d.amountPercent).Div(decimal.NewFromInt(percentageMultiplier))

	// store it for subsequent steps in this series
	d.cycle.AllocatedQuoteAmount = allocatedAmount

	// calculate individual trade amount using the position sizer
	individualQuoteAmount := d.positionSizer(allocatedAmount, d.thresholds.MaxTrades, len(d.cycle.Purchases))

	return individualQuoteAmount, nil
}

// saveSeries serialises the current cycle snapshot to JSON and appends it to
// the WAL under seriesKey.  Called after every mutation that changes cycle
// state (buy fill, sell fill, dip-wait transition).  On process restart the
// last WAL record for seriesKey is replayed by NewStrategy to restore the
// cycle exactly.
func (d *Strategy) saveSeries() error {
	data, err := json.Marshal(d.cycle)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	nextIndex := d.wal.CurrentIndex() + 1
	if err := d.wal.Write(gowal.Record{Index: nextIndex, Key: d.seriesKey, Value: data}); err != nil {
		return errors.Wrap(err, "failed to write DCA series")
	}

	return nil
}

func createWAL(stateKey string) (*gowal.Wal, error) {
	walDir, err := statepath.WALDir(sanitizeStateKey(stateKey))
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve WAL directory")
	}

	if err := os.MkdirAll(walDir, walDirPermissions); err != nil {
		return nil, errors.Wrapf(err, "failed to ensure WAL directory %s", walDir)
	}

	walCfg := gowal.Config{
		Dir:              walDir,
		Prefix:           "log_",
		SegmentThreshold: walSegmentThreshold,
		MaxSegments:      walMaxSegments,
		IsInSyncDiskMode: true,
	}

	w, err := gowal.NewWAL(walCfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create WAL")
	}

	return w, nil
}

func sanitizeStateKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "default"
	}

	var b strings.Builder
	prevSeparator := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSeparator = false
			continue
		}
		if !prevSeparator {
			b.WriteByte('_')
			prevSeparator = true
		}
	}

	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "default"
	}

	return result
}

// isTradeProcessed reports whether the given intent ID has already been applied
// to the cycle.  Used to prevent double-counting fills during reconciliation.
func (d *Strategy) isTradeProcessed(intentID string) bool {
	if intentID == "" {
		return false
	}

	if len(d.cycle.ProcessedTradeIDs) == 0 {
		return false
	}

	return d.cycle.ProcessedTradeIDs[intentID]
}

// markTradeProcessed records intentID in the cycle's dedup set and is called
// immediately after a fill is applied, before saveSeries, so the dedup set is
// part of the persisted snapshot.
func (d *Strategy) markTradeProcessed(intentID string) {
	if intentID == "" {
		return
	}

	d.cycle.ProcessedTradeIDs[intentID] = true
}

// markIntentFailed persists the intent with status "failed" and logs the error.
// Called when ExecuteAction returns an error so the failed order is visible in
// the WAL and won't be mistaken for a pending intent on the next start.
func (d *Strategy) markIntentFailed(intent *tradeIntentRecord, cause error) {
	if d.journal == nil || intent == nil {
		return
	}

	if err := d.journal.MarkFailed(intent, cause); err != nil {
		d.l.Error("failed to persist failed trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
	}
}
