package services

import (
	"encoding/json"
	"fmt"
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

type tradersvc interface {
	Buy(amount decimal.Decimal) error
	Sell(amount decimal.Decimal) error
}

type pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

// TradeService makes trades for specific trade pair.
type TradeService struct {
	pair                    entity.Pair
	amount                  decimal.Decimal
	tradePart               decimal.Decimal
	pricer                  pricer
	trader                  tradersvc
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
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer pricer, trader tradersvc,
	maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell decimal.Decimal) (*TradeService, error) {

	if maxDcaTrades < 1 {
		return nil, fmt.Errorf("MaxDcaTrades must be at least 1, got %d", maxDcaTrades)
	}

	maxDcaTradesDecimal := decimal.NewFromInt(int64(maxDcaTrades))
	individualBuyAmount := amount.Div(maxDcaTradesDecimal)

	if individualBuyAmount.IsZero() {
		return nil, errors.New("calculated individual buy amount is zero, check total capital (Amount) and MaxDcaTrades")
	}

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

	if len(t.dcaSeries.Purchases) == 0 {
		t.l.Debug("No DCA purchases yet, no action taken by TradeService.Trade")
		return nil, nil
	}

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
		t.l.Info("Price significantly higher than average, attempting sell.",
			zap.String("price", price.String()),
			zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()))
		return t.actSell(price)
	}

	t.l.Debug("No significant price movement for action",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", t.dcaSeries.AvgEntryPrice.String()))
	return nil, nil
}

func (t *TradeService) Close() error {
	return t.wal.Close()
}

func (t *TradeService) actBuy(price decimal.Decimal) (*entity.TradeEvent, error) {
	if err := t.trader.Buy(t.individualBuyAmount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", t.pair.String(), t.individualBuyAmount.String())
	}

	if err := t.AddDCAPurchase(price, t.individualBuyAmount, time.Now(), int(t.tradePart.IntPart())); err != nil {
		t.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("pair", t.pair.String()))
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: t.individualBuyAmount,
		Pair:   t.pair,
		Price:  price,
	}

	t.l.Info("DCA buy executed",
		zap.String("pair", t.pair.String()),
		zap.Int("trade_part", int(t.tradePart.IntPart())),
		zap.String("price", price.String()),
		zap.String("amount", t.individualBuyAmount.String()),
		zap.String("avg_entry_price", t.dcaSeries.AvgEntryPrice.String()))

	t.tradePart = t.tradePart.Add(decimal.NewFromInt(1))

	return tradeEvent, nil
}

func (t *TradeService) actSell(price decimal.Decimal) (*entity.TradeEvent, error) {
	profit := price.Sub(t.dcaSeries.AvgEntryPrice).Div(t.dcaSeries.AvgEntryPrice).Mul(decimal.NewFromInt(100))

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
		return nil, nil
	}

	if amountToSell.GreaterThan(t.dcaSeries.TotalAmount) {
		t.l.Warn("Calculated sell amount exceeds total held amount. Selling total amount instead.",
			zap.String("calculatedAmount", amountToSell.String()),
			zap.String("totalAmount", t.dcaSeries.TotalAmount.String()))
		amountToSell = t.dcaSeries.TotalAmount
	}

	if amountToSell.LessThanOrEqual(decimal.Zero) {
		t.l.Info("Amount to sell is zero or less after calculations, no sell action taken.", zap.String("amountToSell", amountToSell.String()))
		return nil, nil
	}

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

		t.dcaSeries.TotalAmount = t.dcaSeries.TotalAmount.Sub(amountToSell)

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
		return false
	}

	diff := currentPrice.Sub(referencePrice)
	percentageDiff := diff.Div(referencePrice)
	absPercentageDiff := percentageDiff.Abs()
	absPercentageDiffHundred := absPercentageDiff.Mul(decimal.NewFromInt(100))

	return absPercentageDiffHundred.GreaterThan(thresholdPercent)
}

func (t *TradeService) SetLastSellPrice(price decimal.Decimal) {
	t.lastSellPrice = price
}

func (t *TradeService) SetWaitingForDip(waiting bool) {
	t.waitingForDip = waiting
}
