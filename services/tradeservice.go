package services

import (
	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services/wallet"
	"math/big"
)

var amount *big.Float = big.NewFloat(1)

type Detector interface {
	NeedAction(pair entity.Pair, price *big.Float) (entity.Action, error)
}

type Pricer interface {
	GetPrice(pair entity.Pair) (*big.Float, error)
}

type Trader interface {
	Buy(pair entity.Pair, amount *big.Float) error
	Sell(pair entity.Pair, amount *big.Float) error
}

type Wallet interface {
	BeginTx() wallet.Tx
	Add(tx wallet.Tx, currency string, amount *big.Float) error
	Sub(tx wallet.Tx, currency string, amount *big.Float) error

	Balance(currency string) (*big.Float, error)
}

type TradeService struct {
	pair     entity.Pair
	wallet   Wallet
	pricer   Pricer
	detector Detector
	trader   Trader
}

func NewTradeService(pair entity.Pair, wallet Wallet, pricer Pricer, detector Detector, trader Trader) *TradeService {
	return &TradeService{pair, wallet, pricer, detector, trader}
}

type TradeEvent struct {
	Action entity.Action
	Amount *big.Float
}

func (t *TradeService) Trade() (*TradeEvent, error) {
	price, err := t.pricer.GetPrice(t.pair)
	if err != nil {
		return nil, errors.Wrapf(err, "pricer failed for pair %s", t.pair)
	}

	act, err := t.detector.NeedAction(t.pair, price)
	if err != nil {
		return nil, errors.Wrapf(err, "detector failed for pair %s", t.pair)
	}

	var tradeEvent *TradeEvent
	switch act {
	case entity.ActionBuy:
		tx := t.wallet.BeginTx()

		if err := t.wallet.Sub(tx, t.pair.To, amount.Mul(amount, price)); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to sub %s for %s", amount.Mul(amount, price), t.pair.To)
		}
		if err := t.wallet.Add(tx, t.pair.From, amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to add %s for %s", amount, t.pair.From)
		}

		if err := t.trader.Buy(t.pair, amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}
			return nil, errors.Wrapf(err, "trader buy failed for pair %s", t.pair)
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "failed to commit")
		}

		tradeEvent = &TradeEvent{
			Action: entity.ActionBuy,
			Amount: amount,
		}
	case entity.ActionSell:
		tx := t.wallet.BeginTx()

		if err := t.wallet.Sub(tx, t.pair.From, amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to sub %s for %s", amount, t.pair.From)
		}
		if err := t.wallet.Add(tx, t.pair.To, amount.Mul(amount, price)); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}

			return nil, errors.Wrapf(err, "wallet failed to add %s for %s", amount.Mul(amount, price), t.pair.To)
		}

		if err := t.trader.Sell(t.pair, amount); err != nil {
			if err := tx.Rollback(); err != nil {
				return nil, errors.Wrap(err, "failed to rollback")
			}
			return nil, errors.Wrapf(err, "trader sell failed for pair %s", t.pair)
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "failed to commit")
		}

		tradeEvent = &TradeEvent{
			Action: entity.ActionSell,
			Amount: amount,
		}
	case entity.ActionNull:
	}

	return tradeEvent, nil
}
