// Package dca implements Dollar-Cost Averaging trading strategy (core logic here; reconciliation in dca_reconciliation.go).
package dca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
	entity "github.com/vadiminshakov/marti/internal/domain"
	"go.uber.org/zap"
)

const (
	dcaSeriesKeyPrefix        = "dca_series_"
	percentageMultiplier      = 100
	defaultOrderCheckInterval = 1 * time.Minute
	walSegmentThreshold       = 1000
	walMaxSegments            = 100
	walDirPermissions         = 0o755
)

type tradersvc interface {
	// ExecuteAction executes a trade action.
	// For ActionOpenLong (buy): amount is in QUOTE currency (e.g., USDT to spend).
	// For ActionCloseLong (sell): amount is in BASE currency (e.g., BTC to sell).
	ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error
	// OrderExecuted checks if an order is fully executed.
	// Returns filledAmount in BASE currency (e.g., how many BTC were bought/sold).
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// DCAStrategy executes DCA trades.
type DCAStrategy struct {
	pair              entity.Pair
	amountPercent     decimal.Decimal
	tradePart         decimal.Decimal
	pricer            pricer
	trader            tradersvc
	l                 *zap.Logger
	wal               *gowal.Wal
	journal           *tradeJournal
	dcaSeries         *entity.DCASeries
	thresholds        entity.DCAThresholds
	seriesKey         string

	// interval for checking order status (can be overridden for testing)
	orderCheckInterval time.Duration
}

// createWAL initializes WAL for the given pair.
func createWAL(pair entity.Pair) (*gowal.Wal, error) {
	walDir := filepath.Join("./wal", pair.String())
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

	return gowal.NewWAL(walCfg)
}

// NewDCAStrategy returns a configured DCA strategy.
func NewDCAStrategy(l *zap.Logger, pair entity.Pair, amountPercent decimal.Decimal, pricer pricer, trader tradersvc,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*DCAStrategy, error) {

	if amountPercent.LessThan(decimal.NewFromInt(1)) || amountPercent.GreaterThan(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("amountPercent must be between 1 and 100, got %s", amountPercent.String())
	}

	seriesKey := fmt.Sprintf("%s%s", dcaSeriesKeyPrefix, pair.String())

	wal, err := createWAL(pair)
	if err != nil {
		return nil, err
	}

	// try to recover DCA series from WAL
	dcaSeries := entity.NewDCASeries()

	tradeIntents := make([]*tradeIntentRecord, 0)

	for msg := range wal.Iterator() {
		if msg.Key == seriesKey {
			if err := json.Unmarshal(msg.Value, dcaSeries); err != nil {
				l.Error("failed to unmarshal DCA series", zap.Error(err))
				continue
			}
			// ensure ProcessedTradeIDs is not nil after unmarshaling old WAL records.
			if dcaSeries.ProcessedTradeIDs == nil {
				dcaSeries.ProcessedTradeIDs = make(map[string]bool)
			}
			continue
		}

		if strings.HasPrefix(msg.Key, tradeIntentKeyPrefix) {
			var intent tradeIntentRecord
			if err := json.Unmarshal(msg.Value, &intent); err != nil {
				l.Error("failed to unmarshal trade intent", zap.Error(err), zap.String("key", msg.Key))
				continue
			}
			intentCopy := intent
			tradeIntents = append(tradeIntents, &intentCopy)
		}
	}

	initialTradePart := decimal.NewFromInt(int64(len(dcaSeries.Purchases)))

	thresholds, err := entity.NewDCAThresholds(dcaPercentThresholdBuy, dcaPercentThresholdSell, maxDcaTrades)
	if err != nil {
		return nil, fmt.Errorf("invalid thresholds: %w", err)
	}

	return &DCAStrategy{
		pair:               pair,
		amountPercent:      amountPercent,
		tradePart:          initialTradePart,
		pricer:             pricer,
		trader:             trader,
		l:                  l,
		wal:                wal,
		journal:            newTradeJournal(wal, tradeIntents),
		dcaSeries:          dcaSeries,
		thresholds:         thresholds,
		seriesKey:          seriesKey,
		orderCheckInterval: defaultOrderCheckInterval,
	}, nil
}

// saveDCASeries persists series state.
func (d *DCAStrategy) saveDCASeries() error {
	data, err := json.Marshal(d.dcaSeries)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	nextIndex := d.wal.CurrentIndex() + 1
	return d.wal.Write(nextIndex, d.seriesKey, data)
}

// calculateIndividualBuyAmount returns quote amount.
func (d *DCAStrategy) calculateIndividualBuyAmount(ctx context.Context) (decimal.Decimal, error) {
	quoteBalance, err := d.trader.GetBalance(ctx, d.pair.To)
	if err != nil {
		return decimal.Decimal{}, errors.Wrapf(err, "failed to get %s balance", d.pair.To)
	}
	individualAmount := quoteBalance.Mul(d.amountPercent).Div(decimal.NewFromInt(percentageMultiplier))

	return individualAmount, nil
}

func (d *DCAStrategy) isTradeProcessed(intentID string) bool {
	if intentID == "" {
		return false
	}
	if len(d.dcaSeries.ProcessedTradeIDs) == 0 {
		return false
	}

	return d.dcaSeries.ProcessedTradeIDs[intentID]
}

func (d *DCAStrategy) markTradeProcessed(intentID string) {
	if intentID == "" {
		return
	}
	d.dcaSeries.ProcessedTradeIDs[intentID] = true
}

// AddDCAPurchase records a DCA purchase.
func (d *DCAStrategy) AddDCAPurchase(intentID string, price, amount decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	if intentID != "" && d.isTradeProcessed(intentID) {
		return nil
	}

	partNumber := tradePartValue
	if partNumber < 1 {
		partNumber = len(d.dcaSeries.Purchases) + 1
	}

	if err := d.dcaSeries.AddPurchase(intentID, price, amount, purchaseTime, partNumber); err != nil {
		return errors.Wrap(err, "failed to add purchase to series")
	}

	d.tradePart = decimal.NewFromInt(int64(len(d.dcaSeries.Purchases)))
	d.markTradeProcessed(intentID)

	return d.saveDCASeries()
}

// GetDCASeries exposes current DCA series.
func (d *DCAStrategy) GetDCASeries() *entity.DCASeries {
	return d.dcaSeries
}

func (d *DCAStrategy) markIntentFailed(intent *tradeIntentRecord, cause error) {
	if d.journal == nil || intent == nil {
		return
	}
	if err := d.journal.MarkFailed(intent, cause); err != nil {
		d.l.Error("failed to persist failed trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
	}
}

// Trade performs one DCA evaluation.
func (d *DCAStrategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	price, err := d.getValidatedPrice(ctx)
	if err != nil {
		return nil, err
	}

	// check if we're waiting for a dip after a sell.
	if d.dcaSeries.WaitingForDip && !d.dcaSeries.LastSellPrice.IsZero() {
		return d.handleWaitingForDip(ctx, price)
	}

	if d.dcaSeries.IsEmpty() {
		return nil, nil
	}

	buy := d.dcaSeries.ShouldBuyAtPrice(price, d.thresholds)
	if buy.ShouldBuy {
		d.l.Info("Buy decision", zap.String("reason", buy.Reason))
		return d.executeBuy(ctx, price)
	}

	sell := d.dcaSeries.ShouldTakeProfitAtPrice(price, d.thresholds)
	if sell.ShouldSell {
		d.l.Info("Sell decision",
			zap.String("reason", sell.Reason),
			zap.Bool("full_sell", sell.IsFullSell))
		return d.executeSell(ctx, price, sell)
	}

	return nil, nil
}

// getValidatedPrice fetches current price.
func (d *DCAStrategy) getValidatedPrice(ctx context.Context) (decimal.Decimal, error) {
	price, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		return decimal.Zero, errors.Wrapf(err, "pricer failed for pair %s", d.pair.String())
	}
	return price, nil
}

// handleWaitingForDip handles post-sell dip wait logic.
func (d *DCAStrategy) handleWaitingForDip(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	percentChange := entity.PercentageDiff(price, d.dcaSeries.LastSellPrice)
	if percentChange.GreaterThan(d.thresholds.BuyThresholdPercent.Neg()) {
		return nil, nil
	}

	d.l.Info("Price dropped from last sell price, initiating new DCA series.",
		zap.String("currentPrice", price.String()),
		zap.String("lastSellPrice", d.dcaSeries.LastSellPrice.String()),
		zap.String("percentChange", percentChange.String()))

	wasWaitingForDip := d.dcaSeries.WaitingForDip
	d.updateSellState(d.dcaSeries.LastSellPrice, false)

	tradeEvent, err := d.executeBuy(ctx, price)
	if err != nil {
		d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
		d.updateSellState(d.dcaSeries.LastSellPrice, wasWaitingForDip)
		return tradeEvent, err
	}

	d.l.Info("Initial purchase recorded successfully",
		zap.String("price", price.String()),
		zap.String("amount", tradeEvent.Amount.String()))
	return tradeEvent, nil
}

func (d *DCAStrategy) executeBuy(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	individualBuyAmount, err := d.calculateIndividualBuyAmount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to calculate buy amount")
	}

	d.l.Info("Price significantly lower than average, attempting DCA buy.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))

	operationTime := time.Now()
	tradePartValue := int(d.tradePart.IntPart()) + 1

	intent, err := d.journal.Prepare(intentActionBuy, price, individualBuyAmount, operationTime, tradePartValue, false)
	if err != nil {
		return nil, err
	}

	if err := d.trader.ExecuteAction(ctx, entity.ActionOpenLong, individualBuyAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", d.pair.String(), individualBuyAmount.String())
	}

	if err := d.AddDCAPurchase(intent.ID, price, individualBuyAmount, operationTime, tradePartValue); err != nil {
		d.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("intent_id", intent.ID))
		return &entity.TradeEvent{
			Action: entity.ActionOpenLong,
			Amount: individualBuyAmount,
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
		zap.String("amount", individualBuyAmount.String()),
		zap.String("avg_entry_price", d.dcaSeries.AvgEntryPrice.String()))

	return &entity.TradeEvent{
		Action: entity.ActionOpenLong,
		Amount: individualBuyAmount,
		Pair:   d.pair,
		Price:  price,
	}, nil
}

func (d *DCAStrategy) executeSell(ctx context.Context, price decimal.Decimal, sell entity.SellDecision) (*entity.TradeEvent, error) {
	d.l.Info("Price significantly higher than average, attempting sell.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))

	amountBaseCurrency := sell.Amount
	amountQuoteCurrency := amountBaseCurrency.Mul(price)

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionSell, price, amountQuoteCurrency, operationTime, 0, sell.IsFullSell)
	if err != nil {
		return nil, err
	}

	// sell uses base currency amount (e.g., BTC)
	if err := d.trader.ExecuteAction(ctx, entity.ActionCloseLong, amountBaseCurrency, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", d.pair.String(), amountBaseCurrency.String())
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

	profit := entity.PercentageDiff(price, d.dcaSeries.AvgEntryPrice)
	d.logBalances(ctx, "sell executed",
		zap.String("price", price.String()),
		zap.String("amountBase", amountBaseCurrency.String()),
		zap.String("amountQuote", amountQuoteCurrency.String()),
		zap.String("profit_percent", profit.String()))

	return tradeEvent, nil
}

func (d *DCAStrategy) Close() error {
	return d.wal.Close()
}

func (d *DCAStrategy) updateSellState(price decimal.Decimal, waitingForDip bool) {
	d.dcaSeries.LastSellPrice = price
	d.dcaSeries.WaitingForDip = waitingForDip
}

func (d *DCAStrategy) logBalances(ctx context.Context, action string, extraFields ...zap.Field) {
	baseBalance, _ := d.trader.GetBalance(ctx, d.pair.From)
	quoteBalance, _ := d.trader.GetBalance(ctx, d.pair.To)

	fields := append(extraFields,
		zap.String(d.pair.From+"_balance", baseBalance.String()),
		zap.String(d.pair.To+"_balance", quoteBalance.String()))

	d.l.Info(action, fields...)
}

// Initialize loads WAL and reconciles intents.
func (d *DCAStrategy) Initialize(ctx context.Context) error {
	// reconcile any pending trade intents from WAL.
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return err
	}

	d.logBalances(ctx, "Starting bot", zap.String("pair", d.pair.String()))

	currentPrice, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		d.l.Error("Failed to get current price for initialization", zap.Error(err))
		return errors.Wrapf(err, "failed to get current price for %s", d.pair.String())
	}

	// validate that we can calculate initial buy amount.
	calculatedInitialBuyAmount, err := d.calculateIndividualBuyAmount(ctx)
	if err != nil {
		d.l.Error("Failed to calculate initial buy amount", zap.Error(err))

		return errors.Wrap(err, "failed to calculate initial buy amount")
	}
	if calculatedInitialBuyAmount.IsZero() {
		d.l.Error("Calculated initial buy amount is zero",
			zap.String("amountPercent", d.amountPercent.String()))
		return fmt.Errorf("calculated initial buy amount is zero, check AmountPercent (%s%%) and current balance", d.amountPercent.String())
	}

	// set initial reference price if not already set.
	if d.dcaSeries.LastSellPrice.IsZero() {
		d.updateSellState(currentPrice, d.dcaSeries.WaitingForDip)
	}

	// check if we need to execute initial buy.
	if len(d.dcaSeries.Purchases) > 0 {
		d.l.Info("DCA series already exists (loaded from WAL). Continuing with existing trades.",
			zap.Int("existingPurchases", len(d.dcaSeries.Purchases)))
		return nil
	}

	// execute initial buy.
	d.l.Info("No existing DCA series. Executing initial buy.",
		zap.String("currentPrice", currentPrice.String()),
		zap.String("amount", calculatedInitialBuyAmount.String()))

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionBuy, currentPrice, calculatedInitialBuyAmount, operationTime, 1, false)
	if err != nil {
		return err
	}

	if err := d.trader.ExecuteAction(ctx, entity.ActionOpenLong, calculatedInitialBuyAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		d.l.Error("Initial buy execution failed", zap.Error(err))

		return errors.Wrapf(err, "initial buy execution failed for %s", d.pair.String())
	}

	if err := d.AddDCAPurchase(intent.ID, currentPrice, calculatedInitialBuyAmount, operationTime, 1); err != nil {
		d.l.Error("Failed to record initial purchase state", zap.Error(err))

		return errors.Wrapf(err, "failed to record initial purchase state for %s", d.pair.String())
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return err
	}

	d.l.Info("Initial buy executed successfully.",
		zap.String("amount", calculatedInitialBuyAmount.String()))

	return nil
}
