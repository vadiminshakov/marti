package main

import (
	"context"
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

var buypoint, window = big.NewFloat(29284), big.NewFloat(100)

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
	buypointFlag := flag.Float64("buypoint", 0, "average price, example: 20449")
	windowFlag := flag.Float64("window", 0, "price window, example: 100")
	flag.Parse()

	if *buypointFlag < 10 {
		logger.Fatal("incorrect --buypoint")
	}
	if *windowFlag < 10 {
		logger.Fatal("incorrect --buypoint")
	}

	buypoint, window := big.NewFloat(*buypointFlag), big.NewFloat(*windowFlag)
	pairElements := strings.Split(*pairFlag, "_")
	if len(pairElements) != 2 {
		logger.Fatal("invalid --par provided", zap.String("--pair", *pairFlag))
	}
	pair := entity.Pair{From: pairElements[0], To: pairElements[1]}

	g := new(errgroup.Group)

	g.Go(func() error {
		return binanceTradeServiceCreator(logger, binanceClient, pair, buypoint, window)(context.Background())
	})

	if err := g.Wait(); err != nil {
		logger.Error(err.Error())
	}
}

func binanceTradeServiceCreator(logger *zap.Logger, binanceClient *binance.Client, pair entity.Pair, buypoint, window *big.Float) func(context.Context) error {
	balancesStore := make(map[string]*big.Float)
	memwallet := binancewallet.NewInMemWallet(&binancewallet.InmemTx{Balances: make(map[string]*big.Float)}, balancesStore)
	pricer := binancepricer.NewPricer(binanceClient)

	detect, err := detector.NewDetector(binanceClient, pair, buypoint, window)
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
	}
}
