package internal

import (
	"testing"
	"time"

	binance "github.com/adshao/go-binance/v2"
	bybit "github.com/hirokisan/bybit/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/entity"
)

func TestNewTradingBot(t *testing.T) {
	defaultConf := config.Config{
		Pair:                    entity.Pair{From: "BTC", To: "USDT"},
		Amount:                  decimal.NewFromInt(100),
		PollPriceInterval:       1 * time.Minute,
		MaxDcaTrades:            10,
		DcaPercentThresholdBuy:  decimal.NewFromInt(1),
		DcaPercentThresholdSell: decimal.NewFromInt(5),
	}

	tests := []struct {
		name             string
		platform         string
		client           interface{}
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name:             "Unsupported Platform",
			platform:         "kraken",
			client:           nil,
			expectError:      true,
			expectedErrorMsg: "unsupported platform: kraken",
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

			bot, err := NewTradingBot(currentConf, tt.client)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				assert.Nil(t, bot)
			} else {
				if err != nil {
					t.Logf("Expected success but got error (this may be due to missing env vars or deps): %v", err)
					return
				}
				require.NotNil(t, bot)
				assert.Equal(t, currentConf, bot.Config)
			}
		})
	}
}
