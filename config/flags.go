package config

import (
	"flag"
	"fmt"
	"github.com/vadimInshakov/marti/entity"
	"strings"
	"time"
)

func Get() (pair entity.Pair, klinesize string, koeff float64, usebalance float64, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	kline := flag.String("klinesize", "1h", "kline size, example: 1h")
	useb := flag.Float64("usebalance", 100, "percent of balance usage, for example 90 means 90%")
	k := flag.Float64("koeff", 1, "koeff for multiply buyprice and window found, example: 0.98")
	flag.Parse()

	usebalance = *useb
	koeff = *k
	klinesize = *kline

	_, err := time.ParseDuration(klinesize)
	if err != nil {
		return entity.Pair{}, "", 0, 0, fmt.Errorf("invalid --klinesize provided: --klinesize=%s", klinesize)
	}

	if usebalance < 0 || usebalance > 100 {
		return entity.Pair{}, "", 0, 0, fmt.Errorf("invalid --usebalance provided, --usebalance=%f", usebalance)
	}

	pairElements := strings.Split(*pairFlag, "_")
	if len(pairElements) != 2 {
		return entity.Pair{}, "", 0, 0, fmt.Errorf("invalid --par provided, --pair=%s", *pairFlag)
	}
	pair = entity.Pair{From: pairElements[0], To: pairElements[1]}

	return pair, klinesize, koeff, usebalance, nil
}
