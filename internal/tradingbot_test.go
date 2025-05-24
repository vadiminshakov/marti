package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vadiminshakov/marti/config"
	"github.com/vadiminshakov/marti/internal/entity"
	"github.com/vadiminshakov/marti/internal/services"
	// "github.com/vadiminshakov/marti/internal/services/channel" // Removed
	// "github.com/vadiminshakov/marti/internal/services/detector" // Removed
	"github.com/vadiminshakov/marti/internal/services/pricer"
	"github.com/vadiminshakov/marti/internal/services/trader"
)

// Mock Trader
type mockTrader struct {
	trader.Trader // Embed interface
	buyFn         func(amount decimal.Decimal) error
	sellFn        func(amount decimal.Decimal) error
}

func (m *mockTrader) Buy(amount decimal.Decimal) error {
	if m.buyFn != nil {
		return m.buyFn(amount)
	}
	return nil
}
func (m *mockTrader) Sell(amount decimal.Decimal) error {
	if m.sellFn != nil {
		return m.sellFn(amount)
	}
	return nil
}

// Removed Mock Detector struct

// Mock Pricer
type mockPricer struct {
	pricer.Pricer // Embed interface
	getPriceFn    func(pair entity.Pair) (decimal.Decimal, error)
}

func (m *mockPricer) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
	if m.getPriceFn != nil {
		return m.getPriceFn(pair)
	}
	return decimal.Zero, nil
}

// Removed Mock ChannelFinder struct

// Removed Mock AnomalyDetector struct

// Store original constructors and replace them with mocks
var (
	originalNewBinanceTrader = trader.NewBinanceTrader
	// originalNewBinanceDetector  = detector.NewBinanceDetector // Removed
	originalNewBinancePricer   = pricer.NewBinancePricer
	// originalNewBinanceChannelFinder = channel.NewBinanceChannelFinder // Removed

	originalNewBybitTrader = trader.NewBybitTrader
	// originalNewBybitDetector  = detector.NewBybitDetector // Removed
	originalNewBybitPricer   = pricer.NewBybitPricer
	// originalNewBybitChannelFinder = channel.NewBybitChannelFinder // Removed
	
	originalNewTradeService = services.NewTradeService
	// originalNewAnomalyDetector = services.NewAnomalyDetector // Removed
)

