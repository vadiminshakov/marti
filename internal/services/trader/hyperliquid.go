package trader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/vadiminshakov/marti/internal/domain"
)

type HyperliquidTrader struct {
	ex          *hyperliquid.Exchange
	info        *hyperliquid.Info
	accountAddr string
	pair        domain.Pair
	marketType  domain.MarketType
	leverage    int
}

func NewHyperliquidTrader(ex *hyperliquid.Exchange, accountAddr string, pair domain.Pair, marketType domain.MarketType, leverage int) (*HyperliquidTrader, error) {
	if ex == nil {
		return nil, fmt.Errorf("hyperliquid exchange is nil")
	}
	t := &HyperliquidTrader{
		ex:          ex,
		info:        ex.Info(),
		accountAddr: accountAddr,
		pair:        pair,
		marketType:  marketType,
		leverage:    leverage,
	}

	// Configure leverage for perp (margin) trading if requested
	if marketType == domain.MarketTypeMargin && leverage > 1 {
		if _, err := ex.UpdateLeverage(context.Background(), leverage, pair.From, true); err != nil {
			// non-fatal; some assets may not support requested leverage immediately
			return nil, errors.Wrap(err, "failed to set leverage for hyperliquid")
		}
	}
	return t, nil
}

// convert a free-form client ID into a valid Hyperliquid cloid (0x + 32 hex chars)
func (t *HyperliquidTrader) cloidFromID(id string) string {
	s := strings.TrimSpace(id)
	if s == "" {
		// fallback: deterministic but simple
		s = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(s))
	// first 16 bytes -> 32 hex chars
	hexStr := hex.EncodeToString(sum[:16])
	return "0x" + hexStr
}

