package pricer

import (
	"context"
	"fmt"

	"github.com/hirokisan/bybit/v2"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
)

type BybitPricer struct {
	client *bybit.Client
}

func NewBybitPricer(client *bybit.Client) *BybitPricer {
	return &BybitPricer{client: client}
}

func (p *BybitPricer) GetPrice(ctx context.Context, pair entity.Pair) (decimal.Decimal, error) {
	symbol := bybit.SymbolV5(pair.Symbol())

	result, err := p.client.V5().Market().GetTickers(bybit.V5GetTickersParam{
		Category: "spot",
		Symbol:   &symbol,
	})
	if err != nil {
		return decimal.Decimal{}, err
	}

	if len(result.Result.Spot.List) == 0 {
		return decimal.Decimal{}, fmt.Errorf("bybit API returned empty prices for %s", pair.String())
	}

	return decimal.NewFromString(result.Result.Spot.List[0].LastPrice)
}
