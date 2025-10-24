package pricer

import (
	"context"
	"fmt"

	"github.com/adshao/go-binance/v2"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

// SimulatePricer fetches real market prices from Binance public API
// without requiring authentication
type SimulatePricer struct {
	client *binance.Client
}

// NewSimulatePricer creates a new simulate pricer that uses
// Binance public API for real market prices
func NewSimulatePricer(client *binance.Client) *SimulatePricer {
	return &SimulatePricer{client: client}
}

// GetPrice fetches the current market price from Binance public API
func (p *SimulatePricer) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	prices, err := p.client.NewListPricesService().Symbol(pair.Symbol()).Do(ctx)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if len(prices) == 0 {
		return decimal.Decimal{}, fmt.Errorf("binance API returned empty prices for %s", pair.String())
	}
	
	return decimal.NewFromString(prices[0].Price)
}
