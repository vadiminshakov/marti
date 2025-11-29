package clients

import (
	"github.com/adshao/go-binance/v2"
)

// SimulateClient wraps a real exchange client for price data.
type SimulateClient struct {
	// use Binance public API for real market prices
	binanceClient *binance.Client
}

// NewSimulateClient creates a new simulate client.
func NewSimulateClient() *SimulateClient {
	// create client without API keys for public data only
	client := binance.NewClient("", "")
	return &SimulateClient{
		binanceClient: client,
	}
}

// GetBinanceClient returns the underlying Binance client.
func (c *SimulateClient) GetBinanceClient() *binance.Client {
	return c.binanceClient
}
