// Package config provides configuration management (YAML + CLI flags) for the trading bot.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
	"gopkg.in/yaml.v3"
)

// Config holds all settings for running a trading strategy instance.
type Config struct {
	// Platform specifies the exchange (e.g. binance, bybit, simulate).
	Platform string
	// Pair is the trading pair (e.g. BTC/USDT).
	Pair entity.Pair
	// StrategyType selects strategy ("dca" or "ai").
	StrategyType string
	// AmountPercent is % of quote balance per trade (DCA only).
	AmountPercent decimal.Decimal
	// PollPriceInterval defines price polling interval.
	PollPriceInterval time.Duration
	// MarketType is "spot" or "margin".
	MarketType entity.MarketType
	// Leverage multiplier (margin only, ignored by AI; must be 1 for spot).
	Leverage int

	// MaxDcaTrades limits DCA trades in a series.
	MaxDcaTrades int
	// DcaPercentThresholdBuy triggers buy when percent drop reached.
	DcaPercentThresholdBuy decimal.Decimal
	// DcaPercentThresholdSell triggers sell when percent rise reached.
	DcaPercentThresholdSell decimal.Decimal

	// LLMAPIURL endpoint for LLM service.
	LLMAPIURL string
	// LLMAPIKey API key for LLM.
	LLMAPIKey string
	// Model LLM model identifier.
	Model string
	// PrimaryTimeframe primary market interval (e.g. 3m, 1h).
	PrimaryTimeframe string
	// HigherTimeframe higher interval (defaults to 15m when primary 3m).
	HigherTimeframe string
	// LookbackPeriods number of periods for indicators.
	LookbackPeriods int
	// HigherLookbackPeriods periods fetched for higher timeframe.
	HigherLookbackPeriods int
	// MaxLeverage upper leverage bound for AI sizing.
	MaxLeverage int
}

// SimulationStateKey returns a stable identifier used for namespacing simulator
// persistence files. It combines the platform, pair, strategy, market type,
// and (for AI strategies) the model so multiple bots on the same pair do not
// overwrite each other's local state.
func (c Config) SimulationStateKey() string {
	var parts []string
	if c.Platform != "" {
		parts = append(parts, strings.ToLower(c.Platform))
	}
	if pair := c.Pair.String(); pair != "" {
		parts = append(parts, strings.ToLower(pair))
	}
	if c.StrategyType != "" {
		parts = append(parts, strings.ToLower(c.StrategyType))
	}
	if mt := string(c.MarketType); mt != "" {
		parts = append(parts, strings.ToLower(mt))
	}
	if c.StrategyType == "ai" && c.Model != "" {
		parts = append(parts, sanitizeStateKeyComponent(c.Model))
	}
	return strings.Join(parts, "__")
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
	LLMAPIURL                 string `yaml:"llm_api_url,omitempty"`
	LLMAPIKey                 string `yaml:"llm_api_key,omitempty"`
	Model                     string `yaml:"model,omitempty"`
	PrimaryTimeframe          string `yaml:"primary_timeframe,omitempty"`
	HigherTimeframe           string `yaml:"higher_timeframe,omitempty"`
	PrimaryLookbackPeriodsStr string `yaml:"primary_lookback_periods,omitempty"`
	LookbackPeriodsStr        string `yaml:"lookback_periods,omitempty"` // legacy alias
	HigherLookbackPeriodsStr  string `yaml:"higher_lookback_periods,omitempty"`
	MaxLeverageStr            string `yaml:"max_leverage,omitempty"`
}

var (
	configPathFlag = flag.String("config", "", "path to yaml config")
	pairFlag       = flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	amountFlag     = flag.String("amount", "10", "percentage of quote currency balance to use (1-100)")
	piFlag         = flag.Duration("pollpriceinterval", 5*time.Minute, "poll market price interval")
)

// Get loads configuration from YAML (if -config provided) or CLI flags.
func Get() ([]Config, error) {
	flag.Parse()

	if *configPathFlag != "" {
		return getYaml(*configPathFlag)
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

	pollPriceInterval = *piFlag

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
		newConfig, err := parseConfig(c)
		if err != nil {
			return nil, err
		}
		configs = append(configs, *newConfig)
	}
	return configs, nil
}

func parseConfig(c ConfigTmp) (*Config, error) {
	pair, err := getPairFromString(c.Pair)
	if err != nil {
		return nil, fmt.Errorf("incorrect 'pair' param in yaml config: %s, error: %w", c.Pair, err)
	}

	strategyType := c.Strategy
	if strategyType == "" {
		strategyType = "dca"
	}

	marketType := entity.MarketType(c.MarketTypeStr)
	if c.MarketTypeStr == "" {
		marketType = entity.MarketTypeSpot
	}
	if !marketType.IsValid() {
		return nil, fmt.Errorf("invalid market_type '%s' in yaml config (must be 'spot' or 'margin')", c.MarketTypeStr)
	}

	leverage := 1
	if c.LeverageStr != "" {
		leverage, err = strconv.Atoi(c.LeverageStr)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'leverage' param in yaml config (must be an integer), error: %w", err)
		}
		if leverage < 1 {
			return nil, fmt.Errorf("leverage must be at least 1, got %d", leverage)
		}
	}

	if marketType == entity.MarketTypeSpot && leverage > 1 {
		return nil, fmt.Errorf("leverage > 1 is not allowed for spot trading. Use market_type: margin")
	}

	newConfig := &Config{
		Platform:          c.Platform,
		Pair:              pair,
		StrategyType:      strategyType,
		PollPriceInterval: c.PollPriceInterval,
		MarketType:        marketType,
		Leverage:          leverage,
	}

	switch strategyType {
	case "dca":
		if err := parseDCAConfig(&c, newConfig); err != nil {
			return nil, err
		}
	case "ai":
		if err := parseAIConfig(&c, newConfig); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported strategy type: %s (must be 'dca' or 'ai')", strategyType)
	}

	return newConfig, nil
}

