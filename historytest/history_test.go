package main

import (
	"encoding/csv"
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/entity"

	"github.com/vadiminshakov/marti/internal/services"
	"github.com/vadiminshakov/marti/internal/services/anomalydetector"
	"github.com/vadiminshakov/marti/internal/services/channel"
	"go.uber.org/zap"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	dataFile            = "data.csv" // file with data for test
	btcBalanceInWallet  = "0.5"      // BTC balance in wallet
	usdtBalanceInWallet = "0"
	klineSize           = "1h" // klinesize for test
	rebalanceHours      = 56
	klineFrame          = 280 // number of klines in frame for stat analysis (channel & buy price detection)
)

var dataHoursAgo int

func TestProfit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping historical test in short mode.")
	}

	t.Run("1 year", func(t *testing.T) {
		dataHoursAgo = 8760
		require.NoError(t, runBot())
	})

	t.Run("2 years", func(t *testing.T) {
		dataHoursAgo = 17520
		require.NoError(t, runBot())
	})
}

func runBot() error {
	dur, err := time.ParseDuration(klineSize)
	if err != nil {
		return fmt.Errorf("unable to parse kline size: %w", err)
	}

	klineSizeHours := float64(dur.Hours())

	logger, err := zap.NewProduction()
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()
	log := logger.Sugar()

	pair := &entity.Pair{From: "BTC", To: "USDT"}

	prices, klines, cleanup, err := prepareData(dataFile, pair)
	if err != nil {
		return fmt.Errorf("failed to prepare data: %w", err)
	}
	defer cleanup()

	balanceBTC, _ := decimal.NewFromString(btcBalanceInWallet)
	balanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	trader, tradeServiceFactory := createTradeServiceFactory(logger, pair, prices, balanceBTC, balanceUSDT)
	trader.Sell(balanceBTC)

	klineBuffer := make([]*entity.Kline, 0, klineFrame)
	var (
		counter      uint
		lastAction   = entity.ActionBuy
		lastPriceBTC = decimal.Zero
		tradeService *services.TradeService
	)

	for kline := range klines {
		counter++

		// maintain kline buffer size
		if len(klineBuffer) >= klineFrame {
			klineBuffer = klineBuffer[1:]
		}
		klineBuffer = append(klineBuffer, &kline)

		hoursSpent := calculateHoursSpent(counter, klineSizeHours)

		// rebalance or initialize trade service if necessary
		if hoursSpent >= rebalanceHours || tradeService == nil {
			counter = 0
			if tradeService != nil {
				if err = tradeService.Close(); err != nil {
					return fmt.Errorf("failed to close trade service: %w", err)
				}
			}

			tradeService, err = tradeServiceFactory(klineBuffer, lastAction)
			if err != nil {
				if !strings.Contains(err.Error(), "channel less than min") {
					log.Fatalf("err trade svc create %s", err)
				}

				continue
			}
		}

		// execute trade
		tradeEvent, err := tradeService.Trade()
		if err != nil {
			log.Debug(err)
		}

		if tradeEvent != nil && tradeEvent.Action != entity.ActionNull {
			lastAction = tradeEvent.Action
			lastPriceBTC = tradeEvent.Price
		}
	}

	trader.Sell(trader.balance1)

	summarizeResults(log, trader, pair, balanceBTC, lastPriceBTC)
	return nil
}

func calculateHoursSpent(counter uint, klineSizeHours float64) float64 {
	if klineSizeHours < 1 {
		return float64(counter) * klineSizeHours
	}
	return float64(counter)
}

func summarizeResults(log *zap.SugaredLogger, trader *traderCsv, pair *entity.Pair, initialBalanceBTC, lastPriceBTC decimal.Decimal) {
	log.Infof("Deals: %d", trader.dealsCount)
	log.Infof("Total balance of %s: %s (was %s)", pair.From, trader.balance1.StringFixed(5), initialBalanceBTC.StringFixed(5))
	log.Infof("Total balance of %s: %s (was %s)", pair.To, trader.balance2.StringFixed(5), decimal.Zero)
	log.Infof("Total fee: %s", trader.fee.StringFixed(5))

	totalProfit := calculateTotalProfit(trader)
	if trader.balance1.IsPositive() {
		totalProfit = totalProfit.Add(lastPriceBTC.Mul(trader.balance1))
	}

	log.Infof("Total profit: %s %s", totalProfit.StringFixed(2), pair.To)
	log.Infof("%s%% profit (you have %s USDT equivivalent at start)", totalProfit.Sub(trader.firstbalance2).
		Mul(decimal.NewFromInt(100)).Div(trader.firstbalance2).StringFixed(2),
		trader.firstbalance2.StringFixed(2))
}

func calculateTotalProfit(trader *traderCsv) decimal.Decimal {
	return trader.balance2.Sub(trader.fee)
}

func prepareData(filePath string, pair *entity.Pair) (chan decimal.Decimal, chan entity.Kline, func(), error) {
	collectData, err := dataColletorFactory(filePath, pair)
	if err != nil {
		return nil, nil, nil, err
	}

	cleanup := func() {
		os.Remove(filePath)
		os.RemoveAll("waldata")
	}

	interval := 1300
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
		buyPrice, window, err := channel.CalcBuyPriceAndChannel(klines)
		if err != nil {
			return nil, err
		}

		ts, err := services.NewTradeService(logger, *pair, balanceBTC, pricer, &detectorCsv{
			lastaction: lastAction,
			buypoint:   buyPrice,
			window:     window,
		}, trader, anomDetector)
		if err != nil {
			return nil, err
		}

		return ts, nil
	}
}

func loadPricesFromCSV(filePath string) (chan decimal.Decimal, chan entity.Kline) {
	prices := make(chan decimal.Decimal, 1000)
	klines := make(chan entity.Kline, 1000)

	priceData, klineData := parseCSV(filePath)
	go func() {
		for _, price := range priceData {
			prices <- price
			prices <- price
		}
		close(prices)
	}()

	go func() {
		for _, kline := range klineData {
			klines <- kline
		}
		close(klines)
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
