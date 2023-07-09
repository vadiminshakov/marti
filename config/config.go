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
	Pair              entity.Pair
	StatHours         uint64
	Usebalance        decimal.Decimal
	Minwindow         decimal.Decimal
	RebalanceInterval time.Duration
	PollPriceInterval time.Duration
}

type ConfigTmp struct {
	Pair              string
	StatHours         uint64
	Usebalance        string
	Minwindow         string
	RebalanceInterval time.Duration
	PollPriceInterval time.Duration
}

func Get() ([]Config, error) {
	config := flag.String("config", "", "path to yaml config")
	flag.Parse()
	if *config != "" {
		return getYaml(*config)
	}

	pair, statHours, usebalance, minwindow, rebalanceInterval, pollPriceInterval, err := getFromCLI()
	if err != nil {
		return nil, err
	}

	return []Config{
		{
			Pair:              pair,
			StatHours:         statHours,
			Usebalance:        usebalance,
			Minwindow:         minwindow,
			RebalanceInterval: rebalanceInterval,
			PollPriceInterval: pollPriceInterval,
		},
	}, nil
}

func getFromCLI() (pair entity.Pair, hours uint64, usebalance, minwindow decimal.Decimal,
	rebalanceInterval, pollPriceInterval time.Duration, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	minw := flag.String("minwindow", "100", "min window size")
	statH := flag.Uint64("stathours", 5, "hours in past that will be used for stats count, example: 10")
	useb := flag.String("usebalance", "100", "percent of balance usage, for example 90 means 90%")
	ri := flag.Duration("rebalanceinterval", 30*time.Hour, "rebalance interval")
	pi := flag.Duration("pollpriceinterval", 5*time.Minute, "poll market price interval")

	flag.Parse()

	var err error
	pair, err = getPairFromString(*pairFlag)
	if err != nil {
		return entity.Pair{}, 0, decimal.Decimal{}, decimal.Decimal{}, 0, 0, fmt.Errorf("invalid --par provided, --pair=%s", *pairFlag)
	}
	usebalance, err = decimal.NewFromString(*useb)
	if err != nil {
		return entity.Pair{}, 0, decimal.Decimal{}, decimal.Decimal{}, 0, 0, err
	}
	minwindow, err = decimal.NewFromString(*minw)
	if err != nil {
		return entity.Pair{}, 0, decimal.Decimal{}, decimal.Decimal{}, 0, 0, err
	}

	hours = *statH
	rebalanceInterval = *ri
	pollPriceInterval = *pi

	ub := usebalance.BigInt().Int64()

	if ub < 0 || ub > 100 {
		return entity.Pair{}, 0, decimal.Decimal{}, decimal.Decimal{}, 0, 0,
			fmt.Errorf("invalid --usebalance provided, --usebalance=%s", usebalance.String())
	}

	return pair, hours, usebalance, minwindow, rebalanceInterval, pollPriceInterval, nil
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
		usebalance, err := decimal.NewFromString(c.Usebalance)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'usebalance' param in yaml config (correct format is 12), error: %s", err)
		}
		minwindow, err := decimal.NewFromString(c.Minwindow)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'minwindow' param in yaml config (correct format is 123), error: %s", err)
		}

		configs = append(configs, Config{
			Pair:              pair,
			StatHours:         c.StatHours,
			Usebalance:        usebalance,
			Minwindow:         minwindow,
			RebalanceInterval: c.RebalanceInterval,
			PollPriceInterval: c.PollPriceInterval,
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