func parseDCAConfig(c *ConfigTmp, cfg *Config) error {
	if c.Amount == "" {
		return errors.New("'amount' is required for DCA strategy")
	}
	amount, err := decimal.NewFromString(c.Amount)
	if err != nil {
		return errors.Wrap(err, "incorrect 'amount' param in yaml config (must be a number between 1 and 100)")
	}
	if amount.LessThan(decimal.NewFromInt(1)) || amount.GreaterThan(decimal.NewFromInt(100)) {
		return fmt.Errorf("amount must be between 1 and 100 percent, got %s", amount.String())
	}
	cfg.AmountPercent = amount

	if c.MaxDcaTradesStr == "" {
		cfg.MaxDcaTrades = 15
	} else {
		maxTrades, err := strconv.Atoi(c.MaxDcaTradesStr)
		if err != nil {
			return errors.Wrap(err, "incorrect 'max_dca_trades' param in yaml config (must be an integer)")
		}
		cfg.MaxDcaTrades = maxTrades
	}

	if c.DcaPercentThresholdBuyStr == "" {
		cfg.DcaPercentThresholdBuy = decimal.NewFromInt(1)
	} else {
		buyThreshold, err := decimal.NewFromString(c.DcaPercentThresholdBuyStr)
		if err != nil {
			return errors.Wrap(err, "incorrect 'dca_percent_threshold_buy' param in yaml config (must be a decimal)")
		}
		cfg.DcaPercentThresholdBuy = buyThreshold
	}

	if c.DcaPercentThresholdSellStr == "" {
		cfg.DcaPercentThresholdSell = decimal.NewFromInt(7)
	} else {
		sellThreshold, err := decimal.NewFromString(c.DcaPercentThresholdSellStr)
		if err != nil {
			return errors.Wrap(err, "incorrect 'dca_percent_threshold_sell' param in yaml config (must be a decimal)")
		}
		cfg.DcaPercentThresholdSell = sellThreshold
	}
	return nil
}

func parseAIConfig(c *ConfigTmp, cfg *Config) error {
	if c.LLMAPIKey == "" {
		return errors.New("'llm_api_key' is required for AI strategy")
	}
	cfg.LLMAPIKey = c.LLMAPIKey

	if c.LLMAPIURL == "" {
		cfg.LLMAPIURL = "https://openrouter.ai/api/v1/chat/completions"
	} else {
		cfg.LLMAPIURL = c.LLMAPIURL
	}

	if c.Model == "" {
		cfg.Model = "deepseek/deepseek-chat"
	} else {
		cfg.Model = c.Model
	}

	if c.PrimaryTimeframe == "" {
		cfg.PrimaryTimeframe = "3m"
	} else {
		cfg.PrimaryTimeframe = c.PrimaryTimeframe
	}

	if c.HigherTimeframe == "" {
		cfg.HigherTimeframe = "15m"
	} else {
		cfg.HigherTimeframe = c.HigherTimeframe
	}

	primaryLookbackRaw := c.PrimaryLookbackPeriodsStr
	if primaryLookbackRaw == "" {
		primaryLookbackRaw = c.LookbackPeriodsStr
	}
	if primaryLookbackRaw == "" {
		cfg.LookbackPeriods = 50
	} else {
		lookback, err := strconv.Atoi(primaryLookbackRaw)
		if err != nil {
			return errors.Wrap(err, "incorrect 'primary_lookback_periods' (or legacy 'lookback_periods') param")
		}
		if lookback < 50 {
			return fmt.Errorf("primary_lookback_periods must be at least 50, got %d", lookback)
		}
		cfg.LookbackPeriods = lookback
	}

	if c.HigherLookbackPeriodsStr == "" {
		cfg.HigherLookbackPeriods = 60
	} else {
		hLookback, err := strconv.Atoi(c.HigherLookbackPeriodsStr)
		if err != nil {
			return errors.Wrap(err, "incorrect 'higher_lookback_periods' param")
		}
		if hLookback < 20 {
			return fmt.Errorf("higher_lookback_periods must be at least 20, got %d", hLookback)
		}
		cfg.HigherLookbackPeriods = hLookback
	}

	if c.MaxLeverageStr == "" {
		cfg.MaxLeverage = 10
	} else {
		maxLeverage, err := strconv.Atoi(c.MaxLeverageStr)
		if err != nil {
			return errors.Wrap(err, "incorrect 'max_leverage' param")
		}
		if maxLeverage < 1 {
			return fmt.Errorf("max_leverage must be at least 1, got %d", maxLeverage)
		}
		cfg.MaxLeverage = maxLeverage
	}
	return nil
}

func getPairFromString(pairStr string) (entity.Pair, error) {
	pairElements := strings.Split(pairStr, "_")
	if len(pairElements) != 2 {
		return entity.Pair{}, fmt.Errorf("invalid pair param")
	}
	return entity.Pair{From: pairElements[0], To: pairElements[1]}, nil
}

func sanitizeStateKeyComponent(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "default"
	}
	return result
}
