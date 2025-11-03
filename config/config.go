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
// including exchange platform, trading pair, and strategy-specific parameters.
type Config struct {
	// Platform specifies the trading exchange platform (e.g., "binance", "bybit", "simulate")
	Platform string
	// Pair represents the cryptocurrency trading pair (e.g., BTC/USDT)
	Pair entity.Pair
	// StrategyType specifies which trading strategy to use ("dca" or "ai")
	StrategyType string
	// AmountPercent is the percentage of quote currency balance to use for each trade (1-100)
	// Used by DCA strategy
	AmountPercent decimal.Decimal
	// PollPriceInterval defines how often to check price updates
	PollPriceInterval time.Duration
	// MarketType specifies the market type for trading ("spot" or "margin")
	MarketType entity.MarketType
	// Leverage specifies the leverage multiplier for margin trading (1-20)
	// NOTE: leverage > 1 can ONLY be used with MarketTypeMargin, NOT with MarketTypeSpot
	// NOTE: Leverage parameter is NOT supported for AI strategy. AI strategy manages position sizing internally.
	// Config validation will reject:
	// - spot trading with leverage > 1
	// - AI strategy with leverage parameter specified
	Leverage int

	// DCA Strategy parameters
	// MaxDcaTrades is the maximum number of DCA trades allowed
	MaxDcaTrades int
	// DcaPercentThresholdBuy is the percentage threshold for triggering buy orders
	DcaPercentThresholdBuy decimal.Decimal
	// DcaPercentThresholdSell is the percentage threshold for triggering sell orders
	DcaPercentThresholdSell decimal.Decimal

	// AI Strategy parameters
	// LLMAPI URL is the endpoint for the LLM service (e.g., "https://openrouter.ai/api/v1/chat/completions")
	LLMAPIURL string
	// LLMAPIKey is the API key for the LLM service
	LLMAPIKey string
	// Model specifies which LLM model to use (e.g., "deepseek/deepseek-chat", "openai/gpt-4")
	Model string
	// KlineInterval defines the interval for kline data (e.g., "1m", "3m", "5m", "1h")
	KlineInterval string
	// HigherTimeframe defines the higher timeframe interval for multi-timeframe analysis (e.g., "15m" when primary is "3m")
	// If not specified, defaults to "15m" for 3m primary timeframe
	HigherTimeframe string
	// LookbackPeriods defines how many historical periods to analyze
	LookbackPeriods int
	// MaxLeverage is the maximum leverage allowed for AI strategy trades
	MaxLeverage int
}

