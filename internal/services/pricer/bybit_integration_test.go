//go:build integration

package pricer

import (
	"os"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vadiminshakov/marti/internal/clients"
	"github.com/vadiminshakov/marti/internal/entity"
)

// TestBybitPricer_GetPrice_Integration is an integration test that calls the real Bybit API
// To run this test, use: go test -tags=integration -v ./...
func TestBybitPricer_GetPrice_Integration(t *testing.T) {
	// Skip if not running integration tests
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a real Bybit client using the constructor
	apiKey := os.Getenv("BYBIT_API_KEY")
	apiSecret := os.Getenv("BYBIT_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		t.Fatal("BYBIT_API_KEY and BYBIT_API_SECRET environment variables must be set for integration tests")
	}

	client := clients.NewBybitClient(apiKey, apiSecret)
	pricer := NewBybitPricer(client)

	t.Run("returns price for BTC/USDT pair", func(t *testing.T) {
		pair := entity.Pair{From: "BTC", To: "USDT"}

		price, err := pricer.GetPrice(pair)
		require.NoError(t, err)
		require.True(t, price.GreaterThan(decimal.Zero), "Expected price > 0 for %s, got %s", pair.String(), price.String())
		t.Logf("Current %s price: %s", pair.String(), price.String())
	})

	t.Run("returns price for ETH/USDT pair", func(t *testing.T) {
		pair := entity.Pair{From: "ETH", To: "USDT"}

		price, err := pricer.GetPrice(pair)

		require.NoError(t, err)
		assert.True(t, price.GreaterThan(decimal.Zero), "Expected price > 0 for %s, got %s", pair.String(), price.String())
		t.Logf("Current %s price: %s", pair.String(), price.String())
	})

	t.Run("returns error for invalid trading pair", func(t *testing.T) {
		pair := entity.Pair{From: "INVALID", To: "PAIR"}

		price, err := pricer.GetPrice(pair)

		assert.Error(t, err, "Expected error for invalid pair")
		t.Logf("Error for invalid pair: %v", err)
		assert.True(t, price.IsZero(), "Expected zero price for invalid pair, got %s", price.String())
	})
}
