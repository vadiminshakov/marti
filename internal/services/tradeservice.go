package services

import (
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/detector"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

const (
	maxDcaTrades            = 15
	dcaPercentThresholdBuy  = 1
	dcaPercentThresholdSell = 7
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
	detector        detector.Detector
	trader          trader.Trader
	anomalyDetector AnomalyDetector
	l               *zap.Logger
	wal             *gowal.Wal
	dcaSeries       *DCASeries
	noTrades        bool
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, detector detector.Detector,
	trader trader.Trader, anomalyDetector AnomalyDetector) (*TradeService, error) {
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
		detector:        detector,
		trader:          trader,
		anomalyDetector: anomalyDetector,
		l:               l,
		wal:             wal,
		dcaSeries:       dcaSeries,
		noTrades:        len(dcaSeries.Purchases) == 0,
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
func (t *TradeService) addDCAPurchase(price, amount decimal.Decimal) error {
	purchase := DCAPurchase{
		Price:     price,
		Amount:    amount,
		Time:      time.Now(),
		TradePart: int(t.tradePart.IntPart()),
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

	act, err := t.detector.NeedAction(price)
	if err != nil {
		return nil, errors.Wrapf(err, "detector failed for pair %s", t.pair.String())
	}

	if t.anomalyDetector.IsAnomaly(price) {
		t.l.Debug("anomaly detected!")
		return nil, nil
	}

	var tradeEvent *entity.TradeEvent
	switch act {
	case entity.ActionBuy:
		tradeEvent, err = t.actBuy(price)
		if err != nil {
			return nil, err
		}
		t.noTrades = false
	case entity.ActionSell:
		tradeEvent, err = t.actSell(price)
		if err != nil {
			return nil, err
		}
	case entity.ActionNull:
		if len(t.dcaSeries.Purchases) > 0 {
			if price.LessThanOrEqual(t.dcaSeries.AvgEntryPrice) {
				if isPercentDifferenceSignificant(price, t.dcaSeries.AvgEntryPrice, dcaPercentThresholdBuy) {
					if t.tradePart.LessThan(decimal.NewFromInt(maxDcaTrades)) {
						return t.actBuy(price)
					}
				}
			}
		}
	}

	return tradeEvent, nil
}

func (t *TradeService) Close() error {
	return t.wal.Close()
}

func (t *TradeService) actBuy(price decimal.Decimal) (*entity.TradeEvent, error) {
	// Check if the price difference is significant enough
	if len(t.dcaSeries.Purchases) > 0 {
		if !isPercentDifferenceSignificant(price, t.dcaSeries.AvgEntryPrice, dcaPercentThresholdBuy) {
			return nil, nil
		}
	}

	// Check if the number of trades has reached the maximum allowed DCA trades
	if t.tradePart.GreaterThanOrEqual(decimal.NewFromInt(maxDcaTrades)) {
		t.l.Info("skip buy, maximum DCA trades reached",
			zap.String("pair", t.pair.String()),
			zap.Int("max_trades", maxDcaTrades))
		return nil, nil
	}

	// Calculate the amount to buy using progressive DCA
	baseAmount := t.amount.Div(decimal.NewFromInt(maxDcaTrades))
	multiplier := t.tradePart.Add(decimal.NewFromInt(1))
	amount := baseAmount.Mul(multiplier)

	// Execute the buy action
	if err := t.trader.Buy(amount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair.String())
	}

	// Add purchase to DCA series and save to WAL
	if err := t.addDCAPurchase(price, amount); err != nil {
		t.l.Error("failed to save DCA purchase",
			zap.Error(err),
			zap.String("pair", t.pair.String()))
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: amount,
		Pair:   t.pair,
		Price:  price,
	}

	t.l.Info("DCA buy executed",
		zap.String("pair", t.pair.String()),
		zap.Int("trade_part", int(t.tradePart.IntPart())+1),
		zap.String("price", price.String()),
		zap.String("amount", amount.String()),
		zap.String("avg_entry_price", t.dcaSeries.AvgEntryPrice.String()))

	t.tradePart = t.tradePart.Add(decimal.NewFromInt(1))

	return tradeEvent, nil
}

func (t *TradeService) actSell(price decimal.Decimal) (*entity.TradeEvent, error) {
	if len(t.dcaSeries.Purchases) == 0 {
		return nil, nil
	}

	// Check if the price difference is significant enough
	if !isPercentDifferenceSignificant(price, t.dcaSeries.AvgEntryPrice, dcaPercentThresholdSell) {
		return nil, nil
	}

	// If price is below average entry price and we haven't reached max DCA trades,
	// execute another buy instead of sell
	if price.LessThanOrEqual(t.dcaSeries.AvgEntryPrice) {
		if t.tradePart.LessThan(decimal.NewFromInt(maxDcaTrades)) {
			return t.actBuy(price)
		}
	}

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

func isPercentDifferenceSignificant(a, b decimal.Decimal, dcaPercentThreshold float64) bool {
	if a.Equal(b) {
		return false
	}

	if a.IsZero() || b.IsZero() {
		return true
	}

	var (
		diff    decimal.Decimal
		percent decimal.Decimal
	)
	if a.GreaterThan(b) {
		diff = a.Sub(b)
		percent = diff.Div(b).Mul(decimal.NewFromInt(100))
	} else {
		diff = b.Sub(a)
		percent = diff.Div(a).Mul(decimal.NewFromInt(100))
	}

	return percent.LessThan(decimal.NewFromFloat(-dcaPercentThreshold)) || percent.GreaterThan(decimal.NewFromFloat(dcaPercentThreshold))
}
