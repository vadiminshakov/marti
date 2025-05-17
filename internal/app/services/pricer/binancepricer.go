package pricer

import (
	"context"
	"fmt"
	"github.com/adshao/go-binance/v2"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/app/entity"
)

type BinancePricer struct {
	client *binance.Client
}

func NewBinancePricer(client *binance.Client) *BinancePricer {
	return &BinancePricer{client: client}
}

func (p *BinancePricer) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
	prices, err := p.client.NewListPricesService().Symbol(pair.Symbol()).Do(context.Background())
	if err != nil {
		return decimal.Decimal{}, err
	}
	if len(prices) == 0 {
		return decimal.Decimal{}, fmt.Errorf("binance API returned empty prices for %s", pair.String())
	}

	return decimal.NewFromString(prices[0].Price)
}