func TestNewTradingBot(t *testing.T) {
	defaultConf := config.Config{
		Pair:                            entity.Pair{From: "BTC", To: "USDT"},
		// StatHours:                       5, // Removed, as it's no longer in config.Config
		Usebalance:                      decimal.NewFromInt(100), // Used as 'amount' for TradeService
		RebalanceInterval:               30 * time.Hour,
		PollPriceInterval:               1 * time.Minute,
		MaxDcaTrades:                    10,
		DcaPercentThresholdBuy:          decimal.NewFromInt(1),
		DcaPercentThresholdSell:         decimal.NewFromInt(5),
		// AnomalyDetectorBufferCap and AnomalyDetectorPercentThreshold removed from config
	}

	mockGoodTrader := &mockTrader{}
	// mockGoodDetector := &mockDetector{} // Removed
	mockGoodPricer := &mockPricer{}
	// mockGoodChannelFinder := &mockChannelFinder{} // Removed
	// mockGoodAnomalyDetector := &mockAnomalyDetector{} // Removed


	type constructorMocks struct {
		newTraderFn         func(client interface{}, pair entity.Pair) (trader.Trader, error)
		// newDetectorFn       func(client interface{}, usebalance decimal.Decimal, pair entity.Pair, buypoint, channel decimal.Decimal) (detector.Detector, error) // Removed
		newPricerFn         func(client interface{}) pricer.Pricer
		// newChannelFinderFn  func(client interface{}, pair entity.Pair, pastHours uint64) channel.ChannelFinder // Removed
		newTradeServiceErr  error
		// newAnomalyDetectorIsNil bool // Removed
	}

	setupMocks := func(platform string, mocks constructorMocks) {
		// Reset all to original first
		trader.NewBinanceTrader = originalNewBinanceTrader
		// detector.NewBinanceDetector = originalNewBinanceDetector // Removed
		pricer.NewBinancePricer = originalNewBinancePricer
		// channel.NewBinanceChannelFinder = originalNewBinanceChannelFinder // Removed
		trader.NewBybitTrader = originalNewBybitTrader
		// detector.NewBybitDetector = originalNewBybitDetector // Removed
		pricer.NewBybitPricer = originalNewBybitPricer
		// channel.NewBybitChannelFinder = originalNewBybitChannelFinder // Removed
		services.NewTradeService = originalNewTradeService
		// services.NewAnomalyDetector = originalNewAnomalyDetector // Removed


		if platform == "binance" {
			trader.NewBinanceTrader = func(_ *binance.Client, _ entity.Pair) (trader.Trader, error) {
				if mocks.newTraderFn != nil {
					return mocks.newTraderFn(nil, entity.Pair{}) // client and pair are not used by mock
				}
				return mockGoodTrader, nil
			}
			// detector.NewBinanceDetector removed
			pricer.NewBinancePricer = func(_ *binance.Client) pricer.Pricer {
				if mocks.newPricerFn != nil {
					return mocks.newPricerFn(nil)
				}
				return mockGoodPricer
			}
			// channel.NewBinanceChannelFinder removed
		} else if platform == "bybit" {
			trader.NewBybitTrader = func(_ *bybit.Client, _ entity.Pair) (trader.Trader, error) {
				if mocks.newTraderFn != nil {
					return mocks.newTraderFn(nil, entity.Pair{})
				}
				return mockGoodTrader, nil
			}
			// detector.NewBybitDetector removed
			pricer.NewBybitPricer = func(_ *bybit.Client) pricer.Pricer {
				if mocks.newPricerFn != nil {
					return mocks.newPricerFn(nil)
				}
				return mockGoodPricer
			}
			// channel.NewBybitChannelFinder removed
		}
		
		// Updated signature for mock NewTradeService (detector.Detector 'd' removed)
		services.NewTradeService = func(l *zap.Logger, pair entity.Pair, amount decimal.Decimal, p pricer.Pricer, tr trader.Trader, maxDcaTrades int, dcaBuy decimal.Decimal, dcaSell decimal.Decimal) (*services.TradeService, error) {
			if mocks.newTradeServiceErr != nil {
				return nil, mocks.newTradeServiceErr
			}
			// Return a dummy TradeService.
			return &services.TradeService{}, nil
		}
		// Removed logic related to newAnomalyDetectorIsNil and mocking services.NewAnomalyDetector

	}
	
	defer func() { // Restore all original functions after tests are done
		trader.NewBinanceTrader = originalNewBinanceTrader
		// detector.NewBinanceDetector = originalNewBinanceDetector // Removed
		pricer.NewBinancePricer = originalNewBinancePricer
		// channel.NewBinanceChannelFinder = originalNewBinanceChannelFinder // Removed
		trader.NewBybitTrader = originalNewBybitTrader
		// detector.NewBybitDetector = originalNewBybitDetector // Removed
		pricer.NewBybitPricer = originalNewBybitPricer
		// channel.NewBybitChannelFinder = originalNewBybitChannelFinder // Removed
		services.NewTradeService = originalNewTradeService
		// services.NewAnomalyDetector = originalNewAnomalyDetector // Removed
	}()

	tests := []struct {
		name          string
		platform      string
		client        interface{}
		mockSetup     constructorMocks
		expectError   bool
		expectedErrorMsg string
		checkBot      func(t *testing.T, bot *TradingBot, conf config.Config)
	}{
		{
			name:     "Success Binance",
			platform: "binance",
			client:   &binance.Client{},
			mockSetup: constructorMocks{
				newTraderFn: func(_ interface{}, _ entity.Pair) (trader.Trader, error) { return mockGoodTrader, nil },
				// newDetectorFn removed
				// Pricer and ChannelFinder are just assigned
			},
			expectError: false,
			checkBot: func(t *testing.T, bot *TradingBot, conf config.Config) {
				require.NotNil(t, bot)
				assert.Equal(t, conf, bot.Config)
				assert.NotNil(t, bot.Trader)
				// assert.NotNil(t, bot.Detector) // Removed
				assert.NotNil(t, bot.Pricer)
				// assert.NotNil(t, bot.ChannelFinder) // Removed
				assert.NotNil(t, bot.tradeService)
			},
		},
		{
			name:     "Success Bybit",
			platform: "bybit",
			client:   &bybit.Client{},
			mockSetup: constructorMocks{
				newTraderFn: func(_ interface{}, _ entity.Pair) (trader.Trader, error) { return mockGoodTrader, nil },
				// newDetectorFn removed
			},
			expectError: false,
			checkBot: func(t *testing.T, bot *TradingBot, conf config.Config) {
				require.NotNil(t, bot)
				assert.Equal(t, conf, bot.Config)
				assert.NotNil(t, bot.Trader)
				// assert.NotNil(t, bot.Detector) // Removed
				assert.NotNil(t, bot.Pricer)
				// assert.NotNil(t, bot.ChannelFinder) // Removed
				assert.NotNil(t, bot.tradeService)
			},
		},
		{
			name:     "Error Binance Trader",
			platform: "binance",
			client:   &binance.Client{},
			mockSetup: constructorMocks{
				newTraderFn: func(_ interface{}, _ entity.Pair) (trader.Trader, error) { return nil, errors.New("binance trader error") },
			},
			expectError:   true,
			expectedErrorMsg: "failed to create BinanceTrader: binance trader error",
		},
		// { // Removed Binance Detector Error Test
		// 	name:     "Error Binance Detector",
		// 	platform: "binance",
		// 	client:   &binance.Client{},
		// 	mockSetup: constructorMocks{
		// 		newDetectorFn: func(_ interface{}, _ decimal.Decimal, _ entity.Pair, _, _ decimal.Decimal) (detector.Detector, error) { return nil, errors.New("binance detector error") },
		// 	},
		// 	expectError:   true,
		// 	expectedErrorMsg: "failed to create BinanceDetector: binance detector error",
		// },
		{
			name:     "Error Bybit Trader",
			platform: "bybit",
			client:   &bybit.Client{},
			mockSetup: constructorMocks{
				newTraderFn: func(_ interface{}, _ entity.Pair) (trader.Trader, error) { return nil, errors.New("bybit trader error") },
			},
			expectError:   true,
			expectedErrorMsg: "failed to create BybitTrader: bybit trader error",
		},
		// { // Removed Bybit Detector Error Test
		// 	name:     "Error Bybit Detector",
		// 	platform: "bybit",
		// 	client:   &bybit.Client{},
		// 	mockSetup: constructorMocks{
		// 		newDetectorFn: func(_ interface{}, _ decimal.Decimal, _ entity.Pair, _, _ decimal.Decimal) (detector.Detector, error) { return nil, errors.New("bybit detector error") },
		// 	},
		// 	expectError:   true,
		// 	expectedErrorMsg: "failed to create BybitDetector: bybit detector error",
		// },
		{
			name:     "Error TradeService Init",
			platform: "binance", // Could be bybit too, error is in common path
			client:   &binance.Client{},
			mockSetup: constructorMocks{
				newTradeServiceErr: errors.New("trade service WAL error"),
			},
			expectError:   true,
			expectedErrorMsg: "failed to create TradeService: trade service WAL error",
		},
		{
			name:     "Error Unsupported Platform",
			platform: "kraken",
			client:   nil,
			mockSetup: constructorMocks{},
			expectError:   true,
			expectedErrorMsg: "unsupported platform: kraken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentConf := defaultConf
			currentConf.Platform = tt.platform
			setupMocks(tt.platform, tt.mockSetup)

			bot, err := NewTradingBot(currentConf, tt.client)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMsg)
				assert.Nil(t, bot)
			} else {
				require.NoError(t, err)
				require.NotNil(t, bot)
				if tt.checkBot != nil {
					tt.checkBot(t, bot, currentConf)
				}
			}
		})
	}
}

// Note: A mock for services.NewAnomalyDetector is less critical for error propagation
// as the current NewAnomalyDetector is a simple constructor that doesn't return an error.
// If it could error, a similar test case for it would be added.

// Example of how to run tests (not part of the file content)
// go test -v ./internal/...
// Ensure GOWAL_FAILPOIN_ERROR is not set, or WAL related tests in TradeService might fail.
// The TradeService mock above for NewTradingBot test bypasses WAL for unit testing NewTradingBot itself.
// Actual TradeService tests (including WAL) would be in tradeservice_test.go.

// Mock Run function for TradingBot to test its setup if needed.
// However, this subtask focuses on NewTradingBot, not its Run method.
func (b *TradingBot) MockRun(ctx context.Context, logger *zap.Logger) error {
	// This is a placeholder if we needed to test parts of Run.
	// For now, we test that tradeService is not nil.
	if b.tradeService == nil {
		return errors.New("tradeService not initialized")
	}
	// Simulate a short run or check
	return nil
}
