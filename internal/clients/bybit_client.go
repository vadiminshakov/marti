package clients

import (
	"github.com/hirokisan/bybit/v2"
)

// NewBybitClient creates a new Bybit client using API keys from environment variables
func NewBybitClient(apiKey, apiSecret string) *bybit.Client {
	client := bybit.NewClient().WithAuth(apiKey, apiSecret)

	return client
}
