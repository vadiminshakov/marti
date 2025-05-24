package config

import (
	"flag"
	"fmt"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"gopkg.in/yaml.v3"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Platform                        string
	Pair                            entity.Pair
	// StatHours                       uint64, // Removed
	Usebalance                      decimal.Decimal
	RebalanceInterval               time.Duration
	PollPriceInterval               time.Duration
	MaxDcaTrades            int
	DcaPercentThresholdBuy  decimal.Decimal
	DcaPercentThresholdSell decimal.Decimal
	// AnomalyDetectorBufferCap        uint             // Removed
	// AnomalyDetectorPercentThreshold decimal.Decimal  // Removed
}

type ConfigTmp struct {
	Pair                               string        `yaml:"pair"`
	// StatHours                          uint64        `yaml:"stat_hours"` // Removed
	Usebalance                         string        `yaml:"usebalance"`
	RebalanceInterval                  time.Duration `yaml:"rebalance_interval"`
	PollPriceInterval                  time.Duration `yaml:"poll_price_interval"`
	MaxDcaTradesStr                    string        `yaml:"max_dca_trades,omitempty"`
	DcaPercentThresholdBuyStr          string        `yaml:"dca_percent_threshold_buy,omitempty"`
	DcaPercentThresholdSellStr         string        `yaml:"dca_percent_threshold_sell,omitempty"`
	// AnomalyDetectorBufferCapStr        string        `yaml:"anomaly_detector_buffer_cap,omitempty"`        // Removed
	// AnomalyDetectorPercentThresholdStr string        `yaml:"anomaly_detector_percent_threshold,omitempty"` // Removed
}

func Get() ([]Config, error) {
	config := flag.String("config", "", "path to yaml config")
	flag.Parse()
	if *config != "" {
		return getYaml(*config)
	}

	// statHours removed from getFromCLI signature
	pair, usebalance, rebalanceInterval, pollPriceInterval, err := getFromCLI()
	if err != nil {
		return nil, err
	}

	// For CLI, we'll use the default DCA parameters. Anomaly Detector params removed.
	// StatHours also removed here.
	return []Config{
		{
			Pair:                    pair,
			// StatHours:               statHours, // Removed
			Usebalance:              usebalance,
			RebalanceInterval:       rebalanceInterval,
			PollPriceInterval:       pollPriceInterval,
			MaxDcaTrades:            15,                    // Default
			DcaPercentThresholdBuy:  decimal.NewFromInt(1), // Default
			DcaPercentThresholdSell: decimal.NewFromInt(7), // Default
			// AnomalyDetectorBufferCap:        20,                                // Removed
			// AnomalyDetectorPercentThreshold: decimal.NewFromInt(10),            // Removed
		},
	}, nil
}

// hours (StatHours) removed from signature
func getFromCLI() (pair entity.Pair, usebalance decimal.Decimal,
	rebalanceInterval, pollPriceInterval time.Duration, _ error) {
	pairFlag := flag.String("pair", "BTC_USDT", "trade pair, example: BTC_USDT")
	// statH := flag.Uint64("stathours", 5, "hours in past that will be used for stats count, example: 10") // Removed
	useb := flag.String("usebalance", "100", "percent of balance usage, for example 90 means 90%")
	ri := flag.Duration("rebalanceinterval", 30*time.Hour, "rebalance interval")
	pi := flag.Duration("pollpriceinterval", 5*time.Minute, "poll market price interval")

	flag.Parse()

	var err error
	pair, err = getPairFromString(*pairFlag)
	if err != nil {
		return entity.Pair{}, decimal.Decimal{}, 0, 0, fmt.Errorf("invalid --par provided, --pair=%s", *pairFlag)
	}
	usebalance, err = decimal.NewFromString(*useb)
	if err != nil {
		return entity.Pair{}, decimal.Decimal{}, 0, 0, err
	}

	// hours = *statH // Removed
	rebalanceInterval = *ri
	pollPriceInterval = *pi

	ub := usebalance.BigInt().Int64()

	if ub < 0 || ub > 100 {
		return entity.Pair{}, decimal.Decimal{}, 0, 0,
			fmt.Errorf("invalid --usebalance provided, --usebalance=%s", usebalance.String())
	}

	return pair, usebalance, rebalanceInterval, pollPriceInterval, nil
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
		usebalance, err := decimal.NewFromString(c.Usebalance)
		if err != nil {
			return nil, fmt.Errorf("incorrect 'usebalance' param in yaml config (correct format is 12), error: %w", err)
		}

		// Initialize newConfig correctly for each iteration.
		newConfig := Config{
			Pair:              pair,
			// StatHours:         c.StatHours, // Removed
			Usebalance:        usebalance,
			RebalanceInterval: c.RebalanceInterval,
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

		// Removed AnomalyDetectorBufferCap parsing
		// if c.AnomalyDetectorBufferCapStr == "" {
		// 	newConfig.AnomalyDetectorBufferCap = 20 // Default value
		// } else {
		// 	bufferCap, err := strconv.ParseUint(c.AnomalyDetectorBufferCapStr, 10, 32)
		// 	if err != nil {
		// 		return nil, fmt.Errorf("incorrect 'anomaly_detector_buffer_cap' param in yaml config (must be an unsigned integer), error: %w", err)
		// 	}
		// 	newConfig.AnomalyDetectorBufferCap = uint(bufferCap)
		// }

		// Removed AnomalyDetectorPercentThreshold parsing
		// if c.AnomalyDetectorPercentThresholdStr == "" {
		// 	newConfig.AnomalyDetectorPercentThreshold = decimal.NewFromInt(10) // Default value
		// } else {
		// 	anomalyThreshold, err := decimal.NewFromString(c.AnomalyDetectorPercentThresholdStr)
		// 	if err != nil {
		// 		return nil, fmt.Errorf("incorrect 'anomaly_detector_percent_threshold' param in yaml config (must be a decimal), error: %w", err)
		// 	}
		// 	newConfig.AnomalyDetectorPercentThreshold = anomalyThreshold
		// }

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
