package main

import (
	"context"
	"encoding/csv"
	"errors"
	"github.com/adshao/go-binance/v2"
	"github.com/vadimInshakov/marti/entity"
	"os"
	"time"
)

func dataColletorFactory(filePath string, pair *entity.Pair) (func(fromHoursAgo, toHoursAgo int, klinesize string) error, error) {
	apikey := os.Getenv("APIKEY")
	if len(apikey) == 0 {
		return nil, errors.New("APIKEY env is not set")
	}

	secretkey := os.Getenv("SECRETKEY")
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

func collectMarketData(client *binance.Client, pair *entity.Pair, fromHoursAgo, toHoursAgo int, klinesize string) ([][]string, error) {
	startTime := time.Now().Add(-time.Duration(fromHoursAgo)*time.Hour).Unix() * 1000
	endTime := time.Now().Add(-time.Duration(toHoursAgo)*time.Hour).Unix() * 1000

	klines, err := client.NewKlinesService().Symbol(pair.Symbol()).StartTime(startTime).
		EndTime(endTime).
		Interval(klinesize).Do(context.Background())
	if err != nil {
		return nil, err
	}

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
