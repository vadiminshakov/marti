package trader

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/common"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

type BinanceTrader struct {
	client     *binance.Client
	pair       entity.Pair
	marketType entity.MarketType
	leverage   int
}

const (
	binanceStopLossClientPrefix   = "marti-sl-"
	binanceTakeProfitClientPrefix = "marti-tp-"
)

func NewBinanceTrader(client *binance.Client, pair entity.Pair, marketType entity.MarketType, leverage int) (*BinanceTrader, error) {
	return &BinanceTrader{
		pair:       pair,
		client:     client,
		marketType: marketType,
		leverage:   leverage,
	}, nil
}

func (t *BinanceTrader) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)

	if t.marketType == entity.MarketTypeMargin {
		// Use margin order service for margin trading
		_, err := t.client.NewCreateMarginOrderService().Symbol(t.pair.Symbol()).
			Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
			Quantity(amount.String()).
			NewClientOrderID(clientOrderID).
			Do(ctx)
		return err
	}

	// Use regular spot order service
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		NewClientOrderID(clientOrderID).
		Do(ctx)
	return err
}

func (t *BinanceTrader) Sell(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)

	if t.marketType == entity.MarketTypeMargin {
		// Use margin order service for margin trading
		_, err := t.client.NewCreateMarginOrderService().Symbol(t.pair.Symbol()).
			Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
			Quantity(amount.String()).
			NewClientOrderID(clientOrderID).
			Do(ctx)
		return err
	}

	// Use regular spot order service
	_, err := t.client.NewCreateOrderService().Symbol(t.pair.Symbol()).
		Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
		Quantity(amount.String()).
		NewClientOrderID(clientOrderID).
		Do(ctx)
	return err
}

func (t *BinanceTrader) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	var order *binance.Order
	var err error

	if t.marketType == entity.MarketTypeMargin {
		// Query margin order
		order, err = t.client.NewGetMarginOrderService().
			Symbol(t.pair.Symbol()).
			OrigClientOrderID(clientOrderID).
			Do(ctx)
	} else {
		// Query spot order
		order, err = t.client.NewGetOrderService().
			Symbol(t.pair.Symbol()).
			OrigClientOrderID(clientOrderID).
			Do(ctx)
	}

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
	if t.marketType == entity.MarketTypeMargin {
		// Get margin account balance
		marginAccount, err := t.client.NewGetMarginAccountService().Do(ctx)
		if err != nil {
			return decimal.Zero, errors.Wrap(err, "failed to get binance margin account balance")
		}

		for _, asset := range marginAccount.UserAssets {
			if asset.Asset == currency {
				free, err := decimal.NewFromString(asset.Free)
				if err != nil {
					return decimal.Zero, errors.Wrap(err, "failed to parse margin balance")
				}
				return free, nil
			}
		}
		return decimal.Zero, nil
	}

	// Get spot account balance
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

func (t *BinanceTrader) GetPosition(ctx context.Context, pair entity.Pair) (*entity.Position, error) {
	if t.marketType != entity.MarketTypeMargin {
		return nil, nil
	}

	trades, err := t.client.NewListMarginTradesService().
		Symbol(pair.Symbol()).
		Do(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list binance margin trades")
	}

	if len(trades) == 0 {
		return nil, nil
	}

	sort.Slice(trades, func(i, j int) bool {
		return trades[i].Time < trades[j].Time
	})

	totalQty := decimal.Zero
	totalCost := decimal.Zero
	var entryTime time.Time

	for _, trade := range trades {
		qty, parseErr := decimal.NewFromString(trade.Quantity)
		if parseErr != nil {
			return nil, errors.Wrap(parseErr, "failed to parse trade quantity")
		}

		price, parseErr := decimal.NewFromString(trade.Price)
		if parseErr != nil {
			return nil, errors.Wrap(parseErr, "failed to parse trade price")
		}

		tradeTime := time.UnixMilli(trade.Time)

		if trade.IsBuyer {
			if totalQty.LessThanOrEqual(decimal.Zero) {
				entryTime = tradeTime
			}
			totalCost = totalCost.Add(price.Mul(qty))
			totalQty = totalQty.Add(qty)
		} else {
			if totalQty.LessThanOrEqual(decimal.Zero) {
				continue
			}

			reducedQty := qty
			if reducedQty.GreaterThan(totalQty) {
				reducedQty = totalQty
			}

			if totalQty.GreaterThan(decimal.Zero) {
				avgCost := decimal.Zero
				if !totalCost.Equal(decimal.Zero) {
					avgCost = totalCost.Div(totalQty)
				}
				totalCost = totalCost.Sub(avgCost.Mul(reducedQty))
			}

			totalQty = totalQty.Sub(reducedQty)
			if totalQty.LessThanOrEqual(decimal.Zero) {
				totalQty = decimal.Zero
				totalCost = decimal.Zero
				entryTime = time.Time{}
			}
		}
	}

	if totalQty.LessThanOrEqual(decimal.Zero) {
		return nil, nil
	}

	if totalCost.LessThanOrEqual(decimal.Zero) {
		return nil, nil
	}

	avgPrice := totalCost.Div(totalQty)
	if avgPrice.LessThanOrEqual(decimal.Zero) {
		return nil, nil
	}

	position, err := entity.NewPositionFromExternalSnapshot(totalQty, avgPrice, entryTime)
	if err != nil {
		return nil, errors.Wrap(err, "failed to construct position snapshot")
	}

	return position, nil
}

