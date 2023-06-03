package config

import (
	"flag"
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"time"
)

type Config struct {
	Pair        entity.Pair
	Klinesize   string
	KlineInPast uint64
	Koeff       decimal.Decimal
	Usebalance  decimal.Decimal
	Minwindow   decimal.Decimal
}

type ConfigTmp struct {
	Pair        string
	Klinesize   string
	KlineInPast uint64
	Koeff       string
	Usebalance  string
	Minwindow   string
}

func Get() ([]Config, error) {
	config := flag.String("config", "", "path to yaml config")
	flag.Parse()
	if *config != "" {
		return getYaml(*config)
	}

	pair, klineSize, klineInPast, koeff, usebalance, minwindow, err := getEnv()
	if err != nil {
		return nil, err
	}

	return []Config{
		{
			Pair:        pair,
			Klinesize:   klineSize,
			KlineInPast: klineInPast,
			Koeff:       koeff,
			Usebalance:  usebalance,
			Minwindow:   minwindow,
		},
	}, nil
}

func getEnv() (pair entity.Pair, klinesize string, klineInPast uint64, koeff, usebalance, minwindow decimal.Decimal, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	minw := flag.String("minwindow", "100", "min window size")
	kline := flag.String("klinesize", "1h", "kline size, example: 1h")
	kPast := flag.Uint64("klinesinpast", 5, "klines in past that will be  used for stats count, example: 10")
	useb := flag.String("usebalance", "100", "percent of balance usage, for example 90 means 90%")
	k := flag.String("koeff", "1", "koeff for multiply buyprice and window found, example: 0.98")
	flag.Parse()

	var err error
	pair, err = getPairFromString(*pairFlag)
	if err != nil {
		return entity.Pair{}, "", 0, decimal.Decimal{}, decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("invalid --par provided, --pair=%s", *pairFlag)
	}
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

	return pair, klinesize, *kPast, koeff, usebalance, minwindow, nil
}

func getYaml(path string) ([]Config, error) {
	var configsTmp []ConfigTmp

	f, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(f, &configsTmp)
	if err != nil {
		return nil, err
	}

	configs := make([]Config, 0, len(configsTmp))

	for _, c := range configsTmp {
		pair, err := getPairFromString(c.Pair)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'pair' param in yaml config (correct format is COIN1_COIN2), error: %s", err)
		}
		koeff, err := decimal.NewFromString(c.Koeff)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'koeff' param in yaml config (correct format is 123.12), error: %s", err)
		}
		usebalance, err := decimal.NewFromString(c.Usebalance)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'usebalance' param in yaml config (correct format is 12), error: %s", err)
		}
		minwindow, err := decimal.NewFromString(c.Minwindow)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'minwindow' param in yaml config (correct format is 123), error: %s", err)
		}

		configs = append(configs, Config{
			Pair:        pair,
			Klinesize:   c.Klinesize,
			KlineInPast: c.KlineInPast,
			Koeff:       koeff,
			Usebalance:  usebalance,
			Minwindow:   minwindow,
		})
	}
	return configs, nil
}

func getPairFromString(pairStr string) (entity.Pair, error) {
	pairElements := strings.Split(pairStr, "_")
	if len(pairElements) != 2 {
		return entity.Pair{}, fmt.Errorf("invalid pair param")
	}
	return entity.Pair{From: pairElements[0], To: pairElements[1]}, nil
}
