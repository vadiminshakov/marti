// Package dca implements DCA (Dollar-Cost Averaging) trading strategy.
//
// This file contains the core trading logic:
//   - DCA strategy implementation (Trade method)
//   - Buy/sell action execution
//   - Price decision chain (waiting for dip, DCA buy, profit taking)
//   - DCA series management (purchases tracking, statistics)
//
// For reconciliation/recovery logic after restarts, see dca_reconciliation.go
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
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

const (
	dcaSeriesKeyPrefix        = "dca_series_"
	percentageMultiplier      = 100
	defaultOrderCheckInterval = 1 * time.Minute
)

// DCAPurchase represents a single DCA purchase
type DCAPurchase struct {
	ID        string          `json:"id"`
	Price     decimal.Decimal `json:"price"`
	Amount    decimal.Decimal `json:"amount"`
	Time      time.Time       `json:"time"`
	TradePart int             `json:"trade_part"`
}

// DCASeries represents a complete DCA series
type DCASeries struct {
	Purchases         []DCAPurchase   `json:"purchases"`
	AvgEntryPrice     decimal.Decimal `json:"avg_entry_price"`
	FirstBuyTime      time.Time       `json:"first_buy_time"`
	TotalAmount       decimal.Decimal `json:"total_amount"`
	LastSellPrice     decimal.Decimal `json:"last_sell_price"`
	WaitingForDip     bool            `json:"waiting_for_dip"`
	ProcessedTradeIDs map[string]bool `json:"processed_trade_ids"`
}

