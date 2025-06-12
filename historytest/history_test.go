package historytest

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services"
	"go.uber.org/zap"
)

const (
	dataFile            = "data.csv" // file with data for test
	btcBalanceInWallet  = "0"
	usdtBalanceInWallet = "10000"
	klineSize           = "4h"
)

var dataHoursAgo int

func TestProfit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping historical test in short mode.")
	}

	// Тестирование с разными параметрами DCA
	testCases := []struct {
		name                    string
		duration                int
		maxDcaTrades            int
		dcaPercentThresholdBuy  float64
		dcaPercentThresholdSell float64
	}{
		{
			name:                    "1 year - Conservative buy",
			duration:                8760,
			maxDcaTrades:            2,
			dcaPercentThresholdBuy:  9.5,
			dcaPercentThresholdSell: 66,
		},
		{
			name:                    "2 years - Conservative buy",
			duration:                17520,
			maxDcaTrades:            3,
			dcaPercentThresholdBuy:  3.5,
			dcaPercentThresholdSell: 66,
		},
		{
			name:                    "1 year - Aggressive trades",
			duration:                8760,
			maxDcaTrades:            30,
			dcaPercentThresholdBuy:  0.8,
			dcaPercentThresholdSell: 2,
		},
		{
			name:                    "2 years - Aggressive trades",
			duration:                17520,
			maxDcaTrades:            30,
			dcaPercentThresholdBuy:  0.8,
			dcaPercentThresholdSell: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dataHoursAgo = tc.duration
			require.NoError(t, runBot(tc.maxDcaTrades, tc.dcaPercentThresholdBuy, tc.dcaPercentThresholdSell))
		})
	}
}

func runBot(maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell float64) error {
	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)

	logger, err := config.Build()
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

	trader, tradeServiceFactory := createTradeServiceFactory(logger, pair, prices, balanceBTC, balanceUSDT, maxDcaTrades, dcaPercentThresholdBuy, dcaPercentThresholdSell)

	price, ok := <-prices
	if !ok {
		return fmt.Errorf("prices channel is closed")
	}

	var (
		lastAction   = entity.ActionSell // Start with ActionSell so we'll buy first
		lastPriceBTC = price
		tradeService *services.TradeService
	)

	// If we're starting with USDT (no BTC), we need to make an initial buy
	if balanceBTC.IsZero() && balanceUSDT.IsPositive() {
		// Create the trade service
		tradeService, err = tradeServiceFactory(lastAction)
		if err != nil {
			log.Fatalf("Failed to create trade service for initial buy: %s", err)
		}
	}

	// Создаем TradeService один раз перед циклом
	tradeService, err = tradeServiceFactory(lastAction)
	if err != nil {
		if !strings.Contains(err.Error(), "channel less than min") {
			log.Fatalf("err trade svc create %s", err)
		}
	}

	for range klines {
		// execute trade
		tradeEvent, err := tradeService.Trade()
		if err != nil {
			log.Debug(err)
		}

		if tradeEvent != nil && tradeEvent.Action != entity.ActionNull {
			lastPriceBTC = tradeEvent.Price
			log.Infof("Trade executed: %s at price %s, amount %s, current deals count: %d",
				tradeEvent.Action, tradeEvent.Price, tradeEvent.Amount, trader.dealsCount)
		}
	}

	summarizeResults(log, trader, pair, balanceBTC, lastPriceBTC)
	return nil
}

