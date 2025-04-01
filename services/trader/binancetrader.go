package trader

import (
	"context"
	"github.com/adshao/go-binance/v2"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

type BinanceTrader struct {
	client *binance.Client
	pair   entity.Pair
}

func NewBinanceTrader(client *binance.Client, pair entity.Pair) (*BinanceTrader, error) {
	return &BinanceTrader{pair: pair, client: client}, nil
}

func (t *BinanceTrader) Buy(amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}

func (t *BinanceTrader) Sell(amount decimal.Decimal) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}