type tradersvc interface {
	Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error
	OrderExecuted(ctx context.Context, clientOrderID string) (executed bool, filledAmount decimal.Decimal, err error)
	GetBalance(ctx context.Context, currency string) (decimal.Decimal, error)
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// DCAStrategy makes trades for specific trade pair using DCA strategy.
type DCAStrategy struct {
	pair                    entity.Pair
	amountPercent           decimal.Decimal
	tradePart               decimal.Decimal
	pricer                  pricer
	trader                  tradersvc
	l                       *zap.Logger
	wal                     *gowal.Wal
	journal                 *tradeJournal
	dcaSeries               *DCASeries
	maxDcaTrades            int
	dcaPercentThresholdBuy  decimal.Decimal
	dcaPercentThresholdSell decimal.Decimal
	seriesKey               string

	// Interval for checking order status (can be overridden for testing)
	orderCheckInterval time.Duration
}

// NewDCAStrategy creates new DCAStrategy instance.
func NewDCAStrategy(l *zap.Logger, pair entity.Pair, amountPercent decimal.Decimal, pricer pricer, trader tradersvc,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*DCAStrategy, error) {

	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("MaxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	if amountPercent.LessThan(decimal.NewFromInt(1)) || amountPercent.GreaterThan(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("amountPercent must be between 1 and 100, got %s", amountPercent.String())
	}

	walDir := filepath.Join("./wal", pair.String())
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		return nil, errors.Wrapf(err, "failed to ensure WAL directory %s", walDir)
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
		return nil, errors.Wrap(err, "failed to create WAL")
	}

	// try to recover DCA series from WAL
	dcaSeries := &DCASeries{
		Purchases:         make([]DCAPurchase, 0),
		ProcessedTradeIDs: make(map[string]bool),
	}

	tradeIntents := make([]*tradeIntentRecord, 0)

	for msg := range wal.Iterator() {
		if msg.Key == seriesKey {
			if err := json.Unmarshal(msg.Value, dcaSeries); err != nil {
				l.Error("failed to unmarshal DCA series", zap.Error(err))
				continue
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

	return &DCAStrategy{
		pair:                    pair,
		amountPercent:           amountPercent,
		tradePart:               initialTradePart,
		pricer:                  pricer,
		trader:                  trader,
		l:                       l,
		wal:                     wal,
		journal:                 newTradeJournal(wal, tradeIntents),
		dcaSeries:               dcaSeries,
		maxDcaTrades:            maxDcaTrades,
		dcaPercentThresholdBuy:  dcaPercentThresholdBuy,
		dcaPercentThresholdSell: dcaPercentThresholdSell,
		seriesKey:               seriesKey,
		orderCheckInterval:      defaultOrderCheckInterval,
	}, nil
}

// saveDCASeries saves the current DCA series to WAL
func (d *DCAStrategy) saveDCASeries() error {
	data, err := json.Marshal(d.dcaSeries)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	nextIndex := d.wal.CurrentIndex() + 1
	return d.wal.Write(nextIndex, d.seriesKey, data)
}

// calculateIndividualBuyAmount calculates buy amount based on current quote currency balance
func (d *DCAStrategy) calculateIndividualBuyAmount(ctx context.Context) (decimal.Decimal, error) {
	// get current balance in quote currency (e.g., USDT)
	quoteBalance, err := d.trader.GetBalance(ctx, d.pair.To)
	if err != nil {
		return decimal.Decimal{}, errors.Wrapf(err, "failed to get %s balance", d.pair.To)
	}

	// calculate buy amount as percentage of current balance
	// amountPercent is the percentage to use for each individual buy
	individualAmount := quoteBalance.Mul(d.amountPercent).Div(decimal.NewFromInt(percentageMultiplier))

	return individualAmount, nil
}

func (d *DCAStrategy) isTradeProcessed(intentID string) bool {
	if intentID == "" || d.dcaSeries == nil {
		return false
	}
	if len(d.dcaSeries.ProcessedTradeIDs) == 0 {
		return false
	}

	return d.dcaSeries.ProcessedTradeIDs[intentID]
}

func (d *DCAStrategy) markTradeProcessed(intentID string) {
	if intentID == "" || d.dcaSeries == nil {
		return
	}
	d.dcaSeries.ProcessedTradeIDs[intentID] = true
}

// AddDCAPurchase adds a new DCA purchase to the series and saves it to WAL
func (d *DCAStrategy) AddDCAPurchase(intentID string, price, amount decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	if intentID != "" && d.isTradeProcessed(intentID) {
		return nil
	}

	partNumber := tradePartValue
	if partNumber < 1 {
		partNumber = len(d.dcaSeries.Purchases) + 1
	}

	purchase := DCAPurchase{
		ID:        intentID,
		Price:     price,
		Amount:    amount,
		Time:      purchaseTime,
		TradePart: partNumber,
	}

	d.dcaSeries.Purchases = append(d.dcaSeries.Purchases, purchase)

	// update average entry price
	if len(d.dcaSeries.Purchases) == 1 {
		d.dcaSeries.AvgEntryPrice = price
		d.dcaSeries.FirstBuyTime = purchase.Time
		d.dcaSeries.TotalAmount = amount
	} else {
		oldTotalAmount := d.dcaSeries.TotalAmount
		d.dcaSeries.TotalAmount = oldTotalAmount.Add(amount)
		totalWeightedPrice := d.dcaSeries.AvgEntryPrice.Mul(oldTotalAmount).Add(price.Mul(amount))
		d.dcaSeries.AvgEntryPrice = totalWeightedPrice.Div(d.dcaSeries.TotalAmount)
	}

	d.tradePart = decimal.NewFromInt(int64(len(d.dcaSeries.Purchases)))

	d.markTradeProcessed(intentID)

	// apply virtual trade if using simulation trader
	if err := d.applySimulationTrade(price, amount, "buy"); err != nil {
		return err
	}

	return d.saveDCASeries()
}

// GetDCASeries returns the current DCA series
func (d *DCAStrategy) GetDCASeries() *DCASeries {
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

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
func (d *DCAStrategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	price, err := d.getValidatedPrice(ctx)
	if err != nil {
		return nil, err
	}

	tradeEvent, shouldReturn, err := d.checkWaitingForDip(ctx, price)
	if shouldReturn {
		return tradeEvent, err
	}

	if len(d.dcaSeries.Purchases) == 0 {
		return nil, nil
	}

	if tradeEvent, err := d.checkDCABuy(ctx, price); tradeEvent != nil || err != nil {
		return tradeEvent, err
	}

	if tradeEvent, err := d.checkProfitTaking(ctx, price); tradeEvent != nil || err != nil {
		return tradeEvent, err
	}

	return nil, nil
}

// getValidatedPrice retrieves and validates the current price
func (d *DCAStrategy) getValidatedPrice(ctx context.Context) (decimal.Decimal, error) {
	price, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		return decimal.Zero, errors.Wrapf(err, "pricer failed for pair %s", d.pair.String())
	}
	return price, nil
}

// checkWaitingForDip checks if we're waiting for a price dip and acts accordingly
// Returns (tradeEvent, shouldReturn, error) where shouldReturn indicates if the caller should return immediately
func (d *DCAStrategy) checkWaitingForDip(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, bool, error) {
	if !d.dcaSeries.WaitingForDip || d.dcaSeries.LastSellPrice.IsZero() {
		return nil, false, nil
	}

	percentChange := calculatePercentageChange(price, d.dcaSeries.LastSellPrice)
	if percentChange.GreaterThan(d.dcaPercentThresholdBuy.Neg()) {
		return nil, true, nil
	}

	d.l.Info("Price dropped from last sell price, initiating new DCA series.",
		zap.String("currentPrice", price.String()),
		zap.String("lastSellPrice", d.dcaSeries.LastSellPrice.String()),
		zap.String("percentChange", percentChange.String()))

	wasWaitingForDip := d.dcaSeries.WaitingForDip
	d.updateSellState(d.dcaSeries.LastSellPrice, false)

	tradeEvent, err := d.actBuy(ctx, price)
	if err != nil {
		d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
		d.updateSellState(d.dcaSeries.LastSellPrice, wasWaitingForDip)
		return tradeEvent, true, err
	}

	d.l.Info("Initial purchase recorded successfully",
		zap.String("price", price.String()),
		zap.String("amount", tradeEvent.Amount.String()))
	return tradeEvent, true, nil
}

// checkDCABuy checks if we should perform a DCA buy
func (d *DCAStrategy) checkDCABuy(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	if !price.LessThan(d.dcaSeries.AvgEntryPrice) {
		return nil, nil
	}

	if !isPercentDifferenceSignificant(price, d.dcaSeries.AvgEntryPrice, d.dcaPercentThresholdBuy) {
		return nil, nil
	}

	if !d.tradePart.LessThan(decimal.NewFromInt(int64(d.maxDcaTrades))) {
		return nil, nil
	}

	d.l.Info("Price significantly lower than average, attempting DCA buy.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))

	return d.actBuy(ctx, price)
}

// checkProfitTaking checks if we should take profit
func (d *DCAStrategy) checkProfitTaking(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	if !price.GreaterThan(d.dcaSeries.AvgEntryPrice) {
		return nil, nil
	}

	if !isPercentDifferenceSignificant(price, d.dcaSeries.AvgEntryPrice, d.dcaPercentThresholdSell) {
		return nil, nil
	}

	d.l.Info("Price significantly higher than average, attempting sell.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))

	return d.actSell(ctx, price)
}

func (d *DCAStrategy) actBuy(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	individualBuyAmount, err := d.calculateIndividualBuyAmount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to calculate buy amount")
	}

	operationTime := time.Now()
	tradePartValue := int(d.tradePart.IntPart()) + 1

	intent, err := d.journal.Prepare(intentActionBuy, price, individualBuyAmount, operationTime, tradePartValue, false)
	if err != nil {
		return nil, err
	}

	if err := d.trader.Buy(ctx, individualBuyAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", d.pair.String(), individualBuyAmount.String())
	}

	if err := d.AddDCAPurchase(intent.ID, price, individualBuyAmount, operationTime, tradePartValue); err != nil {
		d.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("intent_id", intent.ID))
		return &entity.TradeEvent{
			Action: entity.ActionBuy,
			Amount: individualBuyAmount,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	baseBalance, _ := d.trader.GetBalance(ctx, d.pair.From)
	quoteBalance, _ := d.trader.GetBalance(ctx, d.pair.To)

	d.l.Info("DCA buy executed",
		zap.Int("trade_part", int(d.tradePart.IntPart())),
		zap.String("price", price.String()),
		zap.String("amount", individualBuyAmount.String()),
		zap.String("avg_entry_price", d.dcaSeries.AvgEntryPrice.String()),
		zap.String(d.pair.From+"_balance", baseBalance.String()),
		zap.String(d.pair.To+"_balance", quoteBalance.String()))

	return &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: individualBuyAmount,
		Pair:   d.pair,
		Price:  price,
	}, nil
}

func (d *DCAStrategy) calculateSellAmount(profit decimal.Decimal) decimal.Decimal {
	doubleThreshold := d.dcaPercentThresholdSell.Mul(decimal.NewFromInt(2))

	// calculate total base currency holdings from quote currency position and average entry price
	// TotalAmount is in quote currency (e.g., USDT), need to convert to base currency (e.g., BTC)
	totalBaseAmount := d.dcaSeries.TotalAmount.Div(d.dcaSeries.AvgEntryPrice)

	if profit.GreaterThan(doubleThreshold) {
		return totalBaseAmount
	}

	if profit.GreaterThan(d.dcaPercentThresholdSell) {
		numPurchases := decimal.NewFromInt(int64(len(d.dcaSeries.Purchases)))
		if numPurchases.IsZero() {
			return decimal.Zero
		}

		avgPurchaseAmount := d.dcaSeries.TotalAmount.Div(numPurchases)
		individualBaseAmount := avgPurchaseAmount.Div(d.dcaSeries.AvgEntryPrice)

		// cap at total base amount if individual amount exceeds it
		if individualBaseAmount.GreaterThan(totalBaseAmount) {
			return totalBaseAmount
		}

		return individualBaseAmount
	}

	return decimal.Zero
}

func (d *DCAStrategy) actSell(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	profit := calculateProfit(price, d.dcaSeries.AvgEntryPrice)

	amountBaseCurrency := d.calculateSellAmount(profit)
	if amountBaseCurrency.LessThanOrEqual(decimal.Zero) {
		d.l.Debug("No sell action needed", zap.String("profit", profit.String()))
		return nil, nil
	}

	amountQuoteCurrency := amountBaseCurrency.Mul(price)

	operationTime := time.Now()

	// calculate total base amount to check if this is a full sell
	totalBaseAmount := d.dcaSeries.TotalAmount.Div(d.dcaSeries.AvgEntryPrice)
	isFullSell := amountBaseCurrency.Equal(totalBaseAmount)

	intent, err := d.journal.Prepare(intentActionSell, price, amountQuoteCurrency, operationTime, 0, isFullSell)
	if err != nil {
		return nil, err
	}

	// sell uses base currency amount (e.g., BTC)
	if err := d.trader.Sell(ctx, amountBaseCurrency, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", d.pair.String(), amountBaseCurrency.String())
	}

	if err := d.applyExecutedSell(intent); err != nil {
		d.l.Error("failed to apply sell intent to series",
			zap.Error(err),
			zap.String("intent_id", intent.ID))
		return &entity.TradeEvent{
			Action: entity.ActionSell,
			Amount: amountBaseCurrency,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	// get current balances for logging
	baseBalance, _ := d.trader.GetBalance(ctx, d.pair.From)
	quoteBalance, _ := d.trader.GetBalance(ctx, d.pair.To)

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: amountBaseCurrency,
		Pair:   d.pair,
		Price:  price,
	}

	d.l.Info("sell executed",
		zap.String("price", price.String()),
		zap.String("amountBase", amountBaseCurrency.String()),
		zap.String("amountQuote", amountQuoteCurrency.String()),
		zap.String("profit_percent", profit.String()),
		zap.String(d.pair.From+"_balance", baseBalance.String()),
		zap.String(d.pair.To+"_balance", quoteBalance.String()))

	return tradeEvent, nil
}

func (d *DCAStrategy) Close() error {
	return d.wal.Close()
}

func isPercentDifferenceSignificant(currentPrice, referencePrice, thresholdPercent decimal.Decimal) bool {
	if referencePrice.IsZero() {
		return false
	}

	diff := currentPrice.Sub(referencePrice)
	percentageDiff := diff.Div(referencePrice)
	absPercentageDiff := percentageDiff.Abs()
	absPercentageDiffHundred := absPercentageDiff.Mul(decimal.NewFromInt(percentageMultiplier))

	return absPercentageDiffHundred.GreaterThanOrEqual(thresholdPercent)
}

// calculatePercentageChange calculates the percentage change between current and reference price
func calculatePercentageChange(current, reference decimal.Decimal) decimal.Decimal {
	if reference.IsZero() {
		return decimal.Zero
	}
	return current.Sub(reference).Div(reference).Mul(decimal.NewFromInt(percentageMultiplier))
}

// calculateProfit calculates profit percentage based on current price and average entry price
func calculateProfit(price, avgEntryPrice decimal.Decimal) decimal.Decimal {
	if avgEntryPrice.IsZero() {
		return decimal.Zero
	}
	return price.Sub(avgEntryPrice).Div(avgEntryPrice).Mul(decimal.NewFromInt(percentageMultiplier))
}

func (d *DCAStrategy) updateSellState(price decimal.Decimal, waitingForDip bool) {
	if d.dcaSeries != nil {
		d.dcaSeries.LastSellPrice = price
		d.dcaSeries.WaitingForDip = waitingForDip
	}
}

func (d *DCAStrategy) removeAmountFromPurchases(amount decimal.Decimal) {
	if amount.LessThanOrEqual(decimal.Zero) || len(d.dcaSeries.Purchases) == 0 {
		return
	}

	remaining := amount

	for i := len(d.dcaSeries.Purchases) - 1; i >= 0 && remaining.GreaterThan(decimal.Zero); i-- {
		purchase := d.dcaSeries.Purchases[i]

		if purchase.Amount.LessThanOrEqual(remaining) {
			remaining = remaining.Sub(purchase.Amount)
			d.dcaSeries.Purchases = d.dcaSeries.Purchases[:i]
			continue
		}

		purchase.Amount = purchase.Amount.Sub(remaining)
		d.dcaSeries.Purchases[i] = purchase
		remaining = decimal.Zero
	}

	d.recalculateSeriesStats()
	d.tradePart = decimal.NewFromInt(int64(len(d.dcaSeries.Purchases)))
}

func (d *DCAStrategy) recalculateSeriesStats() {
	if len(d.dcaSeries.Purchases) == 0 {
		d.dcaSeries.TotalAmount = decimal.Zero
		d.dcaSeries.AvgEntryPrice = decimal.Zero
		d.dcaSeries.FirstBuyTime = time.Time{}
		return
	}

	totalAmount := decimal.Zero
	weightedPriceSum := decimal.Zero

	for _, purchase := range d.dcaSeries.Purchases {
		totalAmount = totalAmount.Add(purchase.Amount)
		weightedPriceSum = weightedPriceSum.Add(purchase.Price.Mul(purchase.Amount))
	}

	d.dcaSeries.TotalAmount = totalAmount
	d.dcaSeries.AvgEntryPrice = weightedPriceSum.Div(totalAmount)
	d.dcaSeries.FirstBuyTime = d.dcaSeries.Purchases[0].Time
}

// Initialize prepares the DCA strategy:
// 1. setting up initial price reference
// 2. executing initial buy if needed
// 3. reconciling any pending trade intents from WAL
func (d *DCAStrategy) Initialize(ctx context.Context) error {
	// reconcile any pending trade intents from WAL
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return err
	}

	// log current balances
	baseBalance, err := d.trader.GetBalance(ctx, d.pair.From)
	if err != nil {
		d.l.Warn("Failed to get base currency balance", zap.Error(err))
	}
	quoteBalance, err := d.trader.GetBalance(ctx, d.pair.To)
	if err != nil {
		d.l.Warn("Failed to get quote currency balance", zap.Error(err))
	}
	d.l.Info("Starting bot",
		zap.String("pair", d.pair.String()),
		zap.String(d.pair.From+"_balance", baseBalance.String()),
		zap.String(d.pair.To+"_balance", quoteBalance.String()))

	currentPrice, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		d.l.Error("Failed to get current price for initialization", zap.Error(err))
		return errors.Wrapf(err, "failed to get current price for %s", d.pair.String())
	}

	if d.maxDcaTrades < 1 {
		d.l.Error("MaxDcaTrades must be at least 1", zap.Int("maxDcaTrades", d.maxDcaTrades))
		return fmt.Errorf("MaxDcaTrades must be at least 1, configured value: %d", d.maxDcaTrades)
	}

	// validate that we can calculate initial buy amount
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

	// set initial reference price if not already set
	if d.dcaSeries.LastSellPrice.IsZero() {
		d.updateSellState(currentPrice, d.dcaSeries.WaitingForDip)
	}

	// check if we need to execute initial buy
	if len(d.dcaSeries.Purchases) > 0 {
		d.l.Info("DCA series already exists (loaded from WAL). Continuing with existing trades.",
			zap.Int("existingPurchases", len(d.dcaSeries.Purchases)))
		return nil
	}

	// execute initial buy
	d.l.Info("No existing DCA series. Executing initial buy.",
		zap.String("currentPrice", currentPrice.String()),
		zap.String("amount", calculatedInitialBuyAmount.String()))

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionBuy, currentPrice, calculatedInitialBuyAmount, operationTime, 1, false)
	if err != nil {
		return err
	}

	if err := d.trader.Buy(ctx, calculatedInitialBuyAmount, intent.ID); err != nil {
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

// applySimulationTrade applies a trade to the simulation trader if applicable
func (d *DCAStrategy) applySimulationTrade(price, amount decimal.Decimal, side string) error {
	if simTrader, ok := d.trader.(trader.SimulationTrader); ok {
		return simTrader.ApplyTrade(price, amount, side)
	}

	// not a simulation trader, no action needed
	return nil
}

// SyncSimulationBalance synchronizes simulation trader balance with loaded WAL purchases
// This is needed when restarting in simulation mode - the DCA series is loaded from WAL
// but the simulation trader starts with zero balances.
func (d *DCAStrategy) SyncSimulationBalance() error {
	simTrader, ok := d.trader.(trader.SimulationTrader)
	if !ok {
		// not a simulation trader, nothing to sync
		return nil
	}

	// apply all existing purchases to simulation balance
	for _, purchase := range d.dcaSeries.Purchases {
		if err := simTrader.ApplyTrade(purchase.Price, purchase.Amount, "buy"); err != nil {
			return errors.Wrapf(err, "failed to sync purchase %s to simulation balance", purchase.ID)
		}
		
		d.l.Debug("Synced purchase to simulation balance",
			zap.String("purchase_id", purchase.ID),
			zap.String("price", purchase.Price.String()),
			zap.String("amount", purchase.Amount.String()),
		)
	}

	if len(d.dcaSeries.Purchases) > 0 {
		d.l.Info("Synchronized simulation balance with WAL purchases",
			zap.String("pair", d.pair.String()),
			zap.Int("purchases_count", len(d.dcaSeries.Purchases)),
		)
	}

	return nil
}
