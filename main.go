package main

import (
	"context"
	"errors"
	"flag"
	"math/big"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/martinlindhe/notify"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/detector"
	binancepricer "github.com/vadimInshakov/marti/services/pricer/binance"
	binancetrader "github.com/vadimInshakov/marti/services/trader/binance"
	binancewallet "github.com/vadimInshakov/marti/services/wallet/binance"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	binanceClient := binance.NewClient(apikey, secretkey)

	// TODO make window counter and current price detector
	// make constructor to recreate trader service from new buypoint
	// make anomaly detector

	//apikey := os.Getenv("APIKEY")
	//if len(apikey) == 0 {
	//	logger.Fatal("APIKEY env is not set")
	//}
	//require.NotEmpty(t, apikey, "APIKEY env is not set")
	//secretkey := os.Getenv("SECRETKEY")
	//if len(apikey) == 0 {
	//	logger.Fatal("SECRETKEY env is not set")
	//}

	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	pairElements := strings.Split(*pairFlag, "_")
	if len(pairElements) != 2 {
		logger.Fatal("invalid --par provided", zap.String("--pair", *pairFlag))
	}
	pair := entity.Pair{From: pairElements[0], To: pairElements[1]}

	g := new(errgroup.Group)

	g.Go(func() error {
		for {
			ctx, _ := context.WithTimeout(context.Background(), 7*time.Second)
			fn, err := binanceTradeServiceCreator(logger, binanceClient, pair)
			if err != nil {
				return err
			}

			if err := fn(ctx); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.Info("recreate instance")
					continue
				}
				return err
			}
		}
	})

	if err := g.Wait(); err != nil {
		logger.Error(err.Error())
	}
}

func binanceTradeServiceCreator(logger *zap.Logger, binanceClient *binance.Client, pair entity.Pair) (func(context.Context) error, error) {
	balancesStore := make(map[string]*big.Float)
	memwallet := binancewallet.NewInMemWallet(&binancewallet.InmemTx{Balances: make(map[string]*big.Float)}, balancesStore)
	pricer := binancepricer.NewPricer(binanceClient)

	buyprice, window, err := getBuyPriceAndWindow(binanceClient, pair)

	logger.Info("start with price and window",
		zap.String("buyprice", buyprice.String()),
		zap.String("window", window.String()))

	detect, err := detector.NewDetector(binanceClient, pair, buyprice, window)
	if err != nil {
		panic(err)
	}

	trader, err := binancetrader.NewTrader(binanceClient, pair)
	if err != nil {
		panic(err)
	}

	ts := services.NewTradeService(pair, memwallet, pricer, detect, trader)

	return func(ctx context.Context) error {
		t := time.NewTicker(4 * time.Second)
		for ctx.Err() == nil {
			select {
			case <-t.C:
				te, err := ts.Trade()
				if err != nil {
					notify.Alert("marti", "alert", err.Error(), "")
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

func getBuyPriceAndWindow(binanceClient *binance.Client, pair entity.Pair) (*big.Float, *big.Float, error) {
	klines, err := binanceClient.NewKlinesService().Symbol(pair.Symbol()).
		Interval("2h").Do(context.Background())
	if err != nil {
		return nil, nil, err
	}

	cumulativeBuyPrice, cumulativeWindow := big.NewFloat(0), big.NewFloat(0)
	for _, k := range klines {
		klineOpen, _ := new(big.Float).SetString(k.Open)
		klineClose, _ := new(big.Float).SetString(k.Close)

		klinesum := new(big.Float).Add(klineOpen, klineClose)
		buyprice := klinesum.Quo(klinesum, big.NewFloat(2))
		cumulativeBuyPrice.Add(cumulativeBuyPrice, buyprice)

		klinewindow := new(big.Float).Abs(new(big.Float).Sub(klineOpen, klineClose))
		cumulativeWindow.Add(cumulativeWindow, klinewindow)
	}
	cumulativeBuyPrice.Quo(cumulativeBuyPrice, big.NewFloat(float64(len(klines))))
	cumulativeWindow.Quo(cumulativeWindow, big.NewFloat(float64(len(klines))))

	return cumulativeBuyPrice, cumulativeWindow, nil
}
