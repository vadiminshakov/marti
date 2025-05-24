package services

import (
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/entity"
	// "github.com/vadiminshakov/marti/internal/services/detector" // Removed
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

var (
	ErrNoData = errors.New("no data found")
)

// DCAPurchase represents a single DCA purchase
type DCAPurchase struct {
	Price     decimal.Decimal `json:"price"`
	Amount    decimal.Decimal `json:"amount"`
	Time      time.Time       `json:"time"`
	TradePart int             `json:"trade_part"`
}

// DCASeries represents a complete DCA series
type DCASeries struct {
	Purchases     []DCAPurchase   `json:"purchases"`
	AvgEntryPrice decimal.Decimal `json:"avg_entry_price"`
	FirstBuyTime  time.Time       `json:"first_buy_time"`
	TotalAmount   decimal.Decimal `json:"total_amount"`
}

// Pricer provides current price of asset in trade pair.
type Pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

type AnomalyDetector interface {
	// IsAnomaly checks whether price is anomaly or not
	IsAnomaly(price decimal.Decimal) bool
}

// TradeService makes trades for specific trade pair.
type TradeService struct {
	pair            entity.Pair
	amount          decimal.Decimal
	tradePart       decimal.Decimal
	pricer          Pricer
	// detector        detector.Detector, // Removed
	trader          trader.Trader
	// anomalyDetector AnomalyDetector, // Removed
	l                       *zap.Logger
	wal                     *gowal.Wal
	dcaSeries               *DCASeries
	noTrades                bool
	maxDcaTrades            int
	dcaPercentThresholdBuy  decimal.Decimal
	dcaPercentThresholdSell decimal.Decimal
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, // detector detector.Detector, // Removed
	trader trader.Trader,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*TradeService, error) {
	// Initialize WAL
	walCfg := gowal.Config{
		Dir:              "./wal",
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
		Purchases: make([]DCAPurchase, 0),
	}

	for msg := range wal.Iterator() {
		if msg.Key == "dca_series" {
			if err := json.Unmarshal(msg.Value, dcaSeries); err != nil {
				l.Error("failed to unmarshal DCA series", zap.Error(err))
				continue
			}
		}
	}

	return &TradeService{
		pair:            pair,
		amount:          amount,
		tradePart:       decimal.Zero,
		pricer:          pricer,
		// detector:        detector, // Removed
		trader:          trader,
		// anomalyDetector:         anomalyDetector, // Removed
		l:                       l,
		wal:                     wal,
		dcaSeries:               dcaSeries,
		noTrades:                len(dcaSeries.Purchases) == 0,
		maxDcaTrades:            maxDcaTrades,
		dcaPercentThresholdBuy:  dcaPercentThresholdBuy,
		dcaPercentThresholdSell: dcaPercentThresholdSell,
	}, nil
}

// saveDCASeries saves the current DCA series to WAL
func (t *TradeService) saveDCASeries() error {
	data, err := json.Marshal(t.dcaSeries)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	return t.wal.Write(uint64(time.Now().UnixNano()), "dca_series", data)
}

// addDCAPurchase adds a new DCA purchase to the series and saves it to WAL
func (t *TradeService) addDCAPurchase(price, amount decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	purchase := DCAPurchase{
		Price:     price,
		Amount:    amount,
		Time:      purchaseTime,     // Use passed purchaseTime
		TradePart: tradePartValue, // Use passed tradePartValue
	}

	t.dcaSeries.Purchases = append(t.dcaSeries.Purchases, purchase)
	t.dcaSeries.TotalAmount = t.dcaSeries.TotalAmount.Add(amount)

	// Update average entry price
	if len(t.dcaSeries.Purchases) == 1 {
		t.dcaSeries.AvgEntryPrice = price
		t.dcaSeries.FirstBuyTime = purchase.Time
	} else {
		t.dcaSeries.AvgEntryPrice = t.dcaSeries.AvgEntryPrice.Mul(t.dcaSeries.TotalAmount.Sub(amount)).
			Add(price.Mul(amount)).Div(t.dcaSeries.TotalAmount)
	}

	return t.saveDCASeries()
}

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
func (t *TradeService) Trade() (*entity.TradeEvent, error) {
	price, err := t.pricer.GetPrice(t.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", t.pair.String())
	}

	// If no purchases yet, TradeService doesn't act on its own until an initial position is established.
	if len(t.dcaSeries.Purchases) == 0 {
		t.l.Debug("No DCA purchases yet, no action taken by TradeService.Trade")
		return nil, nil
	}

	// Check for DCA buy opportunity
	if price.LessThan(t.dcaSeries.AvgEntryPrice) &&
		isPercentDifferenceSignificant(price, t.dcaSeries.AvgEntryPrice, t.dcaPercentThresholdBuy) {
		if t.tradePart.LessThan(decimal.NewFromInt(int64(t.maxDcaTrades))) {
			t.l.Info("Price significantly lower than average, attempting DCA buy.",
				zap.String("price", price.String()),
				zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()))
			tradeEvent, err := t.actBuy(price)
			if err == nil && tradeEvent != nil {
				t.noTrades = false
			}
			return tradeEvent, err
		}
		t.l.Info("Price significantly lower, but max DCA trades reached or tradePart issue.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()),
			zap.Int32("tradePart", t.tradePart.IntPart()),
			zap.Int("maxDcaTrades", t.maxDcaTrades))
	} else if price.GreaterThan(t.dcaSeries.AvgEntryPrice) &&
		isPercentDifferenceSignificant(price, t.dcaSeries.AvgEntryPrice, t.dcaPercentThresholdSell) {
		// Check for sell opportunity
		t.l.Info("Price significantly higher than average, attempting sell.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()))
		return t.actSell(price)
	}

	// No significant price movement for buy or sell
	t.l.Debug("No significant price movement for action",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()))
	return nil, nil
}

