package historytest

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"sort"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/vadiminshakov/marti/internal/entity"
)

func DataCollectorFactory(filePath string, pair *entity.Pair) (func(fromHoursAgo, toHoursAgo int, klinesize string) error, error) {
	apikey := os.Getenv("BINANCE_API_KEY")
	if len(apikey) == 0 {
		return nil, errors.New("BINANCE_API_KEY env is not set")
	}

	secretkey := os.Getenv("BINANCE_API_SECRET")
	if len(apikey) == 0 {
		return nil, errors.New("SECRETKEY env is not set")
	}

	client := binance.NewClient(apikey, secretkey)

	return func(fromHoursAgo, toHoursAgo int, klinesize string) error {
		data, err := collectMarketData(client, pair, fromHoursAgo, toHoursAgo, klinesize)
		if err != nil {
			return err
		}
		return writeMarketDataCsv(filePath, data)
	}, nil
}

// collectMarketData fetches historical market data from Binance for a given pair and time range
// This is used within the closure returned by DataCollectorFactory
func collectMarketData(client *binance.Client, pair *entity.Pair, fromHoursAgo, toHoursAgo int, klinesize string) ([][]string, error) {
	startTime := time.Now().Add(-time.Duration(fromHoursAgo)*time.Hour).Unix() * 1000
	endTime := time.Now().Add(-time.Duration(toHoursAgo)*time.Hour).Unix() * 1000

	klines, err := client.NewKlinesService().Symbol(pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(klinesize).Do(context.Background())
	if err != nil {
		return nil, err
	}

	sort.Slice(klines, func(i, j int) bool {
		return klines[i].OpenTime < klines[j].OpenTime
	})

	data := make([][]string, 0, len(klines))
	for _, kline := range klines {
		data = append(data, []string{
			kline.Open,
			kline.High,
			kline.Low,
			kline.Close,
		})
	}

	return data, nil
}

func writeMarketDataCsv(filePath string, data [][]string) error {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer f.Close()

	w := csv.NewWriter(f)

	return w.WriteAll(data)
}
