package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/vadimInshakov/marti/services/windowfinder"

	"github.com/adshao/go-binance/v2"
	"github.com/martinlindhe/notify"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/detector"
	binancepricer "github.com/vadimInshakov/marti/services/pricer"
	binancetrader "github.com/vadimInshakov/marti/services/trader"
	binancewallet "github.com/vadimInshakov/marti/services/wallet"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	recreateInterval   = 13 * time.Hour
	pollPricesInterval = 5 * time.Second
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
	klineSize := flag.String("klinesize", "1h", "kline size, example: 1h")
	usebalance := flag.Float64("usebalance", 100, "percent of balance usage, for example 90 means 90%")
	koeff := flag.Float64("koeff", 1, "koeff for multiply buyprice and window found, example: 0.98")
	flag.Parse()

	_, err := time.ParseDuration(*klineSize)
	if err != nil {
		logger.Fatal("invalid --klinesize provided", zap.String("--klinesize", *klineSize))
	}

	if *usebalance < 0 || *usebalance > 100 {
		logger.Fatal("invalid --usebalance provided", zap.Float64("--usebalance", *usebalance))
	}

	pairElements := strings.Split(*pairFlag, "_")
	if len(pairElements) != 2 {
		logger.Fatal("invalid --par provided", zap.String("--pair", *pairFlag))
	}
	pair := entity.Pair{From: pairElements[0], To: pairElements[1]}

	g := new(errgroup.Group)

	g.Go(func() error {
		for {
			ctx, _ := context.WithTimeout(context.Background(), recreateInterval)
			wf := windowfinder.NewBinanceWindowFinder(binanceClient, pair, *klineSize, *koeff)
			fn, err := binanceTradeServiceCreator(logger, wf, binanceClient, pair, *usebalance)
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

type WindowFinder interface {
	GetBuyPriceAndWindow() (*big.Float, *big.Float, error)
}

func binanceTradeServiceCreator(logger *zap.Logger, wf WindowFinder, binanceClient *binance.Client, pair entity.Pair, usebalance float64) (func(context.Context) error, error) {
	balancesStore := make(map[string]*big.Float)
	memwallet := binancewallet.NewInMemWallet(&binancewallet.InmemTx{Balances: make(map[string]*big.Float)}, balancesStore)
	pricer := binancepricer.NewPricer(binanceClient)

	buyprice, window, err := wf.GetBuyPriceAndWindow()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find window for %s", pair.String())
	}

	detect, err := detector.NewDetector(binanceClient, pair, buyprice, window)
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

	var balanceSecondCurrency *big.Float
	for _, b := range res.Balances {
		if b.Asset == pair.To {
			balanceSecondCurrency, _ = new(big.Float).SetString(b.Free)
			break
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
	balanceSecondCurrency, _ = new(big.Float).SetString(fmt.Sprintf("%0.4f", f))

	logger.Info("start",
		zap.String("buyprice", buyprice.String()),
		zap.String("window", window.String()),
		zap.String("use % of "+pair.From+" balance", balanceSecondCurrency.String()))

	ts := services.NewTradeService(pair, balanceSecondCurrency, memwallet, pricer, detect, trader)

	return func(ctx context.Context) error {
		t := time.NewTicker(pollPricesInterval)
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
