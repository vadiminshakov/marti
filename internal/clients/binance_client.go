package clients

import (
	"github.com/adshao/go-binance/v2"
)

func NewBinanceClient(apiKey, apiSecret string) *binance.Client {
	client := binance.NewClient(apiKey, apiSecret)
	return client
}
