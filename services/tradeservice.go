package services

import (
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"go.uber.org/zap"
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

// TradeService makes trades for specific trade pair.
type TradeService struct {
	pair            entity.Pair
	amount          decimal.Decimal
	pricer          Pricer
	detector        Detector
	trader          Trader
	anomalyDetector AnomalyDetector
	l               *zap.Logger
}

// NewTradeService creates new TradeService instance.
func NewTradeService(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, pricer Pricer, detector Detector,
	trader Trader, anomalyDetector AnomalyDetector) *TradeService {
	return &TradeService{pair, amount, pricer, detector, trader, anomalyDetector, l}
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
		if err := t.trader.Buy(t.amount); err != nil {
			return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair.String())
		}

		tradeEvent = &entity.TradeEvent{
			Action: entity.ActionBuy,
			Amount: t.amount,
			Pair:   t.pair,
			Price:  price,
		}
	case entity.ActionSell:
		if err := t.trader.Sell(t.amount); err != nil {
			return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
		}

		tradeEvent = &entity.TradeEvent{
			Action: entity.ActionSell,
			Amount: t.amount,
			Pair:   t.pair,
			Price:  price,
		}
	case entity.ActionNull:
	}

	return tradeEvent, nil
}