func (t *TradeService) Close() error {
	return t.wal.Close()
}

func (t *TradeService) actBuy(price decimal.Decimal) (*entity.TradeEvent, error) {
	// All pre-conditions (price < avgEntry, significant difference, tradePart < maxDcaTrades)
	// are now expected to be checked by the calling Trade() method.
	// This method focuses on executing the buy.

	// The check for len(t.dcaSeries.Purchases) > 0 is implicitly handled by Trade()
	// because AvgEntryPrice would not be meaningful otherwise for the checks in Trade().
	// If it's the very first buy (initial purchase), Trade() would not call actBuy;
	// it would be handled by TradingBot.Run's initial purchase logic which calls RecordInitialPurchase.
	// Subsequent DCA buys assume at least one purchase exists.

	// The check for t.tradePart < maxDcaTrades is handled in Trade() before calling actBuy.
	// if t.tradePart.GreaterThanOrEqual(decimal.NewFromInt(int64(t.maxDcaTrades))) {
	// 	t.l.Info("skip buy, maximum DCA trades reached",
	// 		zap.String("pair", t.pair.String()),
	// 		zap.Int("max_trades", t.maxDcaTrades))
	// 	return nil, nil
	// }

	// Use fixed amount t.amount for each DCA purchase.
	calculatedAmount := t.amount 

	// Execute the buy action
	if err := t.trader.Buy(calculatedAmount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", t.pair.String(), calculatedAmount.String())
	}

	// Add purchase to DCA series and save to WAL
	// t.tradePart was incremented by RecordInitialPurchase or a previous actBuy call.
	// It represents the current trade part number (e.g., 1 for the first DCA buy after initial, 2 for the second, etc.).
	if err := t.addDCAPurchase(price, calculatedAmount, time.Now(), int(t.tradePart.IntPart())); err != nil {
		t.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("pair", t.pair.String()))
		// Note: If saving fails, the buy already executed. This could lead to inconsistency.
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: calculatedAmount, // Use the calculatedAmount that was actually bought
		Pair:   t.pair,
		Price:  price,
	}

	t.l.Info("DCA buy executed",
		zap.String("pair", t.pair.String()),
		zap.Int("trade_part", int(t.tradePart.IntPart())), // Log current trade_part number
		zap.String("price", price.String()),
		zap.String("amount", calculatedAmount.String()), // Log actual amount bought
		zap.String("avg_entry_price", t.dcaSeries.AvgEntryPrice.String()))

	t.tradePart = t.tradePart.Add(decimal.NewFromInt(1)) // Increment for the *next* potential DCA buy

	return tradeEvent, nil
}

