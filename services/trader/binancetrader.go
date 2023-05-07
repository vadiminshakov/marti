package trader

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/shopspring/decimal"
	"github.com/vadimInshakov/marti/entity"
)

type Trader struct {
	client *binance.Client
	pair   entity.Pair
}

func NewTrader(client *binance.Client, pair entity.Pair) (*Trader, error) {
	return &Trader{pair: pair, client: client}, nil
}

func (t *Trader) Buy(amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}

func (t *Trader) Sell(amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}
