package config

import (
	"flag"
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"strings"
	"time"
)

func Get() (pair entity.Pair, klinesize string, klineInPast uint64, koeff, usebalance, minwindow decimal.Decimal, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	minw := flag.String("minwindow", "100", "min window size")
	kline := flag.String("klinesize", "1h", "kline size, example: 1h")
	kPast := flag.Uint64("klinesinpast", 5, "klines in past that will be  used for stats count, example: 10")
	useb := flag.String("usebalance", "100", "percent of balance usage, for example 90 means 90%")
	k := flag.String("koeff", "1", "koeff for multiply buyprice and window found, example: 0.98")
	flag.Parse()

	var err error
	usebalance, err = decimal.NewFromString(*useb)
	if err != nil {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, err
	}
	minwindow, err = decimal.NewFromString(*minw)
	if err != nil {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, err
	}
	koeff, err = decimal.NewFromString(*k)
	if err != nil {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, err
	}

	klinesize = *kline

	_, err = time.ParseDuration(klinesize)
	if err != nil {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("invalid --klinesize provided: --klinesize=%s", klinesize)
	}

	ub := usebalance.BigInt().Int64()

	if ub < 0 || ub > 100 {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("invalid --usebalance provided, --usebalance=%f", usebalance)
	}

	pairElements := strings.Split(*pairFlag, "_")
	if len(pairElements) != 2 {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("invalid --par provided, --pair=%s", *pairFlag)
	}
	pair = entity.Pair{From: pairElements[0], To: pairElements[1]}

	return pair, klinesize, *kPast, koeff, usebalance, minwindow, nil
}
