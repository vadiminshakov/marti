package services

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
	"go.uber.org/zap"
)

const (
	maxDcaTrades            = 5
	dcaPercentThresholdBuy  = 0.1
	dcaPercentThresholdSell = 1
)

// Detector checks need to buy, sell assets or do nothing. This service must be
// instantiated for every trade pair separately.
type Detector interface {
	// NeedAction checks need to buy, sell assets or do nothing.
	NeedAction(price decimal.Decimal) (entity.Action, error)
	// LastAction returns last decision made by detector.
	LastAction() entity.Action
}

// Pricer provides current price of asset in trade pair.
type Pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

// Trader makes buy and sell actions for trade pair.
type Trader interface {
	// Buy buys amount of asset in trade pair.
	Buy(amount decimal.Decimal) error
	// Sell sells amount of asset in trade pair.
	Sell(amount decimal.Decimal) error
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
	detector        Detector
	trader          Trader
	anomalyDetector AnomalyDetector
	l               *zap.Logger
	wal             wal

	noTrades bool
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, detector Detector,
	trader Trader, anomalyDetector AnomalyDetector) (*TradeService, error) {
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

// Trade checks current price of asset and decides whether to buy, sell or do anything.
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
	if !isPercentDifferenceSignificant(price, t.lastBuyPrice, dcaPercentThresholdBuy) {
		return nil, nil
	}

	if t.tradePart.GreaterThanOrEqual(decimal.NewFromInt(maxDcaTrades)) {
		fmt.Println("skip buy, insufficient balance")
	}

	amount := t.amount.Div(decimal.NewFromInt(maxDcaTrades))
	if err := t.trader.Buy(amount); err != nil {
		return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair.String())
	}

	if err := t.wal.Write("lastamount", amount); err != nil {
		return nil, errors.Wrapf(err, "failed to write last buy amount for pair %s", t.pair.String())
	}

	// save last buy price if trade part is less than 1
	// to prevent saving last buy price for every trade part (DCA)
	// we need to store last buy price only for the first trade part
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
	if t.lastBuyPrice.IsZero() {
		return nil, nil
	}

	if !isPercentDifferenceSignificant(price, t.lastBuyPrice, dcaPercentThresholdSell) {
		return nil, nil
	}

	if price.LessThanOrEqual(t.lastBuyPrice) {
		if t.tradePart.LessThan(decimal.NewFromInt(maxDcaTrades)) {
			return t.actBuy(price)
		}

	}

	amount := t.amount.Div(decimal.NewFromInt(maxDcaTrades)).Mul(t.tradePart)
	if err := t.trader.Sell(amount); err != nil {
		return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
	}

	t.tradePart = decimal.Zero

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
