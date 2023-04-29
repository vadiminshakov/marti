package trader

import (
	"context"
	"fmt"
	"math/big"

	"github.com/adshao/go-binance/v2"
	"github.com/vadimInshakov/marti/entity"
)

type Trader struct {
	client *binance.Client
	pair   entity.Pair
}

func NewTrader(client *binance.Client, pair entity.Pair) (*Trader, error) {
	return &Trader{pair: pair, client: client}, nil
}

func (t *Trader) Buy(amount *big.Float) error {
	fmt.Println(amount.String())
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}

func (t *Trader) Sell(amount *big.Float) error {
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		Do(context.Background())

	return err
}