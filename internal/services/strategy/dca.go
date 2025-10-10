package strategy

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
	"go.uber.org/zap"
)

var (
	ErrNoData = errors.New("no data found")
)

const dcaSeriesKeyPrefix = "dca_series_"

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
}

type pricer interface {
	GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error)
}

// DCAStrategy makes trades for specific trade pair using DCA strategy.
type DCAStrategy struct {
	pair                    entity.Pair
	amount                  decimal.Decimal
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
	individualBuyAmount     decimal.Decimal
	lastSellPrice           decimal.Decimal
	waitingForDip           bool
	seriesKey               string
}

// NewDCAStrategy creates new DCAStrategy instance.
func NewDCAStrategy(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer pricer, trader tradersvc,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*DCAStrategy, error) {

	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("MaxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	maxDcaTradesDecimal := decimal.NewFromInt(int64(maxDcaTrades))
	individualBuyAmount := amount.Div(maxDcaTradesDecimal)

	if individualBuyAmount.IsZero() {
		return nil, errors.New("calculated individual buy amount is zero, check total capital (Amount) and MaxDcaTrades")
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

	// Try to recover DCA series from WAL
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
		amount:                  amount,
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
		individualBuyAmount:     individualBuyAmount,
		lastSellPrice:           dcaSeries.LastSellPrice,
		waitingForDip:           dcaSeries.WaitingForDip,
		seriesKey:               seriesKey,
	}, nil
}

// saveDCASeries saves the current DCA series to WAL
func (d *DCAStrategy) saveDCASeries() error {
	d.dcaSeries.LastSellPrice = d.lastSellPrice
	d.dcaSeries.WaitingForDip = d.waitingForDip

	data, err := json.Marshal(d.dcaSeries)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	nextIndex := d.wal.CurrentIndex() + 1
	return d.wal.Write(nextIndex, d.seriesKey, data)
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

	// Update average entry price
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

func (d *DCAStrategy) reconcileTradeIntents(ctx context.Context) error {
	if d.journal == nil {
		return nil
	}

	for _, intent := range d.journal.Intents() {
		switch intent.Status {
		case tradeIntentStatusDone, tradeIntentStatusFailed:
			continue
		case tradeIntentStatusPending:
			executed, filledAmount, err := d.trader.OrderExecuted(ctx, intent.ID)
			if err != nil {
				d.l.Error("failed to verify pending trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
				return err
			}

			if !executed {
				if filledAmount.GreaterThan(decimal.Zero) {
					d.l.Info("pending intent partially filled; waiting for completion",
						zap.String("intent_id", intent.ID),
						zap.String("action", string(intent.Action)),
						zap.String("filled_amount", filledAmount.String()),
						zap.String("requested_amount", intent.Amount.String()))
						
						time.Sleep(30 * time.Second)
					continue
				}

				d.l.Warn("pending intent not executed; marking as failed",
					zap.String("intent_id", intent.ID),
					zap.String("action", string(intent.Action)))
				d.markIntentFailed(intent, errors.New("order not executed"))
				continue
			}

			if filledAmount.LessThanOrEqual(decimal.Zero) {
				d.l.Warn("executed intent reported zero filled amount; marking as failed",
					zap.String("intent_id", intent.ID),
					zap.String("action", string(intent.Action)))
				d.markIntentFailed(intent, errors.New("filled amount reported as zero"))
				continue
			}

			if d.isTradeProcessed(intent.ID) {
				if intent.Status != tradeIntentStatusDone {
					if err := d.journal.MarkDone(intent); err != nil {
						d.l.Error("failed to persist completed trade intent", zap.Error(err), zap.String("intent_id", intent.ID))
						return err
					}
				}
				continue
			}

			if !filledAmount.Equal(intent.Amount) {
				if err := d.journal.UpdateAmount(intent, filledAmount); err != nil {
					d.l.Error("failed to persist actual filled amount for intent",
						zap.Error(err),
						zap.String("intent_id", intent.ID),
						zap.String("old_amount", intent.Amount.String()),
						zap.String("filled_amount", filledAmount.String()))
					return err
				}
			}

			if err := d.applyExecutedIntent(intent); err != nil {
				d.l.Error("failed to apply executed trade intent", zap.Error(err), zap.String("intent_id", intent.ID))
				return err
			}

			if err := d.journal.MarkDone(intent); err != nil {
				d.l.Error("failed to persist completed trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
				return err
			}
		default:
			d.l.Warn("encountered trade intent with unknown status", zap.String("status", intent.Status), zap.String("intent_id", intent.ID))
		}
	}
	return nil
}

func (d *DCAStrategy) applyExecutedIntent(intent *tradeIntentRecord) error {
	switch intent.Action {
	case intentActionBuy:
		return d.AddDCAPurchase(intent.ID, intent.Price, intent.Amount, intent.Time, intent.TradePart)
	case intentActionSell:
		return d.applyExecutedSell(intent)
	default:
		return fmt.Errorf("unknown trade intent action: %s", intent.Action)
	}
}

func (d *DCAStrategy) applyExecutedSell(intent *tradeIntentRecord) error {
	if d.isTradeProcessed(intent.ID) {
		return nil
	}

	amountToSell := intent.Amount

	if amountToSell.GreaterThan(d.dcaSeries.TotalAmount) {
		amountToSell = d.dcaSeries.TotalAmount
	}

	if amountToSell.LessThanOrEqual(decimal.Zero) {
		d.markTradeProcessed(intent.ID)
		return d.saveDCASeries()
	}

	isFullSell := intent.IsFullSell || amountToSell.Equal(d.dcaSeries.TotalAmount)

	if isFullSell {
		d.l.Info("Full sell executed. Resetting DCA series.", zap.String("amountSold", amountToSell.String()))
		d.dcaSeries = &DCASeries{
			Purchases:         make([]DCAPurchase, 0),
			ProcessedTradeIDs: d.dcaSeries.ProcessedTradeIDs,
		}
		d.tradePart = decimal.Zero
		d.SetLastSellPrice(intent.Price)
		d.SetWaitingForDip(true)
		d.l.Info("Waiting for price to drop before starting new DCA series",
			zap.String("lastSellPrice", intent.Price.String()),
			zap.String("requiredDropPercent", d.dcaPercentThresholdBuy.String()))
	} else {
		d.l.Info("Partial sell executed.", zap.String("amountSold", amountToSell.String()))
		d.removeAmountFromPurchases(amountToSell)
		if len(d.dcaSeries.Purchases) == 0 || d.dcaSeries.TotalAmount.LessThanOrEqual(decimal.Zero) {
			d.l.Info("Total amount became zero after partial sell. Resetting DCA series, waiting for price drop before starting new DCA series.",
				zap.String("remainingTotalAmount", d.dcaSeries.TotalAmount.String()))
			d.dcaSeries = &DCASeries{
				Purchases:         make([]DCAPurchase, 0),
				ProcessedTradeIDs: d.dcaSeries.ProcessedTradeIDs,
			}
			d.tradePart = decimal.Zero
			d.SetLastSellPrice(intent.Price)
			d.SetWaitingForDip(true)
		}
	}

	d.markTradeProcessed(intent.ID)

	if err := d.saveDCASeries(); err != nil {
		return err
	}

	return nil
}

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
func (d *DCAStrategy) Trade(ctx context.Context) (*entity.TradeEvent, error) {
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return nil, err
	}

	price, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", d.pair.String())
	}

	if d.waitingForDip && !d.lastSellPrice.IsZero() {
		percentChange := price.Sub(d.lastSellPrice).Div(d.lastSellPrice).Mul(decimal.NewFromInt(100))
		if percentChange.LessThanOrEqual(d.dcaPercentThresholdBuy.Neg()) {
			d.l.Info("Price dropped from last sell price, initiating new DCA series.",
				zap.String("currentPrice", price.String()),
				zap.String("lastSellPrice", d.lastSellPrice.String()),
				zap.String("percentChange", percentChange.String()))

			wasWaitingForDip := d.waitingForDip
			d.SetWaitingForDip(false)

			tradeEvent, err := d.actBuy(ctx, price)
			if err != nil {
				d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
				d.SetWaitingForDip(wasWaitingForDip)
				return tradeEvent, err
			}

			d.l.Info("Initial purchase recorded successfully",
				zap.String("pair", d.pair.String()),
				zap.String("price", price.String()),
				zap.String("amount", d.individualBuyAmount.String()))
			return tradeEvent, nil
		} else {
			d.l.Debug("Still waiting for price to drop from last sell price",
				zap.String("currentPrice", price.String()),
				zap.String("lastSellPrice", d.lastSellPrice.String()),
				zap.String("percentChange", percentChange.String()),
				zap.String("requiredDrop", d.dcaPercentThresholdBuy.String()))
			return nil, nil
		}
	}

	if len(d.dcaSeries.Purchases) == 0 {
		d.l.Debug("No DCA purchases yet, no action taken by DCAStrategy.Trade")
		return nil, ErrNoData
	}

	if price.LessThan(d.dcaSeries.AvgEntryPrice) &&
		isPercentDifferenceSignificant(price, d.dcaSeries.AvgEntryPrice, d.dcaPercentThresholdBuy) {
		if d.tradePart.LessThan(decimal.NewFromInt(int64(d.maxDcaTrades))) {
			d.l.Info("Price significantly lower than average, attempting DCA buy.",
				zap.String("price", price.String()),
				zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))
			tradeEvent, err := d.actBuy(ctx, price)
			return tradeEvent, err
		}
		d.l.Info("Price significantly lower, but max DCA trades reached or tradePart issue.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()),
			zap.Int32("tradePart", int32(d.tradePart.IntPart())),
			zap.Int("maxDcaTrades", d.maxDcaTrades))
	} else if price.GreaterThan(d.dcaSeries.AvgEntryPrice) &&
		isPercentDifferenceSignificant(price, d.dcaSeries.AvgEntryPrice, d.dcaPercentThresholdSell) {
		d.l.Info("Price significantly higher than average, attempting sell.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))
		return d.actSell(ctx, price)
	}

	d.l.Debug("No significant price movement for action",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))
	return nil, nil
}

func (d *DCAStrategy) Close() error {
	return d.wal.Close()
}

func (d *DCAStrategy) actBuy(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	operationTime := time.Now()
	tradePartValue := int(d.tradePart.IntPart()) + 1

	intent, err := d.journal.Prepare(intentActionBuy, price, d.individualBuyAmount, operationTime, tradePartValue, false)
	if err != nil {
		return nil, err
	}

	if err := d.trader.Buy(ctx, d.individualBuyAmount, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", d.pair.String(), d.individualBuyAmount.String())
	}

	if err := d.AddDCAPurchase(intent.ID, price, d.individualBuyAmount, operationTime, tradePartValue); err != nil {
		d.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("pair", d.pair.String()),
			zap.String("intent_id", intent.ID))
		return &entity.TradeEvent{
			Action: entity.ActionBuy,
			Amount: d.individualBuyAmount,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	d.l.Info("DCA buy executed",
		zap.String("pair", d.pair.String()),
		zap.Int("trade_part", int(d.tradePart.IntPart())),
		zap.String("price", price.String()),
		zap.String("amount", d.individualBuyAmount.String()),
		zap.String("avg_entry_price", d.dcaSeries.AvgEntryPrice.String()))

	return &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: d.individualBuyAmount,
		Pair:   d.pair,
		Price:  price,
	}, nil
}

func (d *DCAStrategy) actSell(ctx context.Context, price decimal.Decimal) (*entity.TradeEvent, error) {
	profit := price.Sub(d.dcaSeries.AvgEntryPrice).Div(d.dcaSeries.AvgEntryPrice).Mul(decimal.NewFromInt(100))

	amountToSell := decimal.Zero
	if profit.GreaterThan(d.dcaPercentThresholdSell) {
		amountToSell = d.individualBuyAmount
		d.l.Info("Profit above threshold, attempting to sell one individual part.",
			zap.String("individualBuyAmount", d.individualBuyAmount.String()),
			zap.String("profit", profit.String()),
			zap.String("threshold", d.dcaPercentThresholdSell.String()))

		if profit.GreaterThan(d.dcaPercentThresholdSell.Mul(decimal.NewFromInt(2))) {
			amountToSell = d.dcaSeries.TotalAmount
			d.l.Info("Profit above double threshold, attempting to sell total amount.",
				zap.String("totalAmount", d.dcaSeries.TotalAmount.String()),
				zap.String("profit", profit.String()),
				zap.String("threshold", d.dcaPercentThresholdSell.Mul(decimal.NewFromInt(2)).String()))
		}
	}

	if amountToSell.IsZero() {
		d.l.Debug("Sell condition met, but profit tier resulted in zero amount to sell.", zap.String("profit", profit.String()))
		return nil, nil
	}

	if amountToSell.GreaterThan(d.dcaSeries.TotalAmount) {
		d.l.Warn("Calculated sell amount exceeds total held amount. Selling total amount instead.",
			zap.String("calculatedAmount", amountToSell.String()),
			zap.String("totalAmount", d.dcaSeries.TotalAmount.String()))
		amountToSell = d.dcaSeries.TotalAmount
	}

	if amountToSell.LessThanOrEqual(decimal.Zero) {
		d.l.Info("Amount to sell is zero or less after calculations, no sell action taken.", zap.String("amountToSell", amountToSell.String()))
		return nil, nil
	}

	operationTime := time.Now()

	intent, err := d.journal.Prepare(intentActionSell, price, amountToSell, operationTime, 0, amountToSell.Equal(d.dcaSeries.TotalAmount))
	if err != nil {
		return nil, err
	}

	if err := d.trader.Sell(ctx, amountToSell, intent.ID); err != nil {
		d.markIntentFailed(intent, err)
		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", d.pair.String(), amountToSell.String())
	}

	if err := d.applyExecutedSell(intent); err != nil {
		d.l.Error("failed to apply sell intent to series",
			zap.Error(err),
			zap.String("pair", d.pair.String()),
			zap.String("intent_id", intent.ID))
		return &entity.TradeEvent{
			Action: entity.ActionSell,
			Amount: amountToSell,
			Pair:   d.pair,
			Price:  price,
		}, err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		return nil, err
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: intent.Amount,
		Pair:   d.pair,
		Price:  price,
	}

	d.l.Info("sell executed",
		zap.String("pair", d.pair.String()),
		zap.String("price", price.String()),
		zap.String("amount", amountToSell.String()),
		zap.String("profit_percent", profit.String()))

	return tradeEvent, nil
}

func isPercentDifferenceSignificant(currentPrice, referencePrice, thresholdPercent decimal.Decimal) bool {
	if referencePrice.IsZero() {
		return false
	}

	diff := currentPrice.Sub(referencePrice)
	percentageDiff := diff.Div(referencePrice)
	absPercentageDiff := percentageDiff.Abs()
	absPercentageDiffHundred := absPercentageDiff.Mul(decimal.NewFromInt(100))

	return absPercentageDiffHundred.GreaterThanOrEqual(thresholdPercent)
}

func (d *DCAStrategy) SetLastSellPrice(price decimal.Decimal) {
	d.lastSellPrice = price
	if d.dcaSeries != nil {
		d.dcaSeries.LastSellPrice = price
	}
}

func (d *DCAStrategy) SetWaitingForDip(waiting bool) {
	d.waitingForDip = waiting
	if d.dcaSeries != nil {
		d.dcaSeries.WaitingForDip = waiting
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

// Initialize prepares the DCA strategy by setting up initial price reference and executing initial buy if needed
func (d *DCAStrategy) Initialize(ctx context.Context) error {
	if err := d.reconcileTradeIntents(ctx); err != nil {
		return err
	}

	currentPrice, calculatedInitialBuyAmount, err := d.prepareInitialBuy(ctx)
	if err != nil {
		return err
	}

	return d.executeInitialBuy(ctx, currentPrice, calculatedInitialBuyAmount)
}

// prepareInitialBuy gets the current price and calculates the initial buy amount
func (d *DCAStrategy) prepareInitialBuy(ctx context.Context) (decimal.Decimal, decimal.Decimal, error) {
	currentPrice, err := d.pricer.GetPrice(ctx, d.pair)
	if err != nil {
		d.l.Error("Failed to get current price for initial buy check", zap.Error(err), zap.String("pair", d.pair.String()))
		return decimal.Zero, decimal.Zero, errors.Wrapf(err, "failed to get current price for initial buy check for %s", d.pair.String())
	}

	if d.maxDcaTrades < 1 {
		d.l.Error("Initial buy error: MaxDcaTrades must be at least 1.", zap.Int("maxDcaTrades", d.maxDcaTrades))
		return decimal.Zero, decimal.Zero, fmt.Errorf("MaxDcaTrades must be at least 1, configured value: %d", d.maxDcaTrades)
	}
	maxDcaTradesDecimal := decimal.NewFromInt(int64(d.maxDcaTrades))
	calculatedInitialBuyAmount := d.amount.Div(maxDcaTradesDecimal)

	if calculatedInitialBuyAmount.IsZero() {
		d.l.Error("Initial buy error: calculatedInitialBuyAmount is zero. Check Amount and MaxDcaTrades config.",
			zap.String("amount", d.amount.String()),
			zap.Int("maxDcaTrades", d.maxDcaTrades))
		return decimal.Zero, decimal.Zero, fmt.Errorf("calculatedInitialBuyAmount is zero, check Amount (%s) and MaxDcaTrades (%d)", d.amount.String(), d.maxDcaTrades)
	}

	if d.lastSellPrice.IsZero() {
		// Only set the reference price when there is no persisted sell price yet
		d.SetLastSellPrice(currentPrice)
	}

	return currentPrice, calculatedInitialBuyAmount, nil
}

// executeInitialBuy checks if a DCA series exists and executes initial buy if needed
func (d *DCAStrategy) executeInitialBuy(ctx context.Context, currentPrice decimal.Decimal, calculatedInitialBuyAmount decimal.Decimal) error {
	if len(d.dcaSeries.Purchases) == 0 {
		d.l.Info("No existing DCA series. Executing initial buy.",
			zap.String("pair", d.pair.String()),
			zap.String("currentPrice", currentPrice.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))

		operationTime := time.Now()

		intent, err := d.journal.Prepare(intentActionBuy, currentPrice, calculatedInitialBuyAmount, operationTime, 1, false)
		if err != nil {
			return err
		}

		if buyErr := d.trader.Buy(ctx, calculatedInitialBuyAmount, intent.ID); buyErr != nil {
			d.markIntentFailed(intent, buyErr)
			d.l.Error("Initial buy execution failed",
				zap.Error(buyErr),
				zap.String("pair", d.pair.String()))
			return errors.Wrapf(buyErr, "initial buy execution failed for %s", d.pair.String())
		}

		if err := d.AddDCAPurchase(intent.ID, currentPrice, calculatedInitialBuyAmount, operationTime, 1); err != nil {
			d.l.Error("Failed to record initial purchase state",
				zap.Error(err),
				zap.String("pair", d.pair.String()))
			return errors.Wrapf(err, "failed to record initial purchase state for %s", d.pair.String())
		}

		if err := d.journal.MarkDone(intent); err != nil {
			return err
		}

		d.l.Info("Initial buy executed successfully.",
			zap.String("pair", d.pair.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))
	} else {
		d.l.Info("DCA series already exists (loaded from WAL). Continuing with existing trades.",
			zap.String("pair", d.pair.String()),
			zap.Int("existingPurchases", len(d.dcaSeries.Purchases)))
	}

	return nil
}
