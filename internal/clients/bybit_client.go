package clients

import (
	"github.com/hirokisan/bybit/v2"
)

func NewBybitClient(apiKey, apiSecret string) *bybit.Client {
	client := bybit.NewClient().WithAuth(apiKey, apiSecret)

	return client
}
