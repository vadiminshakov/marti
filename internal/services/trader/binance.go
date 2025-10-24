package trader

import (
	"context"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/common"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

type BinanceTrader struct {
	client *binance.Client
	pair   entity.Pair
}

func NewBinanceTrader(client *binance.Client, pair entity.Pair) (*BinanceTrader, error) {
	return &BinanceTrader{pair: pair, client: client}, nil
}

func (t *BinanceTrader) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		NewClientOrderID(clientOrderID).
		Do(ctx)

	return err
}

func (t *BinanceTrader) Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		NewClientOrderID(clientOrderID).
		Do(ctx)

	return err
}

func (t *BinanceTrader) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	order, err := t.client.NewGetOrderService().
		Symbol(t.pair.Symbol()).
		OrigClientOrderID(clientOrderID).
		Do(ctx)
	if err != nil {
		if apiErr, ok := err.(*common.APIError); ok && apiErr.Code == -2013 {
			// order does not exist
			return false, decimal.Zero, nil
		}
		return false, decimal.Zero, errors.Wrap(err, "failed to query binance order status")
	}

	executedQty, parseErr := decimal.NewFromString(order.ExecutedQuantity)
	if parseErr != nil {
		return false, decimal.Zero, errors.Wrap(parseErr, "failed to parse executed quantity")
	}

	switch order.Status {
	case binance.OrderStatusTypeFilled, binance.OrderStatusTypePartiallyFilled:
		if order.Status == binance.OrderStatusTypeFilled {
			return true, executedQty, nil
		}
		// partial fill still active
		return false, executedQty, nil
	case binance.OrderStatusTypeCanceled, binance.OrderStatusTypeRejected, binance.OrderStatusTypeExpired:
		if executedQty.GreaterThan(decimal.Zero) {
			return true, executedQty, nil
		}
		return false, decimal.Zero, nil
	default:
		if executedQty.GreaterThan(decimal.Zero) {
			return false, executedQty, nil
		}
		return false, decimal.Zero, nil
	}
}

func (t *BinanceTrader) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	account, err := t.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "failed to get binance account balance")
	}

	for _, balance := range account.Balances {
		if balance.Asset == currency {
			free, err := decimal.NewFromString(balance.Free)
			if err != nil {
				return decimal.Zero, errors.Wrap(err, "failed to parse balance")
			}
			return free, nil
		}
	}

	return decimal.Zero, nil
}
