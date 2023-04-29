package main

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/martinlindhe/notify"
	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/detector"
	binancepricer "github.com/vadimInshakov/marti/services/pricer"
	binancetrader "github.com/vadimInshakov/marti/services/trader"
	binancewallet "github.com/vadimInshakov/marti/services/wallet"
	"github.com/vadimInshakov/marti/services/windowfinder"
	"go.uber.org/zap"
	"math"
	"math/big"
	"time"
)

func binanceTradeServiceCreator(logger *zap.Logger, wf windowfinder.WindowFinder, binanceClient *binance.Client, pair entity.Pair, usebalance float64) (func(context.Context) error, error) {
	balancesStore := make(map[string]*big.Float)
	memwallet := binancewallet.NewInMemWallet(&binancewallet.InmemTx{Balances: make(map[string]*big.Float)}, balancesStore)
	pricer := binancepricer.NewPricer(binanceClient)

	buyprice, window, err := wf.GetBuyPriceAndWindow()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find window for %s", pair.String())
	}

	detect, err := detector.NewDetector(binanceClient, usebalance, pair, buyprice, window)
	if err != nil {
		panic(err)
	}

	trader, err := binancetrader.NewTrader(binanceClient, pair)
	if err != nil {
		panic(err)
	}

	res, err := binanceClient.NewGetAccountService().Do(context.Background())
	if err != nil {
		return nil, err
	}

	var balanceFirstCurrency *big.Float
	var balanceSecondCurrency *big.Float
	for _, b := range res.Balances {
		if b.Asset == pair.To {
			balanceSecondCurrency, _ = new(big.Float).SetString(b.Free)
		}
		if b.Asset == pair.From {
			balanceFirstCurrency, _ = new(big.Float).SetString(b.Free)
		}
	}

	price, err := pricer.GetPrice(pair)
	if err != nil {
		return nil, err
	}

	percent := new(big.Float).Quo(big.NewFloat(usebalance), big.NewFloat(100))

	balanceSecondCurrency.Quo(balanceSecondCurrency, price)
	balanceSecondCurrency.Mul(balanceSecondCurrency, percent)

	f, _ := balanceSecondCurrency.Float64()
	var amount *big.Float
	amount = new(big.Float).SetFloat64(math.Floor(f*10000) / 10000) // round down to 0,000x

	if detect.LastAction() == entity.ActionBuy {
		f, _ := balanceFirstCurrency.Float64()
		amount = new(big.Float).SetFloat64(math.Floor(f*10000) / 10000)
	}

	logger.Info("start",
		zap.String("buyprice", buyprice.String()),
		zap.String("window", window.String()),
		zap.String("use "+pair.From, amount.String()))

	ts := services.NewTradeService(pair, amount, memwallet, pricer, detect, trader)

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
