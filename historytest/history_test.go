package historytest

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal"
	"github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/strategy/dca"
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
			name:                    "1 year - Conservative",
			duration:                8760,
			maxDcaTrades:            20,
			dcaPercentThresholdBuy:  2,
			dcaPercentThresholdSell: 20,
		},
		{
			name:                    "2 years - Conservative",
			duration:                17520,
			maxDcaTrades:            40,
			dcaPercentThresholdBuy:  2,
			dcaPercentThresholdSell: 20,
		},
		{
			name:                    "1 year - Aggressive",
			duration:                8760,
			maxDcaTrades:            100,
			dcaPercentThresholdBuy:  0.5,
			dcaPercentThresholdSell: 4,
		},
		{
			name:                    "2 years - Aggressive",
			duration:                17520,
			maxDcaTrades:            200,
			dcaPercentThresholdBuy:  0.5,
			dcaPercentThresholdSell: 4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dataHoursAgo = tc.duration
			summary, err := runBot(tc.maxDcaTrades, tc.dcaPercentThresholdBuy, tc.dcaPercentThresholdSell)
			require.NoError(t, err)

			fmt.Printf("[%s] Deals: %d\n", tc.name, summary.Deals)
			fmt.Printf("[%s] Total balance of %s: %s (was %s)\n", tc.name, summary.PairFrom, summary.BalanceFromFinal.StringFixed(5), summary.BalanceFromInitial.StringFixed(5))
			fmt.Printf("[%s] Total balance of %s: %s (was %s)\n", tc.name, summary.PairTo, summary.BalanceToFinal.StringFixed(5), summary.BalanceToInitial.StringFixed(5))
			fmt.Printf("[%s] Total fee: %s\n", tc.name, summary.TotalFee.StringFixed(5))
			fmt.Printf("[%s] Total profit: %s %s\n", tc.name, summary.TotalProfit.StringFixed(2), summary.PairTo)
			fmt.Printf("[%s] %s%% profit (initial investment: %s %s)\n", tc.name, summary.ProfitPercent, summary.BalanceToInitial.StringFixed(2), summary.PairTo)
		})
	}
}

type botSummary struct {
	Deals              uint
	PairFrom           string
	PairTo             string
	BalanceFromInitial decimal.Decimal
	BalanceFromFinal   decimal.Decimal
	BalanceToInitial   decimal.Decimal
	BalanceToFinal     decimal.Decimal
	TotalFee           decimal.Decimal
	TotalProfit        decimal.Decimal
	ProfitPercent      string
}

func runBot(maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell float64) (botSummary, error) {
	logger := zap.NewNop()
	defer logger.Sync()
	log := logger.Sugar()

	pair := &entity.Pair{From: "BTC", To: "USDT"}

	prices, klines, cleanup, err := prepareData(dataFile, pair)
	if err != nil {
		return botSummary{}, fmt.Errorf("failed to prepare data: %w", err)
	}
	defer cleanup()

	balanceBTC, _ := decimal.NewFromString(btcBalanceInWallet)
	balanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	trader, strategyFactory := createStrategyFactory(logger, pair, prices, balanceBTC, balanceUSDT, maxDcaTrades, dcaPercentThresholdBuy, dcaPercentThresholdSell)

	price, ok := <-prices
	if !ok {
		return botSummary{}, fmt.Errorf("prices channel is closed")
	}

	var (
		lastPriceBTC    = price
		tradingStrategy internal.TradingStrategy
	)

	tradingStrategy, err = strategyFactory()
	if err != nil {
		if !strings.Contains(err.Error(), "channel less than min") {
			log.Fatalf("failed to create trading strategy: %s", err)
		}
	}

	ctx := context.Background()

	if tradingStrategy != nil {
		if err := tradingStrategy.Initialize(ctx); err != nil {
			log.Fatalf("failed to initialize strategy: %s", err)
		}
		defer tradingStrategy.Close()
	}

	for range klines {
		// execute trade
		tradeEvent, err := tradingStrategy.Trade(ctx)
		if err != nil {
			log.Debug(err)
		}

		if tradeEvent != nil && tradeEvent.Action != entity.ActionNull {
			lastPriceBTC = tradeEvent.Price
			log.Infof("Trade executed: %s at price %s, amount %s, current deals count: %d",
				tradeEvent.Action, tradeEvent.Price, tradeEvent.Amount, trader.dealsCount)
		}
	}

	return summarizeResults(trader, pair, balanceBTC, lastPriceBTC), nil
}

