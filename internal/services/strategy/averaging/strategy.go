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
type Strategy struct {
	pair          entity.Pair
	amountPercent decimal.Decimal
	tradePart     decimal.Decimal
	pricer        Pricer
	trader        Tradersvc
	recorder      DecisionRecorder
	l             *zap.Logger
	wal           *gowal.Wal
	journal       *tradeJournal
	series        *entity.AveragingSeries
	thresholds    *entity.AveragingThresholds
	seriesKey     string
	// interval for checking order status (can be overridden for testing)
	orderCheckInterval time.Duration
	// positionSizer determines how much to buy at each step.
	positionSizer PositionSizer
	// strategyName identifies the concrete strategy ("dca", "martingale")
	// and is written into emitted decision events.
	strategyName string
}

// NewStrategy returns a configured averaging strategy.
// strategyName labels emitted decision events (e.g. "dca", "martingale").
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

	// try to recover DCA series from WAL
	series := entity.NewAveragingSeries()

	tradeIntents := make([]*tradeIntentRecord, 0)

	for record := range wal.Iterator() {
		if record.Key == seriesKey {
			if unmarshalErr := json.Unmarshal(record.Value, series); unmarshalErr != nil {
				l.Error("failed to unmarshal DCA series", zap.Error(unmarshalErr))

				continue
			}

			// ensure ProcessedTradeIDs is not nil after unmarshaling old WAL records.
			if series.ProcessedTradeIDs == nil {
				series.ProcessedTradeIDs = make(map[string]bool)
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

	initialTradePart := decimal.NewFromInt(int64(len(series.Purchases)))

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
		series:             series,
		thresholds:         &thresholds,
		orderCheckInterval: defaultOrderCheckInterval,
		seriesKey:          seriesKey,
		positionSizer:      sizer,
		strategyName:       strategyName,
	}, nil
}

// Initialize loads WAL and reconciles intents.
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
	if d.series.LastSellPrice.IsZero() {
		d.updateSellState(currentPrice, d.series.WaitingForDip)
	}

	// check if we need to execute initial buy.
	if len(d.series.Purchases) > 0 || d.series.WaitingForDip {
		if d.series.WaitingForDip {
			d.l.Info("Waiting for dip before starting new series.",
				zap.String("lastSellPrice", d.series.LastSellPrice.String()))
		} else {
			d.l.Info("Series already exists (loaded from WAL). Continuing with existing trades.",
				zap.Int("existingPurchases", len(d.series.Purchases)))
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
		AverageEntryPrice: d.series.AvgEntryPrice,
		TradePart:         1,
		QuoteBalance:      initialBuyQuoteAmount,
	}); err != nil {
		d.l.Error("failed to save averaging decision", zap.Error(err))
	}

	return nil
}

// Trade performs one evaluation cycle.
func (d *Strategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return nil, err
	}

	price, err := d.getValidatedPrice(ctx)
	if err != nil {
		return nil, err
	}

	// check if we're waiting for a dip after a sell.
	if d.series.WaitingForDip && !d.series.LastSellPrice.IsZero() {
		return d.handleWaitingForDip(ctx, price)
	}

	if d.series.IsEmpty() {
		return nil, nil
	}

	buy := d.series.ShouldBuyAtPrice(price, *d.thresholds)
	if buy.ShouldBuy {
		d.l.Info("Buy decision", zap.String("reason", buy.Reason))

		return d.executeBuy(ctx, price)
	}

	sell := d.series.ShouldTakeProfitAtPrice(price, *d.thresholds)
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

