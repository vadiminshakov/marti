package services

import (
	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/entity"
	"math/big"
)

var amount *big.Float = big.NewFloat(0.000002)

func run(pair entity.Pair, pricer Pricer, detector Detector, trader Trader) error {
	for {
		price, err := pricer.GetPrice(pair)
		if err != nil {
			return errors.Wrapf(err, "pricer failed for pair %s", pair)
		}

		act, err := detector.NeedAction(pair, price)
		if err != nil {
			return errors.Wrapf(err, "detector failed for pair %s", pair)
		}

		switch act {
		case entity.ActionBuy:
			if err := trader.Buy(pair, amount); err != nil {
				return errors.Wrapf(err, "trader buy failed for pair %s", pair)
			}
		case entity.ActionSell:
			if err := trader.Sell(pair, amount); err != nil {
				return errors.Wrapf(err, "trader sell failed for pair %s", pair)
			}
		case entity.ActionNull:
		}
	}
}
