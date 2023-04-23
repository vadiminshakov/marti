package services

import (
	"math/big"

	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services/wallet"
)

type Detector interface {
	NeedAction(price *big.Float) (entity.Action, error)
}

type Pricer interface {
	GetPrice(pair entity.Pair) (*big.Float, error)
}

type Trader interface {
	Buy(amount *big.Float) error
	Sell(amount *big.Float) error
}

type TradeService struct {
	pair     entity.Pair
	amount   *big.Float
	wallet   wallet.Wallet
	pricer   Pricer
	detector Detector
	trader   Trader
}

func NewTradeService(pair entity.Pair, amount *big.Float, wallet wallet.Wallet, pricer Pricer, detector Detector, trader Trader) *TradeService {
	return &TradeService{pair, amount, wallet, pricer, detector, trader}
}

func (t *TradeService) Trade() (*entity.TradeEvent, error) {
	price, err := t.pricer.GetPrice(t.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", t.pair)
	}

	act, err := t.detector.NeedAction(price)
	if err != nil {
		return nil, errors.Wrapf(err, "detector failed for pair %s", t.pair)
	}

	var tradeEvent *entity.TradeEvent
	switch act {
	case entity.ActionBuy:
		tx := t.wallet.BeginTx()

		if err := t.wallet.Sub(tx, t.pair.To, (&big.Float{}).Mul(t.amount, price)); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to sub %s for %s", t.amount.Mul(t.amount, price), t.pair.To)
		}
		if err := t.wallet.Add(tx, t.pair.From, t.amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to add %s for %s", t.amount, t.pair.From)
		}

		if err := t.trader.Buy(t.amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}
			return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair)
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "failed to commit")
		}

		tradeEvent = &entity.TradeEvent{
			Action: entity.ActionBuy,
			Amount: t.amount,
			Pair:   t.pair,
		}
	case entity.ActionSell:
		tx := t.wallet.BeginTx()

		if err := t.wallet.Sub(tx, t.pair.From, t.amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to sub %s for %s", t.amount, t.pair.From)
		}
		if err := t.wallet.Add(tx, t.pair.To, (&big.Float{}).Mul(t.amount, price)); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to add %s for %s", t.amount.Mul(t.amount, price), t.pair.To)
		}

		if err := t.trader.Sell(t.amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}
			return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "failed to commit")
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
