package services

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services/detector"
	"github.com/vadiminshakov/marti/internal/services/trader"
	"go.uber.org/zap"
)

const (
	maxDcaTrades            = 5
	dcaPercentThresholdBuy  = 0.1
	dcaPercentThresholdSell = 1
)

// Pricer provides current price of asset in trade pair.
type Pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

type AnomalyDetector interface {
	// IsAnomaly checks whether price is anomaly or not
	IsAnomaly(price decimal.Decimal) bool
}

type wal interface {
	GetLastBuyMeta() (BuyMetaData, error)
	Write(key string, value decimal.Decimal) error
	Close() error
}

// TradeService makes trades for specific trade pair.
type TradeService struct {
	pair            entity.Pair
	amount          decimal.Decimal
	lastBuyPrice    decimal.Decimal
	tradePart       decimal.Decimal
	pricer          Pricer
	detector        detector.Detector
	trader          trader.Trader
	anomalyDetector AnomalyDetector
	l               *zap.Logger
	wal             wal

	noTrades bool
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, detector detector.Detector,
	trader trader.Trader, anomalyDetector AnomalyDetector) (*TradeService, error) {
	w, err := NewWrappedWal()
	if err != nil {
		return nil, err
	}

	lastBuy, err := w.GetLastBuyMeta()
	if err != nil && !errors.Is(err, ErrNoData) {
		w.Close()
		return nil, err
	}

	return &TradeService{
		pair,
		amount,
		lastBuy.price,
		decimal.Zero,
		pricer,
		detector,
		trader,
		anomalyDetector,
		l, w,
		errors.Is(err, ErrNoData),
	}, nil
}

// Trade is the main method responsible for executing trading logic based on the current price of the asset.
// It performs the following steps:
// 1. Fetches the current price of the trading pair using the pricer service.
// 2. Determines the required action (buy, sell, or no action) using the detector service.
// 3. Checks for price anomalies using the anomaly detector. If an anomaly is detected, no action is taken.
// 4. Executes the appropriate action (buy or sell) based on the detected action.
// 5. Handles a special case for ActionNull (no action):
//   - If the current price is less than or equal to the last buy price and the price difference is significant,
//     it triggers a buy action if the maximum number of DCA trades has not been reached.
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
		}

		t.noTrades = false
	case entity.ActionSell:
		tradeEvent, err = t.actSell(price)
		if err != nil {
		}

	case entity.ActionNull:
		if price.LessThanOrEqual(t.lastBuyPrice) {
			if isPercentDifferenceSignificant(price, t.lastBuyPrice, dcaPercentThresholdBuy) {
				if t.tradePart.LessThan(decimal.NewFromInt(maxDcaTrades)) {
					return t.actBuy(price)
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
	// Check if the price difference between the current price and the last buy price
	// is significant enough to proceed with the buy action.
	// If not, return nil (no action is taken).
	if !isPercentDifferenceSignificant(price, t.lastBuyPrice, dcaPercentThresholdBuy) {
		return nil, nil
	}

	// Check if the number of trades (trade parts) has reached the maximum allowed DCA trades.
	// If so, skip the buy action and log a message.
	if t.tradePart.GreaterThanOrEqual(decimal.NewFromInt(maxDcaTrades)) {
		fmt.Println("skip buy, insufficient balance")
	}

	// Calculate the amount to buy by dividing the total amount by the maximum number of DCA trades.
	// This ensures that the total amount is evenly distributed across multiple trades.
	amount := t.amount.Div(decimal.NewFromInt(maxDcaTrades))

	// Execute the buy action using the trader's Buy method.
	if err := t.trader.Buy(amount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair.String())
	}

	// Write the last buy amount to the WAL (Write-Ahead Log) for persistence.
	if err := t.wal.Write("lastamount", amount); err != nil {
		return nil, errors.Wrapf(err, "failed to write last buy amount for pair %s", t.pair.String())
	}

	// Save the last buy price only if this is the first trade part (DCA).
	// This prevents overwriting the last buy price for subsequent trade parts.
	if t.tradePart.LessThan(decimal.NewFromInt(1)) {
		if err := t.wal.Write("lastbuy", price); err != nil {
			return nil, errors.Wrapf(err, "failed to write last buy price for pair %s", t.pair.String())
		}
		t.lastBuyPrice = price
	}

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionBuy,
		Amount: amount,
		Pair:   t.pair,
		Price:  price,
	}

	if t.tradePart.GreaterThan(decimal.NewFromInt(0)) {
		fmt.Println("DCA buy trade part", t.tradePart.Add(decimal.NewFromInt(1)),
			"price", price,
			"first DCA buy price", t.lastBuyPrice,
		)
	}

	t.tradePart = t.tradePart.Add(decimal.NewFromInt(1))

	return tradeEvent, nil
}

func (t *TradeService) actSell(price decimal.Decimal) (*entity.TradeEvent, error) {
	// If there is no recorded last buy price, return nil (no action is taken).
	if t.lastBuyPrice.IsZero() {
		return nil, nil
	}

	// Check if the price difference between the current price and the last buy price
	// is significant enough to proceed with the sell action.
	// If not, return nil (no action is taken).
	if !isPercentDifferenceSignificant(price, t.lastBuyPrice, dcaPercentThresholdSell) {
		return nil, nil
	}

	// If the current price is less than or equal to the last buy price,
	// and the number of trade parts is less than the maximum allowed DCA trades,
	// execute a buy action instead of a sell action (to average down the cost).
	if price.LessThanOrEqual(t.lastBuyPrice) {
		if t.tradePart.LessThan(decimal.NewFromInt(maxDcaTrades)) {
			return t.actBuy(price)
		}
	}

	// Calculate the total amount to sell by multiplying the amount per trade part
	// by the number of trade parts executed so far.
	amount := t.amount.Div(decimal.NewFromInt(maxDcaTrades)).Mul(t.tradePart)

	// Execute the sell action using the trader's Sell method.
	if err := t.trader.Sell(amount); err != nil {
		return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
	}

	// Reset the trade part counter to zero after a successful sell action.
	t.tradePart = decimal.Zero

	// Update the last buy price in the WAL (Write-Ahead Log) to reflect the current price.
	// This ensures that the system has an up-to-date record of the last buy price.
	if err := t.wal.Write("lastbuy", price); err != nil {
		return nil, errors.Wrapf(err, "failed to write last buy price for pair %s", t.pair.String())
	}
	t.lastBuyPrice = price

	tradeEvent := &entity.TradeEvent{
		Action: entity.ActionSell,
		Amount: amount,
		Pair:   t.pair,
		Price:  price,
	}

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
