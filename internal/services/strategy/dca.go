package strategy

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

// DCAStrategy makes trades for specific trade pair using DCA strategy.
type DCAStrategy struct {
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

	return &DCAStrategy{
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
func (d *DCAStrategy) saveDCASeries() error {
	data, err := json.Marshal(d.dcaSeries)
	if err != nil {
		return errors.Wrap(err, "failed to marshal DCA series")
	}

	return d.wal.Write(uint64(time.Now().UnixNano()), "dca_series", data)
}

// AddDCAPurchase adds a new DCA purchase to the series and saves it to WAL
func (d *DCAStrategy) AddDCAPurchase(price, amount decimal.Decimal, purchaseTime time.Time, tradePartValue int) error {
	purchase := DCAPurchase{
		Price:     price,
		Amount:    amount,
		Time:      purchaseTime,   // Use passed purchaseTime
		TradePart: tradePartValue, // Use passed tradePartValue
	}

	d.dcaSeries.Purchases = append(d.dcaSeries.Purchases, purchase)
	d.dcaSeries.TotalAmount = d.dcaSeries.TotalAmount.Add(amount)

	// Update average entry price
	if len(d.dcaSeries.Purchases) == 1 {
		d.dcaSeries.AvgEntryPrice = price
		d.dcaSeries.FirstBuyTime = purchase.Time
	} else {
		d.dcaSeries.AvgEntryPrice = d.dcaSeries.AvgEntryPrice.Mul(d.dcaSeries.TotalAmount.Sub(amount)).
			Add(price.Mul(amount)).Div(d.dcaSeries.TotalAmount)
	}

	return d.saveDCASeries()
}

// GetDCASeries returns the current DCA series
func (d *DCAStrategy) GetDCASeries() *DCASeries {
	return d.dcaSeries
}

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
func (d *DCAStrategy) Trade() (*entity.TradeEvent, error) {
	price, err := d.pricer.GetPrice(d.pair)
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

			d.SetWaitingForDip(false)

			if err := d.AddDCAPurchase(price, d.individualBuyAmount, time.Now(), 0); err != nil {
				d.l.Error("Failed to record initial purchase after price drop", zap.Error(err))
				return nil, err
			}
			d.tradePart = decimal.NewFromInt(1)

			d.l.Info("Initial purchase recorded successfully",
				zap.String("pair", d.pair.String()),
				zap.String("price", price.String()),
				zap.String("amount", d.individualBuyAmount.String()),
				zap.Time("time", time.Now()))

			tradeEvent := &entity.TradeEvent{
				Action: entity.ActionBuy,
				Amount: d.individualBuyAmount,
				Pair:   d.pair,
				Price:  price,
			}

			if err := d.trader.Buy(d.individualBuyAmount); err != nil {
				return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", d.pair.String(), d.individualBuyAmount.String())
			}

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
			tradeEvent, err := d.actBuy(price)
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
		return d.actSell(price)
	}

	d.l.Debug("No significant price movement for action",
		zap.String("price", price.String()),
		zap.String("avgEntryPrice", d.dcaSeries.AvgEntryPrice.String()))
	return nil, nil
}

func (d *DCAStrategy) Close() error {
	return d.wal.Close()
}

func (d *DCAStrategy) actBuy(price decimal.Decimal) (*entity.TradeEvent, error) {
	if err := d.trader.Buy(d.individualBuyAmount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s with amount %s", d.pair.String(), d.individualBuyAmount.String())
	}

	if err := d.AddDCAPurchase(price, d.individualBuyAmount, time.Now(), int(d.tradePart.IntPart())); err != nil {
		d.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("pair", d.pair.String()))
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: d.individualBuyAmount,
		Pair:   d.pair,
		Price:  price,
	}

	d.l.Info("DCA buy executed",
		zap.String("pair", d.pair.String()),
		zap.Int("trade_part", int(d.tradePart.IntPart())),
		zap.String("price", price.String()),
		zap.String("amount", d.individualBuyAmount.String()),
		zap.String("avg_entry_price", d.dcaSeries.AvgEntryPrice.String()))

	d.tradePart = d.tradePart.Add(decimal.NewFromInt(1))

	return tradeEvent, nil
}

