package trader

import (
	"context"

	"github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

type BybitTrader struct {
	client *bybit.Client
	pair   entity.Pair
}

func NewBybitTrader(client *bybit.Client, pair entity.Pair) (*BybitTrader, error) {
	return &BybitTrader{pair: pair, client: client}, nil
}

func (t *BybitTrader) Buy(ctx context.Context, amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:   "spot",
		Symbol:     bybit.SymbolV5(t.pair.Symbol()),
		Side:       bybit.SideBuy,
		OrderType:  bybit.OrderTypeMarket,
		Qty:        amount.String(),
		IsLeverage: nil,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create buy order")
	}
	return nil
}

func (t *BybitTrader) Sell(ctx context.Context, amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:   "spot",
		Symbol:     bybit.SymbolV5(t.pair.Symbol()),
		Side:       bybit.SideSell,
		OrderType:  bybit.OrderTypeMarket,
		Qty:        amount.String(),
		IsLeverage: nil,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create sell order")
	}
	return nil
}