func summarizeResults(trader *traderCsv, pair *entity.Pair, initialBalanceBTC, lastPriceBTC decimal.Decimal) botSummary {
	// get the initial USDT balance from the trader
	initialBalanceUSDT, _ := decimal.NewFromString(usdtBalanceInWallet)

	// calculate total profit in USDT
	totalProfit := calculateTotalProfit(trader)
	if trader.balance1.IsPositive() {
		totalProfit = totalProfit.Add(lastPriceBTC.Mul(trader.balance1))
	}

	// calculate profit percentage based on initial USDT investment
	var profitPercent string
	if initialBalanceUSDT.IsPositive() {
		// profit = (Current USDT - Initial USDT) / Initial USDT * 100
		profitPercent = totalProfit.Sub(initialBalanceUSDT).Mul(decimal.NewFromInt(100)).Div(initialBalanceUSDT).StringFixed(2)
	} else if initialBalanceBTC.IsPositive() {
		// fallback to BTC calculation if we started with BTC
		initialValueUSDT := initialBalanceBTC.Mul(lastPriceBTC)
		profitPercent = totalProfit.Sub(initialValueUSDT).Mul(decimal.NewFromInt(100)).Div(initialValueUSDT).StringFixed(2)
	} else {
		profitPercent = "N/A"
	}

	return botSummary{
		Deals:              trader.dealsCount,
		PairFrom:           pair.From,
		PairTo:             pair.To,
		BalanceFromInitial: initialBalanceBTC,
		BalanceFromFinal:   trader.balance1,
		BalanceToInitial:   initialBalanceUSDT,
		BalanceToFinal:     trader.balance2,
		TotalFee:           trader.fee,
		TotalProfit:        totalProfit,
		ProfitPercent:      profitPercent,
	}
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

func createStrategyFactory(logger *zap.Logger, pair *entity.Pair, prices chan decimal.Decimal, balanceBTC, balanceUSDT decimal.Decimal, maxDcaTrades int, dcaPercentThresholdBuy, dcaPercentThresholdSell float64) (*traderCsv, func() (internal.TradingStrategy, error)) {
	pricer := &pricerCsv{pricesCh: prices}
	trader := &traderCsv{pair: pair, balance1: balanceBTC, balance2: balanceUSDT, pricesCh: prices, executed: make(map[string]decimal.Decimal)}

	return trader, func() (internal.TradingStrategy, error) {
		dcaPercentThresholdBuyDecimal := decimal.NewFromFloat(dcaPercentThresholdBuy)
		dcaPercentThresholdSellDecimal := decimal.NewFromFloat(dcaPercentThresholdSell)

		// for backtesting: use 20% to maximize capital utilization
		// this means: use all available USDT balance for EACH trade
		// each buy order will use: current_balance * 20%
		// this represents aggressive reinvestment strategy, good for backtesting
		// maxDcaTrades only limits the number of buys in a series
		amountPercent := decimal.NewFromInt(20)

		dcaStrategy, err := dca.NewDCAStrategy(
			logger,
			*pair,
			amountPercent,
			pricer,
			trader,
			maxDcaTrades,
			dcaPercentThresholdBuyDecimal,
			dcaPercentThresholdSellDecimal,
		)
		if err != nil {
			return nil, err
		}

		return dcaStrategy, nil
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