// SellAll closes all positions and resets strategy state.
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

	if d.series.TotalBaseAmount().IsZero() && realAmount.IsZero() {
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

// GetSeries exposes the current averaging series.
func (d *Strategy) GetSeries() *entity.AveragingSeries {
	return d.series
}

// GetCostBasis returns average entry price and total amount for PnL calculation.
func (d *Strategy) GetCostBasis() (entryPrice, amount decimal.Decimal) {
	if d.series == nil {
		return decimal.Zero, decimal.Zero
	}

	return d.series.AvgEntryPrice, d.series.TotalBaseAmount()
}

// AddPurchase records a purchase in the averaging series.
func (d *Strategy) AddPurchase(intentID string, price, amountQuote decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	if intentID != "" && d.isTradeProcessed(intentID) {
		return nil
	}

	partNumber := tradePartValue
	if partNumber < 1 {
		partNumber = len(d.series.Purchases) + 1
	}

	if err := d.series.AddPurchase(intentID, price, amountQuote, purchaseTime, partNumber); err != nil {
		return errors.Wrap(err, "failed to add purchase to series")
	}

	d.tradePart = decimal.NewFromInt(int64(len(d.series.Purchases)))
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

// handleWaitingForDip handles post-sell dip wait logic.
func (d *Strategy) handleWaitingForDip(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	percentChange := entity.PercentageDiff(price, d.series.LastSellPrice)
	if percentChange.GreaterThan(d.thresholds.BuyThresholdPercent.Neg()) {
		return nil, nil
	}

	d.l.Info("Price dropped from last sell price, initiating new DCA series.",
		zap.String("currentPrice", price.String()),
		zap.String("lastSellPrice", d.series.LastSellPrice.String()),
		zap.String("percentChange", percentChange.String()))

	wasWaitingForDip := d.series.WaitingForDip
	d.updateSellState(d.series.LastSellPrice, false)

	tradeEvent, err := d.executeBuy(ctx, price)
	if err != nil {
		d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
		d.updateSellState(d.series.LastSellPrice, wasWaitingForDip)

		return tradeEvent, err
	}

	d.l.Info("Initial purchase recorded successfully",
		zap.String("price", price.String()),
		zap.String("amount_base", tradeEvent.Amount.String()))

	return tradeEvent, nil
}

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
		zap.String("avgEntryPrice", d.series.AvgEntryPrice.String()))

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
		zap.String("avg_entry_price", d.series.AvgEntryPrice.String()))

	if err := d.recorder.SaveAveraging(entity.AveragingDecisionEvent{
		Timestamp:         operationTime,
		Pair:              d.pair.String(),
		Strategy:          d.strategyName,
		Action:            "buy",
		CurrentPrice:      price,
		AverageEntryPrice: d.series.AvgEntryPrice,
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

func (d *Strategy) executeSell(ctx context.Context, price decimal.Decimal, sell entity.SellDecision) (*entity.TradeEvent, error) {
	d.l.Info("Price significantly higher than average, attempting sell.",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.series.AvgEntryPrice.String()))

	amountBaseCurrency := sell.Amount
	amountQuoteProceeds := amountBaseCurrency.Mul(price)
	tradePartValue := d.series.CurrentSellTradePart()

	amountQuoteCost := amountBaseCurrency.Mul(d.series.AvgEntryPrice).Round(8)
	if sell.IsFullSell {
		amountQuoteCost = d.series.TotalAmount
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

	profit := entity.PercentageDiff(price, d.series.AvgEntryPrice)
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
		AverageEntryPrice: d.series.AvgEntryPrice,
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

func (d *Strategy) updateSellState(price decimal.Decimal, waitingForDip bool) {
	d.series.LastSellPrice = price
	d.series.WaitingForDip = waitingForDip
}

func (d *Strategy) calculateBuyQuoteAmount(ctx context.Context) (decimal.Decimal, error) {
	// if ongoing series, use stored allocated balance
	if d.series.AllocatedQuoteAmount.GreaterThan(decimal.Zero) {
		return d.positionSizer(d.series.AllocatedQuoteAmount, d.thresholds.MaxTrades, len(d.series.Purchases)), nil
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
	d.series.AllocatedQuoteAmount = allocatedAmount

	// calculate individual trade amount using the position sizer
	individualQuoteAmount := d.positionSizer(allocatedAmount, d.thresholds.MaxTrades, len(d.series.Purchases))

	return individualQuoteAmount, nil
}

func (d *Strategy) saveSeries() error {
	data, err := json.Marshal(d.series)
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

func (d *Strategy) isTradeProcessed(intentID string) bool {
	if intentID == "" {
		return false
	}

	if len(d.series.ProcessedTradeIDs) == 0 {
		return false
	}

	return d.series.ProcessedTradeIDs[intentID]
}

func (d *Strategy) markTradeProcessed(intentID string) {
	if intentID == "" {
		return
	}

	d.series.ProcessedTradeIDs[intentID] = true
}

func (d *Strategy) markIntentFailed(intent *tradeIntentRecord, cause error) {
	if d.journal == nil || intent == nil {
		return
	}

	if err := d.journal.MarkFailed(intent, cause); err != nil {
		d.l.Error("failed to persist failed trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
	}
}
