package services

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/entity"
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

// TradeService makes trades for specific trade pair.
type TradeService struct {
	pair                    entity.Pair
	amount                  decimal.Decimal
	tradePart               decimal.Decimal
	pricer                  Pricer
	trader                  trader.Trader
	l                       *zap.Logger
	wal                     *gowal.Wal
	dcaSeries               *DCASeries
	maxDcaTrades            int
	dcaPercentThresholdBuy  decimal.Decimal
	dcaPercentThresholdSell decimal.Decimal
	individualBuyAmount     decimal.Decimal
	lastSellPrice           decimal.Decimal
	waitingForDip           bool
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, trader trader.Trader,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*TradeService, error) {

	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("MaxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	maxDcaTradesDecimal := decimal.NewFromInt(int64(maxDcaTrades))
	// amount is total capital
	individualBuyAmount := amount.Div(maxDcaTradesDecimal)

	if individualBuyAmount.IsZero() {
		// This can happen if total capital (amount) is very small or maxDcaTrades is very large.
		// It's a configuration issue that would lead to zero-amount buys.
		return nil, errors.New("calculated individual buy amount is zero, check total capital (Amount) and MaxDcaTrades")
	}

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
		pair:                    pair,
		amount:                  amount,
		tradePart:               decimal.Zero,
		pricer:                  pricer,
		trader:                  trader,
		l:                       l,
		wal:                     wal,
		dcaSeries:               dcaSeries,
		maxDcaTrades:            maxDcaTrades,
		dcaPercentThresholdBuy:  dcaPercentThresholdBuy,
		dcaPercentThresholdSell: dcaPercentThresholdSell,
		individualBuyAmount:     individualBuyAmount,
		lastSellPrice:           decimal.Zero,
		waitingForDip:           false,
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

// AddDCAPurchase adds a new DCA purchase to the series and saves it to WAL
func (t *TradeService) AddDCAPurchase(price, amount decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	purchase := DCAPurchase{
		Price:     price,
		Amount:    amount,
		Time:      purchaseTime,   // Use passed purchaseTime
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

// GetDCASeries returns the current DCA series
func (t *TradeService) GetDCASeries() *DCASeries {
	return t.dcaSeries
}

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
func (t *TradeService) Trade() (*entity.TradeEvent, error) {
	price, err := t.pricer.GetPrice(t.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", t.pair.String())
	}

	if t.waitingForDip && !t.lastSellPrice.IsZero() {
		percentChange := price.Sub(t.lastSellPrice).Div(t.lastSellPrice).Mul(decimal.NewFromInt(100))
		if percentChange.LessThanOrEqual(t.dcaPercentThresholdBuy.Neg()) {
			t.l.Info("Price dropped from last sell price, initiating new DCA series.",
				zap.String("currentPrice", price.String()),
				zap.String("lastSellPrice", t.lastSellPrice.String()),
				zap.String("percentChange", percentChange.String()))

			t.waitingForDip = false

			if err := t.AddDCAPurchase(price, t.individualBuyAmount, time.Now(), 0); err != nil {
				t.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
				return nil, err
			}
			t.tradePart = decimal.NewFromInt(1)

			t.l.Info("Initial purchase recorded successfully",
				zap.String("pair", t.pair.String()),
				zap.String("price", price.String()),
				zap.String("amount", t.individualBuyAmount.String()),
				zap.Time("time", time.Now()))

			tradeEvent := &entity.TradeEvent{
				Action: entity.ActionBuy,
				Amount: t.individualBuyAmount,
				Pair:   t.pair,
				Price:  price,
			}

			if err := t.trader.Buy(t.individualBuyAmount); err != nil {
				return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", t.pair.String(), t.individualBuyAmount.String())
			}

			return tradeEvent, nil
		} else {
			t.l.Debug("Still waiting for price to drop from last sell price",
				zap.String("currentPrice", price.String()),
				zap.String("lastSellPrice", t.lastSellPrice.String()),
				zap.String("percentChange", percentChange.String()),
				zap.String("requiredDrop", t.dcaPercentThresholdBuy.String()))
			return nil, nil
		}
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
			return tradeEvent, err
		}
		t.l.Info("Price significantly lower, but max DCA trades reached or tradePart issue.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()),
			zap.Int32("tradePart", int32(t.tradePart.IntPart())),
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

	// Use fixed individual buy amount calculated at initialization.
	calculatedAmount := t.individualBuyAmount

	// Execute the buy action
	if err := t.trader.Buy(calculatedAmount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", t.pair.String(), calculatedAmount.String())
	}

	// Add purchase to DCA series and save to WAL
	// t.tradePart was incremented by RecordInitialPurchase or a previous actBuy call.
	// It represents the current trade part number (e.g., 1 for the first DCA buy after initial, 2 for the second, etc.).
	if err := t.AddDCAPurchase(price, calculatedAmount, time.Now(), int(t.tradePart.IntPart())); err != nil {
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

	// Determine sell amount based on the dcaPercentThresholdSell
	amountToSell := decimal.Zero
	if profit.GreaterThan(t.dcaPercentThresholdSell) {
		amountToSell = t.individualBuyAmount
		t.l.Info("Profit above threshold, attempting to sell one individual part.",
			zap.String("individualBuyAmount", t.individualBuyAmount.String()),
			zap.String("profit", profit.String()),
			zap.String("threshold", t.dcaPercentThresholdSell.String()))

		if profit.GreaterThan(t.dcaPercentThresholdSell.Mul(decimal.NewFromInt(2))) {
			amountToSell = t.dcaSeries.TotalAmount
			t.l.Info("Profit above double threshold, attempting to sell total amount.",
				zap.String("totalAmount", t.dcaSeries.TotalAmount.String()),
				zap.String("profit", profit.String()),
				zap.String("threshold", t.dcaPercentThresholdSell.Mul(decimal.NewFromInt(2)).String()))
		}
	}

	if amountToSell.IsZero() {
		t.l.Debug("Sell condition met, but profit tier resulted in zero amount to sell.", zap.String("profit", profit.String()))
		return nil, nil // No trade event
	}

	if amountToSell.GreaterThan(t.dcaSeries.TotalAmount) {
		t.l.Warn("Calculated sell amount exceeds total held amount. Selling total amount instead.",
			zap.String("calculatedAmount", amountToSell.String()),
			zap.String("totalAmount", t.dcaSeries.TotalAmount.String()))
		amountToSell = t.dcaSeries.TotalAmount
	}

	if amountToSell.LessThanOrEqual(decimal.Zero) { // Ensure we are selling a positive amount
		t.l.Info("Amount to sell is zero or less after calculations, no sell action taken.", zap.String("amountToSell", amountToSell.String()))
		return nil, nil
	}

	// Execute the sell action
	if err := t.trader.Sell(amountToSell); err != nil {
		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", t.pair.String(), amountToSell.String())
	}

	isFullSell := amountToSell.Equal(t.dcaSeries.TotalAmount)

	if isFullSell {
		t.l.Info("Full sell executed. Resetting DCA series.", zap.String("amountSold", amountToSell.String()))
		// Reset DCA series
		t.dcaSeries = &DCASeries{
			Purchases: make([]DCAPurchase, 0),
		}
		// Reset trade part counter
		t.tradePart = decimal.Zero

		t.lastSellPrice = price
		t.waitingForDip = true
		t.l.Info("Waiting for price to drop before starting new DCA series",
			zap.String("lastSellPrice", price.String()),
			zap.String("requiredDropPercent", t.dcaPercentThresholdBuy.String()))
	} else {
		t.l.Info("Partial sell executed.", zap.String("amountSold", amountToSell.String()))
		// Update total amount. AvgEntryPrice and tradePart remain.
		// Purchases slice modification for partial sell is complex (e.g. FIFO/LIFO accounting for specific parts sold).
		// For this simplification, we only adjust TotalAmount. AvgEntryPrice remains.
		// This means avg entry price might become less accurate over many partial sells if not all parts had same entry.
		// However, our DCA buys are at different prices, so AvgEntryPrice is an average.
		// Selling a part at profit doesn't change the avg entry of remaining parts.
		t.dcaSeries.TotalAmount = t.dcaSeries.TotalAmount.Sub(amountToSell)
		// Note: If t.dcaSeries.TotalAmount becomes zero or negative due to this partial sell (e.g. if individualBuyAmount was larger than remaining total),
		// it should ideally be treated as a full sell. The safeguard `amountToSell.GreaterThan(t.dcaSeries.TotalAmount)` handles over-selling.
		// If `t.dcaSeries.TotalAmount` becomes zero exactly after a partial sell, the next `Trade()` call will see `len(purchases) > 0` but `TotalAmount == 0`.
		// This state might need further handling or consideration if it's possible and problematic.
		// For now, we assume individualBuyAmount is less than TotalAmount for a partial sell to be meaningful.
		if t.dcaSeries.TotalAmount.LessThanOrEqual(decimal.Zero) {
			t.l.Info("Total amount became zero or less after partial sell. Treating as full sell for reset purposes.",
				zap.String("remainingTotalAmount", t.dcaSeries.TotalAmount.String()))
			t.dcaSeries = &DCASeries{Purchases: make([]DCAPurchase, 0)} // Reset purchases
			t.tradePart = decimal.Zero                                  // Reset tradePart
			t.lastSellPrice = price
			t.waitingForDip = true
		}
	}

	if err := t.saveDCASeries(); err != nil {
		t.l.Error("failed to save DCA series after sell",
			zap.Error(err),
			zap.String("pair", t.pair.String()))
		// This is also a state where trade executed but saving state failed.
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: amountToSell,
		Pair:   t.pair,
		Price:  price,
	}

	t.l.Info("sell executed",
		zap.String("pair", t.pair.String()),
		zap.String("price", price.String()),
		zap.String("amount", amountToSell.String()),
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

// SetLastSellPrice sets the last sell price
func (t *TradeService) SetLastSellPrice(price decimal.Decimal) {
	t.lastSellPrice = price
}

// SetWaitingForDip sets the waiting for dip flag
func (t *TradeService) SetWaitingForDip(waiting bool) {
	t.waitingForDip = waiting
}
