package binance

import (
	"context"
	"fmt"
	"github.com/adshao/go-binance/v2"
	"github.com/vadimInshakov/marti/entity"
	"math/big"
)

type Pricer struct {
	client    *binance.Client
	apikey    string
	secretkey string
}

func NewPricer(apikey, secretkey string) *Pricer {
	client := binance.NewClient(apikey, secretkey)
	return &Pricer{client: client}
}

func (p *Pricer) GetPrice(pair entity.Pair) (*big.Float, error) {
	prices, err := p.client.NewListPricesService().Symbol(pair.Symbol()).Do(context.Background())
	if err != nil {
		return nil, err
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("binance API returned empty prices for %s", pair.String())
	}

	f, _ := (&big.Float{}).SetString(prices[0].Price)
	return f, nil
}
