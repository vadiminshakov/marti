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
	"github.com/vadiminshakov/marti/internal/domain"
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
		// use margin order service for margin trading
		_, err := t.client.NewCreateMarginOrderService().Symbol(t.pair.Symbol()).
			Side(binance.SideTypeBuy).Type(binance.OrderTypeMarket).
			Quantity(amount.String()).
			NewClientOrderID(clientOrderID).Do(ctx)
		return err
	}

	// use regular spot order service
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
		// use margin order service for margin trading
		_, err := t.client.NewCreateMarginOrderService().Symbol(t.pair.Symbol()).
			Side(binance.SideTypeSell).Type(binance.OrderTypeMarket).
			Quantity(amount.String()).
			NewClientOrderID(clientOrderID).
			Do(ctx)
		return err
	}

	// use regular spot order service
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
		// query margin order
		order, err = t.client.NewGetMarginOrderService().
			Symbol(t.pair.Symbol()).
			OrigClientOrderID(clientOrderID).
			Do(ctx)
	} else {
		// query spot order
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
		// get margin account balance
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

	// get spot account balance
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

	// totalQty can be positive (long) or negative (short)
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
			// buying: increases position (towards long or closes short)
			if totalQty.LessThanOrEqual(decimal.Zero) {
				// opening long or starting to close short
				if totalQty.Equal(decimal.Zero) {
					entryTime = tradeTime
				}
			}

			// if closing a short position, reduce cost proportionally
			if totalQty.LessThan(decimal.Zero) {
				absQty := totalQty.Abs()
				reducedQty := qty
				if reducedQty.GreaterThan(absQty) {
					reducedQty = absQty
				}

				if absQty.GreaterThan(decimal.Zero) {
					avgCost := totalCost.Div(absQty)
					totalCost = totalCost.Sub(avgCost.Mul(reducedQty))
				}

				remainingQty := qty.Sub(reducedQty)
				if remainingQty.GreaterThan(decimal.Zero) {
					// flipping to long
					totalCost = price.Mul(remainingQty)
					entryTime = tradeTime
				}
			} else {
				// adding to long position
				totalCost = totalCost.Add(price.Mul(qty))
			}

			totalQty = totalQty.Add(qty)

			if totalQty.Equal(decimal.Zero) {
				totalCost = decimal.Zero
				entryTime = time.Time{}
			}
		} else {
			// selling: decreases position (closes long or opens short)
			if totalQty.GreaterThanOrEqual(decimal.Zero) {
				// closing long or starting to open short
				if totalQty.Equal(decimal.Zero) {
					entryTime = tradeTime
				}
			}

			// if closing a long position, reduce cost proportionally
			if totalQty.GreaterThan(decimal.Zero) {
				reducedQty := qty
				if reducedQty.GreaterThan(totalQty) {
					reducedQty = totalQty
				}

				if totalQty.GreaterThan(decimal.Zero) {
					avgCost := totalCost.Div(totalQty)
					totalCost = totalCost.Sub(avgCost.Mul(reducedQty))
				}

				remainingQty := qty.Sub(reducedQty)
				if remainingQty.GreaterThan(decimal.Zero) {
					// flipping to short
					totalCost = price.Mul(remainingQty)
					entryTime = tradeTime
				}
			} else {
				// adding to short position
				totalCost = totalCost.Add(price.Mul(qty))
			}

			totalQty = totalQty.Sub(qty)

			if totalQty.Equal(decimal.Zero) {
				totalCost = decimal.Zero
				entryTime = time.Time{}
			}
		}
	}

	// no position if totalQty is zero
	if totalQty.Equal(decimal.Zero) {
		return nil, nil
	}

	absQty := totalQty.Abs()
	if totalCost.LessThanOrEqual(decimal.Zero) {
		return nil, nil
	}

	avgPrice := totalCost.Div(absQty)
	if avgPrice.LessThanOrEqual(decimal.Zero) {
		return nil, nil
	}

	// determine position side
	side := entity.PositionSideLong
	if totalQty.LessThan(decimal.Zero) {
		side = entity.PositionSideShort
	}

	position, err := entity.NewPositionFromExternalSnapshot(absQty, avgPrice, entryTime, side)
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
		if err := t.placeBinanceProtectiveOrder(ctx, pair, quantity, takeProfit, binance.OrderTypeTakeProfit, binanceTakeProfitClientPrefix, position.Side); err != nil {
			return err
		}
	}

	if stopLoss.GreaterThan(decimal.Zero) {
		if err := t.placeBinanceProtectiveOrder(ctx, pair, quantity, stopLoss, binance.OrderTypeStopLoss, binanceStopLossClientPrefix, position.Side); err != nil {
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
	positionSide entity.PositionSide,
) error {
	clientOrderID := fmt.Sprintf("%s%d", clientIDPrefix, time.Now().UnixNano())

	// for long positions: protective orders are sells
	// for short positions: protective orders are buys
	side := binance.SideTypeSell
	if positionSide == entity.PositionSideShort {
		side = binance.SideTypeBuy
	}

	_, err := t.client.NewCreateMarginOrderService().
		Symbol(pair.Symbol()).
		Side(side).
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

// ExecuteAction executes a trading action (supports both long and short positions)
func (t *BinanceTrader) ExecuteAction(ctx context.Context, action entity.Action, amount decimal.Decimal, clientOrderID string) error {
	switch action {
	case entity.ActionOpenLong:
		return t.Buy(ctx, amount, clientOrderID)
	case entity.ActionCloseLong:
		return t.Sell(ctx, amount, clientOrderID)
	case entity.ActionOpenShort:
		// Opening short = selling
		return t.Sell(ctx, amount, clientOrderID)
	case entity.ActionCloseShort:
		// Closing short = buying
		return t.Buy(ctx, amount, clientOrderID)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}
