package main

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/martinlindhe/notify"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/anomalydetector"
	"github.com/vadimInshakov/marti/services/detector"
	binancepricer "github.com/vadimInshakov/marti/services/pricer"
	binancetrader "github.com/vadimInshakov/marti/services/trader"
	"github.com/vadimInshakov/marti/services/windowfinder"
	"go.uber.org/zap"
	"time"
)

// binanceTradeServiceCreator creates trade service for binance exchange.
func binanceTradeServiceCreator(logger *zap.Logger, wf windowfinder.WindowFinder,
	binanceClient *binance.Client, pair entity.Pair, usebalance decimal.Decimal,
	pollPricesInterval time.Duration) (func(context.Context) error, error) {
	pricer := binancepricer.NewPricer(binanceClient)

	buyprice, window, err := wf.GetBuyPriceAndWindow()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find window for %s", pair.String())
	}

	detect, err := detector.NewDetector(binanceClient, usebalance, pair, buyprice, window)
	if err != nil {
		return nil, err
	}

	trader, err := binancetrader.NewTrader(binanceClient, pair)
	if err != nil {
		return nil, err
	}

	res, err := binanceClient.NewGetAccountService().Do(context.Background())
	if err != nil {
		return nil, err
	}

	var balanceFirstCurrency decimal.Decimal
	var balanceSecondCurrency decimal.Decimal
	for _, b := range res.Balances {
		if b.Asset == pair.To {
			balanceSecondCurrency, _ = decimal.NewFromString(b.Free)
		}
		if b.Asset == pair.From {
			balanceFirstCurrency, _ = decimal.NewFromString(b.Free)
		}
	}

	price, err := pricer.GetPrice(pair)
	if err != nil {
		return nil, err
	}

	percent := usebalance.Div(decimal.NewFromInt(100))

	balanceSecondCurrency = balanceSecondCurrency.Div(price)
	balanceSecondCurrency = balanceSecondCurrency.Mul(percent)

	balanceSecondCurrency = balanceSecondCurrency.RoundFloor(5) // round down to 0,000x

	amount := balanceSecondCurrency
	if detect.LastAction() == entity.ActionBuy {
		balanceFirstCurrency = balanceFirstCurrency.RoundFloor(5)
		amount = balanceFirstCurrency
	}

	logger.Info("start",
		zap.String("buyprice", buyprice.String()),
		zap.String("window", window.String()),
		zap.String("use "+pair.From, amount.String()))

	anomdetector := anomalydetector.NewAnomalyDetector(pair, 30, decimal.NewFromInt(3))

	ts := services.NewTradeService(logger, pair, amount, pricer, detect, trader, anomdetector)

	return func(ctx context.Context) error {
		t := time.NewTicker(pollPricesInterval)
		for ctx.Err() == nil {
			select {
			case <-t.C:
				te, err := ts.Trade()
				if err != nil {
					notify.Alert("marti", "alert", err.Error(), "")
					t.Stop()
					return err
				}
				if te != nil {
					logger.Info(te.String())
					notify.Alert("marti", "alert", te.String(), "")
				}
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			}
		}

		return ctx.Err()
	}, nil
}
