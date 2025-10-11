// Package config provides configuration management for the marti trading bot.
// It supports both YAML file-based configuration and command-line arguments.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"gopkg.in/yaml.v3"
)

// Config represents the trading bot configuration parameters.
// It contains all necessary settings for running a trading strategy
// including exchange platform, trading pair, and DCA strategy parameters.
type Config struct {
	// Platform specifies the trading exchange platform (e.g., "binance", "bybit")
	Platform string
	// Pair represents the cryptocurrency trading pair (e.g., BTC/USDT)
	Pair entity.Pair
	// Amount is the base trading amount for each trade
	Amount decimal.Decimal
	// PollPriceInterval defines how often to check price updates
	PollPriceInterval time.Duration
	// MaxDcaTrades is the maximum number of DCA trades allowed
	MaxDcaTrades int
	// DcaPercentThresholdBuy is the percentage threshold for triggering buy orders
	DcaPercentThresholdBuy decimal.Decimal
	// DcaPercentThresholdSell is the percentage threshold for triggering sell orders
	DcaPercentThresholdSell decimal.Decimal
}

type ConfigTmp struct {
	Pair                       string        `yaml:"pair"`
	Platform                   string        `yaml:"platform"`
	Amount                     string        `yaml:"amount"`
	PollPriceInterval          time.Duration `yaml:"pollpriceinterval"`
	MaxDcaTradesStr            string        `yaml:"max_dca_trades,omitempty"`
	DcaPercentThresholdBuyStr  string        `yaml:"dca_percent_threshold_buy,omitempty"`
	DcaPercentThresholdSellStr string        `yaml:"dca_percent_threshold_sell,omitempty"`
}

// Get retrieves configuration settings from either YAML file or command-line arguments.
// If a config file path is provided via the -config flag, it reads from the YAML file.
// Otherwise, it attempts to parse configuration from command-line flags.
// Returns a slice of Config objects to support multiple trading configurations.
func Get() ([]Config, error) {
	config := flag.String("config", "", "path to yaml config")
	flag.Parse()
	if *config != "" {
		return getYaml(*config)
	}

	pair, amount, pollPriceInterval, err := getFromCLI()
	if err != nil {
		return nil, err
	}

	return []Config{
		{
			Pair:                    pair,
			Amount:                  amount,
			PollPriceInterval:       pollPriceInterval,
			MaxDcaTrades:            3,
			DcaPercentThresholdBuy:  decimal.NewFromFloat(3.5),
			DcaPercentThresholdSell: decimal.NewFromInt(66),
		},
	}, nil
}

func getFromCLI() (pair entity.Pair, amount decimal.Decimal,
	pollPriceInterval time.Duration, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	amountFlag := flag.String("amount", "100", "amount to trade")
	pi := flag.Duration("pollpriceinterval", 5*time.Minute, "poll market price interval")

	flag.Parse()

	var err error
	pair, err = getPairFromString(*pairFlag)
	if err != nil {
		return entity.Pair{}, decimal.Decimal{}, 0, fmt.Errorf("invalid --pair provided, --pair=%s", *pairFlag)
	}
	amount, err = decimal.NewFromString(*amountFlag)
	if err != nil {
		return entity.Pair{}, decimal.Decimal{}, 0, err
	}

	pollPriceInterval = *pi

	return pair, amount, pollPriceInterval, nil
}

func getYaml(path string) ([]Config, error) {
	var configsTmp []ConfigTmp
	var configs []Config

	f, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(f, &configsTmp); err != nil {
		return nil, err
	}

	for _, c := range configsTmp {
		pair, err := getPairFromString(c.Pair)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'pair' param in yaml config: %s, error: %w", c.Pair, err)
		}
		amount, err := decimal.NewFromString(c.Amount)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'amount' param in yaml config (correct format is 12), error: %w", err)
		}

		// Initialize newConfig correctly for each iteration.
		newConfig := Config{
			Pair:              pair,
			Amount:            amount,
			PollPriceInterval: c.PollPriceInterval,
		}

		// Parse MaxDcaTrades
		if c.MaxDcaTradesStr == "" {
			newConfig.MaxDcaTrades = 15 // Default value
		} else {
			maxDcaTrades, err := strconv.Atoi(c.MaxDcaTradesStr)
			if err != nil {
				return nil, fmt.Errorf("incorrect 'max_dca_trades' param in yaml config (must be an integer), error: %w", err)
			}
			newConfig.MaxDcaTrades = maxDcaTrades
		}

		// Parse DcaPercentThresholdBuy
		if c.DcaPercentThresholdBuyStr == "" {
			newConfig.DcaPercentThresholdBuy = decimal.NewFromInt(1) // Default value
		} else {
			dcaBuyThreshold, err := decimal.NewFromString(c.DcaPercentThresholdBuyStr)
			if err != nil {
				return nil, fmt.Errorf("incorrect 'dca_percent_threshold_buy' param in yaml config (must be a decimal), error: %w", err)
			}
			newConfig.DcaPercentThresholdBuy = dcaBuyThreshold
		}

		// Parse DcaPercentThresholdSell
		if c.DcaPercentThresholdSellStr == "" {
			newConfig.DcaPercentThresholdSell = decimal.NewFromInt(7) // Default value
		} else {
			dcaSellThreshold, err := decimal.NewFromString(c.DcaPercentThresholdSellStr)
			if err != nil {
				return nil, fmt.Errorf("incorrect 'dca_percent_threshold_sell' param in yaml config (must be a decimal), error: %w", err)
			}
			newConfig.DcaPercentThresholdSell = dcaSellThreshold
		}

		configs = append(configs, newConfig)
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
