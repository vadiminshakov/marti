package main

import (
	"encoding/csv"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/entity"
	"github.com/vadiminshakov/marti/services"
	"github.com/vadiminshakov/marti/services/anomalydetector"
	"github.com/vadiminshakov/marti/services/channel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"testing"
	"time"
)

const (
	dataFile            = "data.csv" // file with data for test
	btcBalanceInWallet  = "0.2"      // BTC balance in wallet
	usdtBalanceInWallet = "0"
	klineSize           = "4h" // klinesize for test
	rebalanceHours      = 30
	klineFrame          = 180 // number of klines in frame for stat analysis (channel & buy price detection)
	minWindowUSDT       = 180 // ok window due to binance commissions
)

var dataHoursAgo int

func TestProfit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping historical test in short mode.")
	}

	t.Run("1 year", func(t *testing.T) {
		dataHoursAgo = 8760
		require.NoError(t, runBot(zap.InfoLevel))
	})

	//t.Run("2 years", func(t *testing.T) {
	//	dataHoursAgo = 17520
	//	require.NoError(t, runBot(zap.InfoLevel))
	//})
}

func runBot(logLevel zapcore.Level) error {
	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	defer logger.Sync()
	log := logger.Sugar()

	pair := &entity.Pair{From: "BTC", To: "USDT"}
	prices, klines, cleanup, err := prepareData(dataFile, pair)
	if err != nil {
		return err
	}
	defer cleanup()

	balanceBTC, _ := decimal.NewFromString(btcBalanceInWallet)
	balanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	trader, tradeServiceFactory := createTradeServiceFactory(logger, pair, prices, balanceBTC, balanceUSDT)

	kline := <-klines

	tradeService, err := tradeServiceFactory([]*entity.Kline{&kline}, entity.ActionBuy)
	if err != nil {
	}

	var klineBuffer []*entity.Kline
	var counter uint
	lastAction := entity.ActionBuy

	for {
		if len(prices) == 0 || len(klines) == 0 {
			break
		}
		counter++

		kline = <-klines

		if len(klineBuffer) >= klineFrame {
			klineBuffer = klineBuffer[1:]
		}
		klineBuffer = append(klineBuffer, &kline)

		if counter >= rebalanceHours || tradeService == nil {
			counter = 0
			tradeService, err = tradeServiceFactory(klineBuffer, lastAction)
			if tradeService == nil {
				continue
			}

			if err != nil {
				log.Debug("Skipping kline due to insufficient volatility")
				continue
			}
		}

		tradeEvent, err := tradeService.Trade()
		if err != nil {
			return err
		}

		if tradeEvent == nil {
			<-prices
			continue
		}

		if tradeEvent.Action != entity.ActionNull {
			lastAction = tradeEvent.Action
		}
	}

	summarizeResults(log, trader, pair, balanceBTC)
	return nil
}

func summarizeResults(log *zap.SugaredLogger, trader *traderCsv, pair *entity.Pair, initialBalanceBTC decimal.Decimal) {
	log.Infof("Deals: %d", trader.dealsCount)
	log.Infof("Total balance of %s: %s (was %s)", pair.From, trader.balance1.String(), initialBalanceBTC.String())
	log.Infof("Total balance of %s: %s (was %s)", pair.To, trader.balance2.String(), trader.firstbalance2)
	log.Infof("Total fee: %s", trader.fee.String())

	totalProfit := calculateTotalProfit(trader)
	log.Infof("Total profit: %s %s", totalProfit.String(), pair.To)
}

func calculateTotalProfit(trader *traderCsv) decimal.Decimal {
	if trader.balance1.GreaterThan(decimal.Zero) {
		return trader.balance2.Sub(trader.fee)
	}
	return trader.balance2.Sub(trader.firstbalance2).Sub(trader.fee)
}

func prepareData(filePath string, pair *entity.Pair) (chan decimal.Decimal, chan entity.Kline, func(), error) {
	collectData, err := dataColletorFactory(filePath, pair)
	if err != nil {
		return nil, nil, nil, err
	}

	cleanup := func() { os.Remove(filePath) }
	interval := 100
	for hours := dataHoursAgo; hours > 0; hours -= interval {
		if err := collectData(hours, interval, klineSize); err != nil {
			return nil, nil, cleanup, err
		}
	}

	prices, klines := loadPricesFromCSV(filePath)
	return prices, klines, cleanup, nil
}

func createTradeServiceFactory(logger *zap.Logger, pair *entity.Pair, prices chan decimal.Decimal, balanceBTC, balanceUSDT decimal.Decimal) (*traderCsv, func([]*entity.Kline, entity.Action) (*services.TradeService, error)) {
	pricer := &pricerCsv{pricesCh: prices}
	trader := &traderCsv{pair: pair, balance1: balanceBTC, balance2: balanceUSDT, pricesCh: prices}
	anomDetector := anomalydetector.NewAnomalyDetector(*pair, 30, decimal.NewFromInt(10))

	return trader, func(klines []*entity.Kline, lastAction entity.Action) (*services.TradeService, error) {
		buyPrice, window, err := channel.CalcBuyPriceAndWindow(klines, decimal.NewFromInt(minWindowUSDT))
		if err != nil {
			return nil, err
		}

		ts := services.NewTradeService(logger, *pair, balanceBTC, pricer, &detectorCsv{
			lastaction: lastAction,
			buypoint:   buyPrice,
			window:     window,
		}, trader, anomDetector)

		return ts, nil
	}
}

func loadPricesFromCSV(filePath string) (chan decimal.Decimal, chan entity.Kline) {
	prices := make(chan decimal.Decimal, 3000)
	klines := make(chan entity.Kline, 1000)

	priceData, klineData := parseCSV(filePath)
	go func() {
		for _, price := range priceData {
			prices <- price
			prices <- price
		}
	}()

	go func() {
		for _, kline := range klineData {
			klines <- kline
		}
	}()

	time.Sleep(300 * time.Millisecond) // Ensure goroutine fills channels
	return prices, klines
}

func parseCSV(filePath string) ([]decimal.Decimal, []entity.Kline) {
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Unable to read input file %s: %v", filePath, err)
	}
	defer file.Close()

	csvReader := csv.NewReader(file)
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Fatalf("Unable to parse file as CSV %s: %v", filePath, err)
	}

	prices := make([]decimal.Decimal, 0, len(records))
	klines := make([]entity.Kline, 0, len(records))
	for _, record := range records {
		open, _ := decimal.NewFromString(record[0])
		high, _ := decimal.NewFromString(record[1])
		low, _ := decimal.NewFromString(record[2])
		closePrice, _ := decimal.NewFromString(record[3])

		price := high.Add(low).Div(decimal.NewFromInt(2))
		prices = append(prices, price)
		klines = append(klines, entity.Kline{Open: open, Close: closePrice})
	}

	return prices, klines
}
