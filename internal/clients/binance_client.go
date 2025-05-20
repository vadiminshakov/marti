package clients

import (
	"github.com/adshao/go-binance/v2"
)

// NewBinanceClient creates a new Binance client using the provided API key and secret
func NewBinanceClient(apiKey, apiSecret string) *binance.Client {
	client := binance.NewClient(apiKey, apiSecret)
	return client
}
