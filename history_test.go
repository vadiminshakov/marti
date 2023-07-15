package main

import (
	"encoding/csv"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadimInshakov/marti/entity"
	"github.com/vadimInshakov/marti/services"
	"github.com/vadimInshakov/marti/services/anomalydetector"
	"github.com/vadimInshakov/marti/services/windowfinder"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"testing"
	"time"
)

const (
	file                     = "data.csv" // file with data for test
	btcBalanceInWallet       = "1"        // BTC balance in wallet
	usdtBalanceInWallet      = "0"
	klinesize                = "1h" // klinesize for test
	rebalanceHours           = 3
	klinesframe         uint = 110 // klines*klinesframe = hours before stats recount
	minWindowUSDT            = 128 // ok window due to binance commissions
)

var dataHoursAgo int

func TestProfit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping historical test in short mode.")
	}
	t.Run("1 year", func(t *testing.T) {
		dataHoursAgo = 8760 // 1 year
		require.NoError(t, botrun(zap.InfoLevel))
	})

	t.Run("2 years", func(t *testing.T) {
		dataHoursAgo = 17520 // 2 years
		require.NoError(t, botrun(zap.InfoLevel))
	})
}

func botrun(loglvl zapcore.Level) error {
	l, err := zap.NewProduction()
	if err != nil {
		return err
	}
	log := l.Sugar()
	lvl := zap.NewAtomicLevel()
	lvl.SetLevel(loglvl)

	pair := &entity.Pair{
		From: "BTC",
		To:   "USDT",
	}

	prices, klines, rmFn, err := prepareData(file, pair)
	if err != nil {
		return err
	}

	defer rmFn()

	var lastaction entity.Action = entity.ActionBuy
	balanceBTC, _ := decimal.NewFromString(btcBalanceInWallet)
	balanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	trader, tsFactory := createTradeServiceFactory(l, pair, prices, balanceBTC, balanceUSDT)
	kline := <-klines
	ts, _ := tsFactory([]*entity.Kline{&kline}, lastaction)
	if err != nil {
		return err
	}

	var counter uint
	var kl []*entity.Kline
	for {
		counter++
		if len(prices) == 0 || len(klines) == 0 {
			break
		}

		kline := <-klines
		if len(kl) >= int(klinesframe) {
			kl = kl[1:]
		}
		kl = append(kl, &kline)

		if decimal.NewFromInt(int64(counter)).Equal(decimal.NewFromInt(rebalanceHours)) || ts == nil {
			counter = 0
			// recreate trade service for 'klinesframe' day
			ts, err = tsFactory(kl, lastaction)
			if err != nil {
				log.Debug("skip kline because insufficient volatility")
				continue
			}
		}

		te, err := ts.Trade()
		if err != nil {
			return err
		}

		if te == nil {
			<-prices // skip price that not readed by trader
			continue
		}
		if te.Action != entity.ActionNull {
			lastaction = te.Action
		}

		log.Debug(te.String())
	}

	log.Infof("Deals: %d", trader.dealsCount)
	log.Infof("Total balance of %s is %s (was %s)", pair.From, trader.balance1.String(), balanceBTC.String())
	log.Infof("Total balance of %s is %s (was %s)", pair.To, trader.balance2.String(), trader.firstbalance2)
	log.Infof("Total fee is %s", trader.fee.String())

	var total decimal.Decimal
	if trader.balance1.GreaterThan(decimal.NewFromInt(0)) {
		total = trader.balance2.Sub(trader.fee)
	} else {
		total = trader.balance2.Sub(trader.firstbalance2).Sub(trader.fee)
	}
	log.Infof("Total profit is %s %s", total.String(), pair.To)

	return nil
}

func prepareData(file string, pair *entity.Pair) (prices chan decimal.Decimal, klines chan entity.Kline, removeFile func(), _ error) {
	collect, err := dataColletorFactory(file, pair)
	if err != nil {
		return nil, nil, nil, err
	}

	removeFile = func() {
		os.Remove(file)
	}

	intervalHours := 100
	for collectFromHours := dataHoursAgo; collectFromHours > 0; collectFromHours -= intervalHours {
		if err = collect(collectFromHours, intervalHours, klinesize); err != nil {
			return nil, nil, nil, err
		}
	}

	prices, klines = makePriceChFromCsv(file)

	return
}

func createTradeServiceFactory(logger *zap.Logger, pair *entity.Pair, prices chan decimal.Decimal,
	balanceBTC, balanceUSDT decimal.Decimal) (
	*traderCsv, func(klines []*entity.Kline, lastaction entity.Action) (*services.TradeService, error)) {
	pricer := &pricerCsv{
		pricesCh: prices,
	}

	trader := &traderCsv{
		pair:     pair,
		balance1: balanceBTC,
		balance2: balanceUSDT,
		pricesCh: prices,
	}

	anomdetector := anomalydetector.NewAnomalyDetector(*pair, 30, decimal.NewFromInt(10))

	return trader, func(klines []*entity.Kline, lastaction entity.Action) (*services.TradeService, error) {
		buyprice, window, err := windowfinder.CalcBuyPriceAndWindow(klines, decimal.NewFromInt(minWindowUSDT))
		if err != nil {
			return nil, err
		}
		return services.NewTradeService(logger, *pair, balanceBTC, pricer, &detectorCsv{
			lastaction: lastaction,
			buypoint:   buyprice,
			window:     window,
		},
			trader, anomdetector), nil
	}
}

func makePriceChFromCsv(filePath string) (chan decimal.Decimal, chan entity.Kline) {
	prices := make(chan decimal.Decimal, 1000)
	var klines chan entity.Kline

	go func() {
		pricescsv, kchan := readCsv(filePath)
		klines = kchan
		for _, price := range pricescsv {
			prices <- price // for pricer
			prices <- price // for trader
		}
	}()
	time.Sleep(300 * time.Millisecond)

	return prices, klines
}

func readCsv(filePath string) ([]decimal.Decimal, chan entity.Kline) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Fatal("Unable to read input file "+filePath, err)
	}
	defer f.Close()

	csvReader := csv.NewReader(f)
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Fatal("Unable to parse file as CSV for "+filePath, err)
	}

	prices := make([]decimal.Decimal, 0, len(records))
	klines := make(chan entity.Kline, len(records))

	for _, record := range records {
		priceOpen, _ := decimal.NewFromString(record[0])
		priceHigh, _ := decimal.NewFromString(record[1])
		priceLow, _ := decimal.NewFromString(record[2])
		priceClose, _ := decimal.NewFromString(record[3])

		price := priceHigh.Add(priceLow).Div(decimal.NewFromInt(2))
		prices = append(prices, price)
		klines <- entity.Kline{
			Open:  priceOpen,
			Close: priceClose,
		}
	}

	return prices, klines
}
