package trader

import (
	"context"
	"fmt"

	"github.com/hirokisan/bybit/v2"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

type BybitTrader struct {
	client     *bybit.Client
	pair       entity.Pair
	marketType entity.MarketType
	leverage   int
}

func NewBybitTrader(client *bybit.Client, pair entity.Pair, marketType entity.MarketType, leverage int) (*BybitTrader, error) {
	trader := &BybitTrader{
		pair:       pair,
		client:     client,
		marketType: marketType,
		leverage:   leverage,
	}

	// For margin trading (linear category), set leverage via API
	if marketType == entity.MarketTypeMargin && leverage > 1 {
		err := trader.setLeverage()
		if err != nil {
			return nil, errors.Wrap(err, "failed to set leverage for bybit margin trading")
		}
	}

	return trader, nil
}

// mapMarketTypeToCategory converts internal MarketType to Bybit's CategoryV5
func (t *BybitTrader) mapMarketTypeToCategory() bybit.CategoryV5 {
	switch t.marketType {
	case entity.MarketTypeMargin:
		return bybit.CategoryV5Linear
	default:
		return bybit.CategoryV5Spot
	}
}

// setLeverage sets the leverage for margin trading (linear category only)
func (t *BybitTrader) setLeverage() error {
	if t.marketType != entity.MarketTypeMargin {
		return nil
	}

	leverageStr := fmt.Sprintf("%d", t.leverage)
	_, err := t.client.V5().Position().SetLeverage(bybit.V5SetLeverageParam{
		Category:     bybit.CategoryV5Linear,
		Symbol:       bybit.SymbolV5(t.pair.Symbol()),
		BuyLeverage:  leverageStr,
		SellLeverage: leverageStr,
	})

	return err
}

func (t *BybitTrader) Buy(ctx context.Context, amount decimal.Decimal, clientOrderID string) error {
	amount = amount.RoundFloor(4)
	orderLinkID := clientOrderID
	category := t.mapMarketTypeToCategory()

	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:    category,
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
	category := t.mapMarketTypeToCategory()

	_, err := t.client.V5().Order().CreateOrder(bybit.V5CreateOrderParam{
		Category:    category,
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
	category := t.mapMarketTypeToCategory()

	openResp, err := t.client.V5().Order().GetOpenOrders(bybit.V5GetOpenOrdersParam{
		Category:    category,
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
		Category:    category,
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

func (t *BybitTrader) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	// Choose the correct account type based on market type
	accountType := bybit.AccountTypeV5SPOT
	if t.marketType == entity.MarketTypeMargin {
		accountType = bybit.AccountTypeV5CONTRACT
	}

	resp, err := t.client.V5().Account().GetWalletBalance(accountType, []bybit.Coin{bybit.Coin(currency)})
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "failed to get bybit wallet balance")
	}

	if len(resp.Result.List) == 0 {
		return decimal.Zero, nil
	}

	for _, wallet := range resp.Result.List {
		for _, coin := range wallet.Coin {
			if string(coin.Coin) == currency {
				balance, err := decimal.NewFromString(coin.WalletBalance)
				if err != nil {
					return decimal.Zero, errors.Wrap(err, "failed to parse balance")
				}
				return balance, nil
			}
		}
	}

	return decimal.Zero, nil
}
