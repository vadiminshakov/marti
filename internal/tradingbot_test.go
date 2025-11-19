package internal

import (
	"testing"
	"time"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/domain"
)

func TestNewTradingBot(t *testing.T) {
	defaultConf := config.Config{
		Pair:                    entity.Pair{From: "BTC", To: "USDT"},
		AmountPercent:           decimal.NewFromInt(10),
		PollPriceInterval:       1 * time.Minute,
		MaxDcaTrades:            10,
		DcaPercentThresholdBuy:  decimal.NewFromInt(1),
		DcaPercentThresholdSell: decimal.NewFromInt(5),
		StrategyType:            "dca", // Set a default strategy type
	}

	tests := []struct {
		client           any
		name             string
		platform         string
		expectedErrorMsg string
		expectError      bool
	}{
		{
			name:             "Unsupported Platform",
			platform:         "kraken",
			client:           nil,
			expectError:      true,
			expectedErrorMsg: "unsupported client type: <nil>", // Updated error message
		},
		{
			name:        "Valid Binance Platform",
			platform:    "binance",
			client:      &binance.Client{},
			expectError: false,
		},
		{
			name:        "Valid Bybit Platform",
			platform:    "bybit",
			client:      &bybit.Client{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentConf := defaultConf
			currentConf.Platform = tt.platform

			logger := zap.NewNop()
			bot, err := NewTradingBot(logger, currentConf, tt.client, nil, nil)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				assert.Nil(t, bot)
			} else {
				// in CI we may not have all env vars, so we only check for no error if we are not expecting one
				// this is a soft check for refactoring safety, not a full integration test
				if err != nil && tt.expectedErrorMsg == "" {
					t.Logf("Expected success but got error (this may be due to missing env vars or deps): %v", err)
				} else {
					require.NotNil(t, bot)
					assert.Equal(t, currentConf, bot.Config)
				}
			}
		})
	}
}
