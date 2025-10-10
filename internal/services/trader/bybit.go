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

func (t *BybitTrader) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)
	orderLinkID := clientOrderID
	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:    "spot",
		Symbol:      bybit.SymbolV5(t.pair.Symbol()),
		Side:        bybit.SideBuy,
		OrderType:   bybit.OrderTypeMarket,
		Qty:         amount.String(),
		OrderLinkID: &orderLinkID,
		IsLeverage:  nil,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create buy order")
	}
	return nil
}

func (t *BybitTrader) Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)
	orderLinkID := clientOrderID
	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:    "spot",
		Symbol:      bybit.SymbolV5(t.pair.Symbol()),
		Side:        bybit.SideSell,
		OrderType:   bybit.OrderTypeMarket,
		Qty:         amount.String(),
		OrderLinkID: &orderLinkID,
		IsLeverage:  nil,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create sell order")
	}
	return nil
}

func (t *BybitTrader) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	orderLinkID := clientOrderID
	symbol := bybit.SymbolV5(t.pair.Symbol())

	openResp, err := t.client.V5().Order().GetOpenOrders(bybit.V5GetOpenOrdersParam{
		Category:    "spot",
		Symbol:      &symbol,
		OrderLinkID: &orderLinkID,
	})
	if err != nil {
		return false, decimal.Zero, errors.Wrap(err, "failed to get bybit open orders")
	}

	for _, order := range openResp.Result.List {
		if order.OrderLinkID == clientOrderID {
			filledQty, parseErr := decimal.NewFromString(order.CumExecQty)
			if parseErr != nil {
				return false, decimal.Zero, errors.Wrap(parseErr, "failed to parse cumulative executed quantity for open order")
			}
			return false, filledQty, nil
		}
	}

	historyResp, err := t.client.V5().Order().GetHistoryOrders(bybit.V5GetHistoryOrdersParam{
		Category:    "spot",
		Symbol:      &symbol,
		OrderLinkID: &orderLinkID,
	})
	if err != nil {
		return false, decimal.Zero, errors.Wrap(err, "failed to get bybit order history")
	}

	for _, order := range historyResp.Result.List {
		if order.OrderLinkID != clientOrderID {
			continue
		}

		filledQty, parseErr := decimal.NewFromString(order.CumExecQty)
		if parseErr != nil {
			return false, decimal.Zero, errors.Wrap(parseErr, "failed to parse cumulative executed quantity for historical order")
		}

		leavesQty := decimal.Zero
		if order.LeavesQty != "" {
			if leavesVal, err := decimal.NewFromString(order.LeavesQty); err == nil {
				leavesQty = leavesVal
			}
		}

		switch order.OrderStatus {
		case bybit.OrderStatusFilled, bybit.OrderStatusPartiallyFilled:
			if order.OrderStatus == bybit.OrderStatusPartiallyFilled && leavesQty.GreaterThan(decimal.Zero) {
				return false, filledQty, nil
			}
			if filledQty.GreaterThan(decimal.Zero) {
				return true, filledQty, nil
			}
			return false, decimal.Zero, nil
		case bybit.OrderStatusCancelled, bybit.OrderStatusRejected:
			if filledQty.GreaterThan(decimal.Zero) {
				// order cancelled after partial fill — report executed amount
				return true, filledQty, nil
			}
			return false, decimal.Zero, nil
		default:
			// continue checking open orders
		}
	}

	return false, decimal.Zero, nil
}