type ConfigTmp struct {
	Pair              string        `yaml:"pair"`
	Platform          string        `yaml:"platform"`
	Strategy          string        `yaml:"strategy,omitempty"`
	Amount            string        `yaml:"amount,omitempty"`
	PollPriceInterval time.Duration `yaml:"pollpriceinterval"`
	MarketTypeStr     string        `yaml:"market_type,omitempty"`
	LeverageStr       string        `yaml:"leverage,omitempty"`

	// DCA strategy fields
	MaxDcaTradesStr            string `yaml:"max_dca_trades,omitempty"`
	DcaPercentThresholdBuyStr  string `yaml:"dca_percent_threshold_buy,omitempty"`
	DcaPercentThresholdSellStr string `yaml:"dca_percent_threshold_sell,omitempty"`

	// AI strategy fields
	LLMAPIURL          string `yaml:"llm_api_url,omitempty"`
	LLMAPIKey          string `yaml:"llm_api_key,omitempty"`
	Model              string `yaml:"model,omitempty"`
	KlineInterval      string `yaml:"kline_interval,omitempty"`
	HigherTimeframe    string `yaml:"higher_timeframe,omitempty"`
	LookbackPeriodsStr string `yaml:"lookback_periods,omitempty"`
	MaxLeverageStr     string `yaml:"max_leverage,omitempty"`
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
			AmountPercent:           amount,
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
	amountFlag := flag.String("amount", "10", "percentage of quote currency balance to use (1-100)")
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

	if amount.LessThan(decimal.NewFromInt(1)) || amount.GreaterThan(decimal.NewFromInt(100)) {
		return entity.Pair{}, decimal.Decimal{}, 0, fmt.Errorf("amount must be between 1 and 100 percent, got %s", amount.String())
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

		// Determine strategy type (default to "dca" for backward compatibility)
		strategyType := c.Strategy
		if strategyType == "" {
			strategyType = "dca"
		}

		// Parse market_type (default to "spot")
		marketType := entity.MarketType(c.MarketTypeStr)
		if c.MarketTypeStr == "" {
			marketType = entity.MarketTypeSpot
		}
		if !marketType.IsValid() {
			return nil, fmt.Errorf("invalid market_type '%s' in yaml config (must be 'spot' or 'margin')", c.MarketTypeStr)
		}

		// Parse leverage (default to 1)
		leverage := 1
		if c.LeverageStr != "" {
			var err error
			leverage, err = strconv.Atoi(c.LeverageStr)
			if err != nil {
				return nil, fmt.Errorf("incorrect 'leverage' param in yaml config (must be an integer), error: %w", err)
			}
			if leverage < 1 || leverage > 20 {
				return nil, fmt.Errorf("leverage must be between 1 and 20, got %d", leverage)
			}
		}

		// Validate: spot trading cannot use leverage > 1
		if marketType == entity.MarketTypeSpot && leverage > 1 {
			return nil, fmt.Errorf("leverage > 1 is not allowed for spot trading (market_type: spot). Use market_type: margin for leverage trading")
		}

		// Validate: AI strategy does not support leverage parameter
		if strategyType == "ai" && (c.LeverageStr != "" || leverage > 1) {
			return nil, fmt.Errorf("leverage parameter is not supported for AI strategy. AI strategy manages position sizing internally. Please remove the 'leverage' parameter from your config")
		}

		// Initialize newConfig correctly for each iteration
		newConfig := Config{
			Platform:          c.Platform,
			Pair:              pair,
			StrategyType:      strategyType,
			PollPriceInterval: c.PollPriceInterval,
			MarketType:        marketType,
			Leverage:          leverage,
		}

		// Parse common or DCA-specific fields
		if strategyType == "dca" {
			// Amount is required for DCA strategy
			if c.Amount == "" {
				return nil, fmt.Errorf("'amount' is required for DCA strategy")
			}
			amount, err := decimal.NewFromString(c.Amount)
			if err != nil {
				return nil, fmt.Errorf("incorrect 'amount' param in yaml config (must be a number between 1 and 100), error: %w", err)
			}
			if amount.LessThan(decimal.NewFromInt(1)) || amount.GreaterThan(decimal.NewFromInt(100)) {
				return nil, fmt.Errorf("amount must be between 1 and 100 percent, got %s", amount.String())
			}
			newConfig.AmountPercent = amount

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
		} else if strategyType == "ai" {
			// Parse AI strategy fields
			if c.LLMAPIKey == "" {
				return nil, fmt.Errorf("'llm_api_key' is required for AI strategy")
			}
			newConfig.LLMAPIKey = c.LLMAPIKey

			if c.LLMAPIURL == "" {
				newConfig.LLMAPIURL = "https://openrouter.ai/api/v1/chat/completions" // Default to OpenRouter
			} else {
				newConfig.LLMAPIURL = c.LLMAPIURL
			}

			if c.Model == "" {
				newConfig.Model = "deepseek/deepseek-chat" // Default model for OpenRouter
			} else {
				newConfig.Model = c.Model
			}

			if c.KlineInterval == "" {
				newConfig.KlineInterval = "3m" // Default interval
			} else {
				newConfig.KlineInterval = c.KlineInterval
			}

			// Parse HigherTimeframe (defaults to "15m" for multi-timeframe analysis)
			if c.HigherTimeframe == "" {
				newConfig.HigherTimeframe = "15m" // Default higher timeframe
			} else {
				newConfig.HigherTimeframe = c.HigherTimeframe
			}

			// Parse LookbackPeriods
			if c.LookbackPeriodsStr == "" {
				newConfig.LookbackPeriods = 50 // Default: enough for indicators
			} else {
				lookback, err := strconv.Atoi(c.LookbackPeriodsStr)
				if err != nil {
					return nil, fmt.Errorf("incorrect 'lookback_periods' param in yaml config (must be an integer), error: %w", err)
				}
				if lookback < 50 {
					return nil, fmt.Errorf("lookback_periods must be at least 50 for indicator calculation, got %d", lookback)
				}
				newConfig.LookbackPeriods = lookback
			}

			// Parse MaxLeverage
			if c.MaxLeverageStr == "" {
				newConfig.MaxLeverage = 10 // Default leverage
			} else {
				maxLeverage, err := strconv.Atoi(c.MaxLeverageStr)
				if err != nil {
					return nil, fmt.Errorf("incorrect 'max_leverage' param in yaml config (must be an integer), error: %w", err)
				}
				if maxLeverage < 1 || maxLeverage > 20 {
					return nil, fmt.Errorf("max_leverage must be between 1 and 20, got %d", maxLeverage)
				}
				newConfig.MaxLeverage = maxLeverage
			}
		} else {
			return nil, fmt.Errorf("unsupported strategy type: %s (must be 'dca' or 'ai')", strategyType)
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
