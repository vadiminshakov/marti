package services

import (
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
)

type Detector interface {
	NeedAction(price decimal.Decimal) (entity.Action, error)
	LastAction() entity.Action
}

type Pricer interface {
	GetPrice(pair entity.Pair) (decimal.Decimal, error)
}

type Trader interface {
	Buy(amount decimal.Decimal) error
	Sell(amount decimal.Decimal) error
}

type TradeService struct {
	pair     entity.Pair
	amount   decimal.Decimal
	pricer   Pricer
	detector Detector
	trader   Trader
}

func NewTradeService(pair entity.Pair, amount decimal.Decimal, pricer Pricer, detector Detector, trader Trader) *TradeService {
	return &TradeService{pair, amount, pricer, detector, trader}
}

func (t *TradeService) Trade() (*entity.TradeEvent, error) {
	price, err := t.pricer.GetPrice(t.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", t.pair.String())
	}

	act, err := t.detector.NeedAction(price)
	if err != nil {
		return nil, errors.Wrapf(err, "detector failed for pair %s", t.pair.String())
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
		}
	case entity.ActionSell:
		if err := t.trader.Sell(t.amount); err != nil {
			return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
		}

		tradeEvent = &entity.TradeEvent{
			Action: entity.ActionSell,
			Amount: t.amount,
			Pair:   t.pair,
		}
	case entity.ActionNull:
	}

	return tradeEvent, nil
}