func (t *TradeService) actSell(price decimal.Decimal) (*entity.TradeEvent, error) {
	// All pre-conditions for calling actSell (e.g., len(purchases) > 0,
	// price > avgEntryPrice, and price difference is significant)
	// are now expected to be checked by the calling Trade() method.
	// This method focuses on executing the sell.

	// Calculate profit percentage
	profit := price.Sub(t.dcaSeries.AvgEntryPrice).Div(t.dcaSeries.AvgEntryPrice).Mul(decimal.NewFromInt(100))

	// Determine sell amount based on profit
	var amount decimal.Decimal
	if profit.GreaterThan(decimal.NewFromInt(5)) && profit.LessThan(decimal.NewFromInt(10)) {
		// Partial sell at 5-10% profit
		amount = t.dcaSeries.TotalAmount.Div(decimal.NewFromInt(2))
	} else if profit.GreaterThanOrEqual(decimal.NewFromInt(10)) {
		// Full sell at >=10% profit
		amount = t.dcaSeries.TotalAmount
	} else {
		return nil, nil
	}

	// Execute the sell action
	if err := t.trader.Sell(amount); err != nil {
		return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair.String())
	}

	// Reset DCA series
	t.dcaSeries = &DCASeries{
		Purchases: make([]DCAPurchase, 0),
	}
	if err := t.saveDCASeries(); err != nil {
		t.l.Error("failed to reset DCA series",
			zap.Error(err),
			zap.String("pair", t.pair.String()))
	}

	// Reset trade part counter
	t.tradePart = decimal.Zero

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: amount,
		Pair:   t.pair,
		Price:  price,
	}

	t.l.Info("sell executed",
		zap.String("pair", t.pair.String()),
		zap.String("price", price.String()),
		zap.String("amount", amount.String()),
		zap.String("profit_percent", profit.String()))

	return tradeEvent, nil
}

func isPercentDifferenceSignificant(currentPrice, referencePrice, thresholdPercent decimal.Decimal) bool {
	if referencePrice.IsZero() {
		// If reference is zero, any non-zero current price is an infinite percent difference.
		// If current price is also zero, then there's no difference.
		return !currentPrice.IsZero()
	}

	// Calculate abs((currentPrice - referencePrice) / referencePrice) * 100
	// diff = currentPrice - referencePrice
	diff := currentPrice.Sub(referencePrice)
	// percentageDiff = diff / referencePrice
	percentageDiff := diff.Div(referencePrice)
	// absPercentageDiff = abs(percentageDiff)
	absPercentageDiff := percentageDiff.Abs()
	// absPercentageDiffHundred = absPercentageDiff * 100
	absPercentageDiffHundred := absPercentageDiff.Mul(decimal.NewFromInt(100))

	return absPercentageDiffHundred.GreaterThan(thresholdPercent)
}

// RecordInitialPurchase records the very first purchase made by TradingBot.Run()
func (t *TradeService) RecordInitialPurchase(price, amount decimal.Decimal, purchaseTime time.Time) error {
	if len(t.dcaSeries.Purchases) != 0 {
		return errors.New("initial purchase already recorded or DCA series is not empty")
	}

	}

	// Call addDCAPurchase with the correct tradePart for the initial purchase (0)
	// and the provided purchaseTime.
	if err := t.addDCAPurchase(price, amount, purchaseTime, 0); err != nil {
		t.l.Error("Failed to add initial purchase to DCA series", zap.Error(err))
		return errors.Wrap(err, "failed to add initial purchase to series")
	}
	
	t.noTrades = false // A trade has now occurred

	// Increment tradePart to 1, as the first part (0) is now used.
	// Subsequent DCA buys will be part 1, 2, etc.
	t.tradePart = decimal.NewFromInt(1)

	// saveDCASeries is called within addDCAPurchase, so no need to call it again here
	// if we assume addDCAPurchase handles its own saving.
	// However, addDCAPurchase calls saveDCASeries, which uses time.Now() for WAL key.
	// This is fine. The important part is that the DCAPurchase struct has the correct purchaseTime.
	
	// The WAL saving is handled by addDCAPurchase.
	// If addDCAPurchase failed, it would have returned an error.

	t.l.Info("Initial purchase recorded successfully",
		zap.String("pair", t.pair.String()),
		zap.String("price", price.String()),
		zap.String("amount", amount.String()),
		zap.Time("time", purchaseTime),
		zap.Int32("trade_part_set_to", t.tradePart.IntPart()))

	return nil
}