func (t *HyperliquidTrader) executeOrder(ctx context.Context, isBuy bool, amount decimal.Decimal, clientOrderID string, reduceOnly bool) error {
	// Convert base amount to float
	size, _ := amount.Round(8).Float64()

	// Compute a limit price with small slippage to emulate market order and TIF IOC
	px, err := t.ex.SlippagePrice(ctx, t.pair.From, isBuy, 0.005, nil) // 0.5% slippage
	if err != nil {
		return errors.Wrap(err, "slippage price")
	}

	cloid := t.cloidFromID(clientOrderID)
	req := hyperliquid.CreateOrderRequest{
		Coin:          t.pair.From,
		IsBuy:         isBuy,
		Price:         px,
		Size:          size,
		ReduceOnly:    reduceOnly,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	// Place single order
	_, err = t.ex.Order(ctx, req, nil)
	return err
}

// ExecuteAction executes a trading action (open/close long/short)
func (t *HyperliquidTrader) ExecuteAction(ctx context.Context, action domain.Action, amount decimal.Decimal, clientOrderID string) error {
	switch action {
	case domain.ActionOpenLong:
		return t.executeOrder(ctx, true, amount, clientOrderID, false)
	case domain.ActionCloseLong:
		// sell to close
		reduce := t.marketType == domain.MarketTypeMargin
		return t.executeOrder(ctx, false, amount, clientOrderID, reduce)
	case domain.ActionOpenShort:
		// Opening short = sell
		return t.executeOrder(ctx, false, amount, clientOrderID, false)
	case domain.ActionCloseShort:
		// Closing short = buy
		reduce := t.marketType == domain.MarketTypeMargin
		return t.executeOrder(ctx, true, amount, clientOrderID, reduce)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (t *HyperliquidTrader) OrderExecuted(ctx context.Context, clientOrderID string) (bool, decimal.Decimal, error) {
	if clientOrderID == "" {
		return false, decimal.Zero, nil
	}
	cloid := t.cloidFromID(clientOrderID)
	res, err := t.info.QueryOrderByCloid(ctx, t.accountAddr, cloid)
	if err != nil {
		return false, decimal.Zero, errors.Wrap(err, "query order by cloid")
	}

	if res == nil || res.Status != hyperliquid.OrderQueryStatusSuccess {
		return false, decimal.Zero, nil
	}

	switch res.Order.Status {
	case hyperliquid.OrderStatusValueFilled:
		// best-effort: use original size as filled amount when filled
		if res.Order.Order.OrigSz != "" {
			d, err := decimal.NewFromString(res.Order.Order.OrigSz)
			if err == nil {
				return true, d, nil
			}
		}
		return true, decimal.Zero, nil
	case hyperliquid.OrderStatusValueOpen:
		return false, decimal.Zero, nil
	case hyperliquid.OrderStatusValueCanceled,
		hyperliquid.OrderStatusValueRejected,
		hyperliquid.OrderStatusValueReduceOnlyCanceled,
		hyperliquid.OrderStatusValueScheduledCancel,
		hyperliquid.OrderStatusValueOpenInterestCapCanceled,
		hyperliquid.OrderStatusValueSelfTradeCanceled,
		hyperliquid.OrderStatusValueReduceOnlyRejected:
		// treat as not executed to allow strategy to handle
		return false, decimal.Zero, nil
	default:
		return false, decimal.Zero, nil
	}
}

func (t *HyperliquidTrader) GetBalance(ctx context.Context, currency string) (decimal.Decimal, error) {
	if t.marketType == domain.MarketTypeMargin {
		// Margin: approximate quote as total account value in USD; base is not modeled
		st, err := t.info.UserState(ctx, t.accountAddr)
		if err != nil {
			return decimal.Zero, errors.Wrap(err, "get user state")
		}

		if strings.EqualFold(currency, t.pair.To) {
			// Try TotalRawUsd, fallback to Withdrawable
			if st.MarginSummary.TotalRawUsd != "" {
				if d, err := decimal.NewFromString(st.MarginSummary.TotalRawUsd); err == nil {
					return d, nil
				}
			}
			if st.Withdrawable != "" {
				if d, err := decimal.NewFromString(st.Withdrawable); err == nil {
					return d, nil
				}
			}
			return decimal.Zero, nil
		}
		// base balance in perps isn't tracked like spot; return zero
		return decimal.Zero, nil
	}

	// Spot balances
	st, err := t.info.SpotUserState(ctx, t.accountAddr)
	if err != nil {
		return decimal.Zero, errors.Wrap(err, "get spot user state")
	}
	for _, b := range st.Balances {
		if strings.EqualFold(b.Coin, currency) {
			if d, err := decimal.NewFromString(b.Total); err == nil {
				return d, nil
			}
			break
		}
	}
	return decimal.Zero, nil
}

func (t *HyperliquidTrader) GetPosition(ctx context.Context, pair domain.Pair) (*domain.Position, error) {
	if t.marketType != domain.MarketTypeMargin {
		return nil, nil
	}
	st, err := t.info.UserState(ctx, t.accountAddr)
	if err != nil {
		return nil, errors.Wrap(err, "get user state")
	}
	for _, ap := range st.AssetPositions {
		if ap.Position.Coin != pair.From {
			continue
		}
		szi := strings.TrimSpace(ap.Position.Szi)
		if szi == "" || szi == "0" || szi == "0.0" {
			continue
		}
		size, err := decimal.NewFromString(szi)
		if err != nil || size.Equal(decimal.Zero) {
			continue
		}
		// Entry price may be nil if position doesn't exist
		var entryPrice decimal.Decimal
		if ap.Position.EntryPx != nil {
			if d, err := decimal.NewFromString(*ap.Position.EntryPx); err == nil {
				entryPrice = d
			}
		}
		if entryPrice.Equal(decimal.Zero) {
			// fetch current mid as fallback
			price, _ := t.ex.Info().AllMids(ctx)
			if m := price[pair.From]; m != "" {
				entryPrice, _ = decimal.NewFromString(m)
			}
		}
		side := domain.PositionSideLong
		if size.LessThan(decimal.Zero) {
			side = domain.PositionSideShort
			size = size.Abs()
		}
		pos, err := domain.NewPositionFromExternalSnapshot(size, entryPrice, time.Now(), side)
		if err != nil {
			return nil, errors.Wrap(err, "build position")
		}
		return pos, nil
	}
	return nil, nil
}

func (t *HyperliquidTrader) SetPositionStops(ctx context.Context, pair domain.Pair, takeProfit, stopLoss decimal.Decimal) error {
	if t.marketType != domain.MarketTypeMargin {
		return nil
	}

	// fetch current position
	pos, err := t.GetPosition(ctx, pair)
	if err != nil {
		return errors.Wrap(err, "fetch position for setting stops")
	}
	if pos == nil || pos.Amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	// Cancel existing position TP/SL triggers for this coin
	// Use frontend open orders API to find triggers for this coin
	open, err := t.info.FrontendOpenOrders(ctx, t.accountAddr)
	if err == nil && len(open) > 0 {
		var cancels []hyperliquid.CancelOrderRequest
		for _, o := range open {
			if !strings.EqualFold(o.Coin, pair.From) {
				continue
			}
			if !o.IsTrigger {
				continue
			}
			// cancel all triggers (including previous TP/SL) to replace them
			cancels = append(cancels, hyperliquid.CancelOrderRequest{Coin: pair.From, OrderID: o.Oid})
		}
		if len(cancels) > 0 {
			_, _ = t.ex.BulkCancel(ctx, cancels) // non-fatal — continue to place new stops
		}
	}

	// If neither TP nor SL set, we're done (just canceled existing ones)
	if takeProfit.LessThanOrEqual(decimal.Zero) && stopLoss.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	// determine order side for protective orders
	// For long: protective orders are buys (to close short) — on HL reduce-only, need opposite trade
	// Actually for long, closing is sell; for short, closing is buy.
	isLong := pos.Side == domain.PositionSideLong

	sizeF, _ := pos.Amount.Round(8).Float64()

	// prepare orders
	orders := make([]hyperliquid.CreateOrderRequest, 0, 2)

	addTrigger := func(px decimal.Decimal, tpsl hyperliquid.Tpsl) {
		if px.LessThanOrEqual(decimal.Zero) {
			return
		}
		priceF, _ := px.Round(8).Float64()
		isBuy := !isLong // long -> sell; short -> buy
		// SL: same side logic as TP — both close the position
		// (HL decides direction by side of order)
		cloid := t.cloidFromID(fmt.Sprintf("%s-%s-%s", pair.Symbol(), tpsl, time.Now().UTC().Format(time.RFC3339Nano)))
		orders = append(orders, hyperliquid.CreateOrderRequest{
			Coin:       pair.From,
			IsBuy:      isBuy,
			Price:      priceF, // not used for trigger market but required by wire
			Size:       sizeF,
			ReduceOnly: true,
			OrderType: hyperliquid.OrderType{
				Trigger: &hyperliquid.TriggerOrderType{
					TriggerPx: priceF,
					IsMarket:  true,
					Tpsl:      tpsl,
				},
			},
			ClientOrderID: &cloid,
		})
	}

	if takeProfit.GreaterThan(decimal.Zero) {
		addTrigger(takeProfit, hyperliquid.TakeProfit)
	}
	if stopLoss.GreaterThan(decimal.Zero) {
		addTrigger(stopLoss, hyperliquid.StopLoss)
	}

	if len(orders) == 0 {
		return nil
	}

	// place both orders
	if _, err := t.ex.BulkOrders(ctx, orders, nil); err != nil {
		return errors.Wrap(err, "place hyperliquid tpsl orders")
	}
	return nil
}