func (d *DCAStrategy) actSell(price decimal.Decimal) (*entity.TradeEvent, error) {
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

	if err := d.trader.Sell(amountToSell); err != nil {
		return nil, errors.Wrapf(err, "trader sell failed for pair %s, amount %s", d.pair.String(), amountToSell.String())
	}

	isFullSell := amountToSell.Equal(d.dcaSeries.TotalAmount)

	if isFullSell {
		d.l.Info("Full sell executed. Resetting DCA series.", zap.String("amountSold", amountToSell.String()))
		// Reset DCA series
		d.dcaSeries = &DCASeries{
			Purchases: make([]DCAPurchase, 0),
		}
		// Reset trade part counter
		d.tradePart = decimal.Zero

		d.lastSellPrice = price
		d.SetWaitingForDip(true)
		d.l.Info("Waiting for price to drop before starting new DCA series",
			zap.String("lastSellPrice", price.String()),
			zap.String("requiredDropPercent", d.dcaPercentThresholdBuy.String()))
	} else {
		d.l.Info("Partial sell executed.", zap.String("amountSold", amountToSell.String()))

		d.dcaSeries.TotalAmount = d.dcaSeries.TotalAmount.Sub(amountToSell)

		if d.dcaSeries.TotalAmount.LessThanOrEqual(decimal.Zero) {
			d.l.Info("Total amount became zero after partial sell. Resetting DCA series, waiting for price drop before starting new DCA series.",
				zap.String("remainingTotalAmount", d.dcaSeries.TotalAmount.String()))
			d.dcaSeries = &DCASeries{Purchases: make([]DCAPurchase, 0)} // Reset purchases
			d.tradePart = decimal.Zero                                  // Reset tradePart
			d.lastSellPrice = price
			d.SetWaitingForDip(true)
		}
	}

	if err := d.saveDCASeries(); err != nil {
		d.l.Error("failed to save DCA series after sell",
			zap.Error(err),
			zap.String("pair", d.pair.String()))
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: amountToSell,
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

	return absPercentageDiffHundred.GreaterThan(thresholdPercent)
}

func (d *DCAStrategy) SetLastSellPrice(price decimal.Decimal) {
	d.lastSellPrice = price
}

func (d *DCAStrategy) SetWaitingForDip(waiting bool) {
	d.waitingForDip = waiting
}

// Initialize prepares the DCA strategy by setting up initial price reference and executing initial buy if needed
func (d *DCAStrategy) Initialize() error {
	currentPrice, calculatedInitialBuyAmount, err := d.prepareInitialBuy()
	if err != nil {
		return err
	}
	
	return d.executeInitialBuy(currentPrice, calculatedInitialBuyAmount)
}

// prepareInitialBuy gets the current price and calculates the initial buy amount
func (d *DCAStrategy) prepareInitialBuy() (decimal.Decimal, decimal.Decimal, error) {
	currentPrice, err := d.pricer.GetPrice(d.pair)
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

	// Set initial price as reference
	d.SetLastSellPrice(currentPrice)
	
	return currentPrice, calculatedInitialBuyAmount, nil
}

// executeInitialBuy checks if a DCA series exists and executes initial buy if needed
func (d *DCAStrategy) executeInitialBuy(currentPrice decimal.Decimal, calculatedInitialBuyAmount decimal.Decimal) error {
	if len(d.dcaSeries.Purchases) == 0 {
		d.l.Info("No existing DCA series. Executing initial buy.",
			zap.String("pair", d.pair.String()),
			zap.String("currentPrice", currentPrice.String()),
			zap.String("amount", calculatedInitialBuyAmount.String()))
		
		if buyErr := d.trader.Buy(calculatedInitialBuyAmount); buyErr != nil {
			d.l.Error("Initial buy execution failed",
				zap.Error(buyErr),
				zap.String("pair", d.pair.String()))
			return errors.Wrapf(buyErr, "initial buy execution failed for %s", d.pair.String())
		}
		
		if err := d.AddDCAPurchase(currentPrice, calculatedInitialBuyAmount, time.Now(), 0); err != nil {
			d.l.Error("Failed to record initial purchase state",
				zap.Error(err),
				zap.String("pair", d.pair.String()))
			return errors.Wrapf(err, "failed to record initial purchase state for %s", d.pair.String())
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