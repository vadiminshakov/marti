package bot

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/vadiminshakov/marti/config"
	entity "github.com/vadiminshakov/marti/internal/domain"
)

// stubInitialBalanceTrader implements traderService plus initialBalanceProvider
// for publisher tests.
type stubInitialBalanceTrader struct {
	base           decimal.Decimal
	quote          decimal.Decimal
	initialBalance decimal.Decimal
	saved          *entity.BalanceSnapshot
}

func (s *stubInitialBalanceTrader) ExecuteAction(context.Context, entity.Action, decimal.Decimal, string) error {
	return nil
}
func (s *stubInitialBalanceTrader) OrderExecuted(context.Context, string) (bool, decimal.Decimal, error) {
	return true, decimal.Zero, nil
}
func (s *stubInitialBalanceTrader) OrderExists(context.Context, string) (bool, error) {
	return false, nil
}
func (s *stubInitialBalanceTrader) GetBalance(_ context.Context, currency string) (decimal.Decimal, error) {
	switch currency {
	case "BTC":
		return s.base, nil
	default:
		return s.quote, nil
	}
}
func (s *stubInitialBalanceTrader) GetPosition(context.Context, entity.Pair) (*entity.Position, error) {
	return nil, nil
}
func (s *stubInitialBalanceTrader) SetPositionStops(context.Context, entity.Pair, decimal.Decimal, decimal.Decimal) error {
	return nil
}
func (s *stubInitialBalanceTrader) InitialQuoteBalance() decimal.Decimal { return s.initialBalance }

type stubPricer struct{ price decimal.Decimal }

func (s *stubPricer) GetPrice(context.Context, entity.Pair) (decimal.Decimal, error) {
	return s.price, nil
}

type stubBalanceStore struct{ saved *entity.BalanceSnapshot }

func (s *stubBalanceStore) Save(snapshot entity.BalanceSnapshot) error {
	s.saved = &snapshot
	return nil
}

func TestPublishBalanceSnapshot_InitialQuoteFromTrader(t *testing.T) {
	trader := &stubInitialBalanceTrader{
		base:           decimal.Zero,
		quote:          decimal.NewFromInt(9500),
		initialBalance: decimal.NewFromInt(10000),
	}
	store := &stubBalanceStore{}
	b := &TradingBot{
		Config: config.Config{
			Pair:       entity.Pair{From: "BTC", To: "USDT"},
			MarketType: entity.MarketTypeSpot,
		},
		trader:       trader,
		pricer:       &stubPricer{price: decimal.NewFromInt(50000)},
		balanceStore: store,
		logger:       zap.NewNop(),
	}

	require.NoError(t, b.publishBalanceSnapshot(context.Background()))

	require.NotNil(t, store.saved)
	assert.Equal(t, "10000.00", store.saved.InitialQuote,
		"snapshot should carry the authoritative starting balance from the trader")
}

func TestPublishBalanceSnapshot_InitialQuoteEmptyForNonProvider(t *testing.T) {
	// Use the generated mock which does NOT implement initialBalanceProvider.
	trader := &stubInitialBalanceTraderNoProvider{
		base:  decimal.Zero,
		quote: decimal.NewFromInt(9500),
	}
	store := &stubBalanceStore{}
	b := &TradingBot{
		Config: config.Config{
			Pair:       entity.Pair{From: "BTC", To: "USDT"},
			MarketType: entity.MarketTypeSpot,
		},
		trader:       trader,
		pricer:       &stubPricer{price: decimal.NewFromInt(50000)},
		balanceStore: store,
		logger:       zap.NewNop(),
	}

	require.NoError(t, b.publishBalanceSnapshot(context.Background()))
	require.NotNil(t, store.saved)
	assert.Equal(t, "", store.saved.InitialQuote,
		"non-simulate traders should leave initial_quote empty so the dashboard falls back to its heuristic")
}

func TestPublishBalanceSnapshot_InitialQuoteCachedAcrossSnapshots(t *testing.T) {
	trader := &stubInitialBalanceTrader{
		base:           decimal.Zero,
		quote:          decimal.NewFromInt(9500),
		initialBalance: decimal.NewFromInt(10000),
	}
	store := &stubBalanceStore{}
	b := &TradingBot{
		Config: config.Config{
			Pair:       entity.Pair{From: "BTC", To: "USDT"},
			MarketType: entity.MarketTypeSpot,
		},
		trader:       trader,
		pricer:       &stubPricer{price: decimal.NewFromInt(50000)},
		balanceStore: store,
		logger:       zap.NewNop(),
	}

	require.NoError(t, b.publishBalanceSnapshot(context.Background()))
	require.NotNil(t, store.saved)
	assert.Equal(t, "10000.00", store.saved.InitialQuote)

	// Mutate the trader's reported initial balance after the first snapshot;
	// the bot should have cached the value and not re-read it.
	trader.initialBalance = decimal.NewFromInt(1)
	savedFirst := store.saved
	require.NoError(t, b.publishBalanceSnapshot(context.Background()))
	assert.Equal(t, savedFirst.InitialQuote, store.saved.InitialQuote,
		"initial_quote should be cached after first resolution")
}

// stubInitialBalanceTraderNoProvider implements traderService but NOT
// initialBalanceProvider, simulating a real exchange trader.
type stubInitialBalanceTraderNoProvider struct {
	base  decimal.Decimal
	quote decimal.Decimal
}

func (s *stubInitialBalanceTraderNoProvider) ExecuteAction(context.Context, entity.Action, decimal.Decimal, string) error {
	return nil
}
func (s *stubInitialBalanceTraderNoProvider) OrderExecuted(context.Context, string) (bool, decimal.Decimal, error) {
	return true, decimal.Zero, nil
}
func (s *stubInitialBalanceTraderNoProvider) OrderExists(context.Context, string) (bool, error) {
	return false, nil
}
func (s *stubInitialBalanceTraderNoProvider) GetBalance(_ context.Context, currency string) (decimal.Decimal, error) {
	if currency == "BTC" {
		return s.base, nil
	}
	return s.quote, nil
}
func (s *stubInitialBalanceTraderNoProvider) GetPosition(context.Context, entity.Pair) (*entity.Position, error) {
	return nil, nil
}
func (s *stubInitialBalanceTraderNoProvider) SetPositionStops(context.Context, entity.Pair, decimal.Decimal, decimal.Decimal) error {
	return nil
}