func (t *BinanceTrader) SetPositionStops(ctx context.Context, pair entity.Pair, takeProfit, stopLoss decimal.Decimal) error {
	if t.marketType != entity.MarketTypeMargin {
		return nil
	}

	position, err := t.GetPosition(ctx, pair)
	if err != nil {
		return errors.Wrap(err, "failed to fetch binance margin position for stop updates")
	}
	if position == nil || position.Amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	quantity := position.Amount.RoundFloor(4)
	if quantity.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	openOrders, err := t.client.NewListMarginOpenOrdersService().
		Symbol(pair.Symbol()).
		Do(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list existing binance margin orders")
	}

	if err := t.cancelBinanceProtectiveOrders(ctx, pair, openOrders); err != nil {
		return err
	}

	if takeProfit.GreaterThan(decimal.Zero) {
		if err := t.placeBinanceProtectiveOrder(ctx, pair, quantity, takeProfit, binance.OrderTypeTakeProfit, binanceTakeProfitClientPrefix); err != nil {
			return err
		}
	}

	if stopLoss.GreaterThan(decimal.Zero) {
		if err := t.placeBinanceProtectiveOrder(ctx, pair, quantity, stopLoss, binance.OrderTypeStopLoss, binanceStopLossClientPrefix); err != nil {
			return err
		}
	}

	return nil
}

func (t *BinanceTrader) cancelBinanceProtectiveOrders(ctx context.Context, pair entity.Pair, orders []*binance.Order) error {
	for _, order := range orders {
		if order == nil {
			continue
		}
		if !strings.HasPrefix(order.ClientOrderID, binanceStopLossClientPrefix) &&
			!strings.HasPrefix(order.ClientOrderID, binanceTakeProfitClientPrefix) {
			continue
		}

		_, err := t.client.NewCancelMarginOrderService().
			Symbol(pair.Symbol()).
			OrigClientOrderID(order.ClientOrderID).
			Do(ctx)
		if err != nil {
			if apiErr, ok := err.(*common.APIError); ok {
				if apiErr.Code == -2011 || apiErr.Code == -2013 {
					continue
				}
			}
			return errors.Wrapf(err, "failed to cancel binance protective order %s", order.ClientOrderID)
		}
	}
	return nil
}

func (t *BinanceTrader) placeBinanceProtectiveOrder(
	ctx context.Context,
	pair entity.Pair,
	quantity decimal.Decimal,
	price decimal.Decimal,
	orderType binance.OrderType,
	clientIDPrefix string,
) error {
	clientOrderID := fmt.Sprintf("%s%d", clientIDPrefix, time.Now().UnixNano())

	_, err := t.client.NewCreateMarginOrderService().
		Symbol(pair.Symbol()).
		Side(binance.SideTypeSell).
		Type(orderType).
		Quantity(quantity.String()).
		StopPrice(price.String()).
		NewClientOrderID(clientOrderID).
		Do(ctx)
	if err != nil {
		return errors.Wrapf(err, "failed to place binance %s order", orderType)
	}

	return nil
}