func summarizeResults(log *zap.SugaredLogger, trader *traderCsv, pair *entity.Pair, initialBalanceBTC, lastPriceBTC decimal.Decimal) {
	// Get the initial USDT balance from the trader
	initialBalanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	log.Infof("Deals: %d", trader.dealsCount)
	log.Infof("Total balance of %s: %s (was %s)", pair.From, trader.balance1.StringFixed(5), initialBalanceBTC.StringFixed(5))
	log.Infof("Total balance of %s: %s (was %s)", pair.To, trader.balance2.StringFixed(5), initialBalanceUSDT.StringFixed(5))
	log.Infof("Total fee: %s", trader.fee.StringFixed(5))

	// Calculate total profit in USDT
	totalProfit := calculateTotalProfit(trader)
	if trader.balance1.IsPositive() {
		totalProfit = totalProfit.Add(lastPriceBTC.Mul(trader.balance1))
	}

	log.Infof("Total profit: %s %s", totalProfit.StringFixed(2), pair.To)

	// Calculate profit percentage based on initial USDT investment
	var profitPercent string
	if initialBalanceUSDT.IsPositive() {
		// Profit = (Current USDT - Initial USDT) / Initial USDT * 100
		profitPercent = totalProfit.Sub(initialBalanceUSDT).Mul(decimal.NewFromInt(100)).Div(initialBalanceUSDT).StringFixed(2)
	} else if initialBalanceBTC.IsPositive() {
		// Fallback to BTC calculation if we started with BTC
		initialValueUSDT := initialBalanceBTC.Mul(lastPriceBTC)
		profitPercent = totalProfit.Sub(initialValueUSDT).Mul(decimal.NewFromInt(100)).Div(initialValueUSDT).StringFixed(2)
	} else {
		profitPercent = "N/A"
	}

	log.Infof("%s%% profit (initial investment: %s USDT)", profitPercent, initialBalanceUSDT.StringFixed(2))
}

func calculateTotalProfit(trader *traderCsv) decimal.Decimal {
	return trader.balance2.Sub(trader.fee)
}

func prepareData(filePath string, pair *entity.Pair) (chan decimal.Decimal, chan entity.Kline, func(), error) {
	collectData, err := DataCollectorFactory(filePath, pair)
	if err != nil {
		return nil, nil, nil, err
	}

	cleanup := func() {
		os.Remove(filePath)
		os.RemoveAll("./wal")
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

func createTradeServiceFactory(logger *zap.Logger, pair *entity.Pair, prices chan decimal.Decimal, balanceBTC, balanceUSDT decimal.Decimal, maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell float64) (*traderCsv, func(entity.Action) (*services.TradeService, error)) {
	pricer := &pricerCsv{pricesCh: prices}
	trader := &traderCsv{pair: pair, balance1: balanceBTC, balance2: balanceUSDT, pricesCh: prices}

	tradeServiceLoggerConfig := zap.NewProductionConfig()
	tradeServiceLoggerConfig.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	tradeServiceLogger, _ := tradeServiceLoggerConfig.Build()

	return trader, func(lastAction entity.Action) (*services.TradeService, error) {

		dcaPercentThresholdBuyDecimal := decimal.NewFromFloat(dcaPercentThresholdBuy)
		dcaPercentThresholdSellDecimal := decimal.NewFromFloat(dcaPercentThresholdSell)

		ts, err := services.NewTradeService(
			tradeServiceLogger,
			*pair,
			balanceUSDT,
			pricer,
			trader,
			maxDcaTrades,
			dcaPercentThresholdBuyDecimal,
			dcaPercentThresholdSellDecimal,
		)
		if err != nil {
			return nil, err
		}

		// Получаем текущую цену напрямую из pricer
		currentPrice, _ := pricer.GetPrice(*pair)

		initialAmount := balanceUSDT.Div(decimal.NewFromInt(int64(maxDcaTrades)))

		// When starting with USDT (balanceBTC is zero), we want to record initial purchase when lastAction is Sell
		// When starting with BTC, we want to record initial purchase when lastAction is Buy
		if (balanceBTC.IsZero() && lastAction == entity.ActionSell) || (!balanceBTC.IsZero() && lastAction == entity.ActionBuy) {
			if err := ts.AddDCAPurchase(currentPrice, initialAmount, time.Now(), 0); err != nil {
				if !strings.Contains(err.Error(), "initial purchase already recorded") {
					logger.Error("Failed to record initial purchase", zap.Error(err))
					return nil, err
				}
				logger.Debug("Initial purchase already recorded, ignoring")
			}
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
		_, _ = decimal.NewFromString(record[1])
		_, _ = decimal.NewFromString(record[2])
		closePrice, _ := decimal.NewFromString(record[3])

		price := closePrice
		prices = append(prices, price)
		klines = append(klines, entity.Kline{Open: open, Close: closePrice})
	}

	return prices, klines
}
