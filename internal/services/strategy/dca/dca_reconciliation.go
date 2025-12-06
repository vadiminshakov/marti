package dca

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// reconcileTradeIntents applies pending trade intents.
func (d *DCAStrategy) reconcileTradeIntents(ctx context.Context) error {
	if d.journal == nil {
		return nil
	}

	intents := d.journal.Intents()
	pending := make([]*tradeIntentRecord, 0, len(intents))
	for _, it := range intents {
		if it != nil && it.Status == tradeIntentStatusPending {
			pending = append(pending, it)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	d.l.Info("Reconciling pending trade intents",
		zap.Int("count", len(pending)))

	for _, intent := range pending {
		if err := d.processPendingIntent(ctx, intent); err != nil {
			d.l.Error("Failed to process pending intent",
				zap.Error(err),
				zap.String("intent_id", intent.ID))

			continue
		}
	}

	return nil
}

// processPendingIntent polls until intent execution is applied.
func (d *DCAStrategy) processPendingIntent(ctx context.Context, intent *tradeIntentRecord) error {
	// if already applied to series, ensure journal reflects done and return
	if d.isTradeProcessed(intent.ID) {
		_ = d.journal.MarkDone(intent)
		return nil
	}

	// defensive default for polling interval
	if d.orderCheckInterval <= 0 {
		d.orderCheckInterval = defaultOrderCheckInterval
	}

	for {
		executed, filledAmount, err := d.trader.OrderExecuted(ctx, intent.ID)
		if err != nil {
			return errors.Wrapf(err, "failed to check order execution for intent %s", intent.ID)
		}

		// track partial fill progress by updating the intent amount in journal.
		// filledAmount from OrderExecuted is in BASE currency (e.g., BTC),
		// but intent.Amount is stored in QUOTE currency (e.g., USDT).
		// Convert filledAmount to quote using intent.Price.
		// Round to 8 decimal places to avoid floating point precision issues.
		filledQuoteAmount := filledAmount.Mul(intent.Price).Round(8)
		if filledQuoteAmount.GreaterThan(decimal.Zero) && !filledQuoteAmount.Equal(intent.Amount.Round(8)) {
			_ = d.journal.UpdateAmount(intent, filledQuoteAmount)
			intent.Amount = filledQuoteAmount
		}

		if !executed {
			// not yet completed — wait and retry
			time.Sleep(d.orderCheckInterval)
			continue
		}

		d.l.Info("Order executed, applying to DCA series",
			zap.String("intent_id", intent.ID),
			zap.String("action", string(intent.Action)),
			zap.String("filled_amount", filledAmount.String()))

		switch intent.Action {
		case intentActionBuy:
			// executed buy with zero amount is invalid → mark failed
			if filledAmount.LessThanOrEqual(decimal.Zero) {
				if err := d.journal.MarkFailed(intent, errors.New("zero filled amount")); err != nil {
					return err
				}
				return nil
			}
			if err := d.applyExecutedBuy(intent); err != nil {
				return errors.Wrapf(err, "failed to apply executed buy for intent %s", intent.ID)
			}
		case intentActionSell:
			if err := d.applyExecutedSell(intent); err != nil {
				return errors.Wrapf(err, "failed to apply executed sell for intent %s", intent.ID)
			}
		default:
			return errors.Errorf("unknown intent action: %s", intent.Action)
		}

		// mark intent done after applying to series
		if err := d.journal.MarkDone(intent); err != nil {
			return errors.Wrapf(err, "failed to mark intent as done: %s", intent.ID)
		}
		return nil
	}
}

// applyExecutedBuy applies executed buy.
func (d *DCAStrategy) applyExecutedBuy(intent *tradeIntentRecord) error {
	if d.isTradeProcessed(intent.ID) {
		return nil
	}

	// intent.Amount is in quote currency (e.g., USDT)
	amountQuoteCurrency := intent.Amount

	d.l.Info("Applying executed buy to DCA series",
		zap.String("intent_id", intent.ID),
		zap.String("price", intent.Price.String()),
		zap.String("amount", amountQuoteCurrency.String()),
		zap.Int("trade_part", intent.TradePart))

	if err := d.AddDCAPurchase(intent.ID, intent.Price, amountQuoteCurrency, intent.Time, intent.TradePart); err != nil {
		return errors.Wrap(err, "failed to add DCA purchase")
	}

	return nil
}

// applyExecutedSell applies executed sell.
func (d *DCAStrategy) applyExecutedSell(intent *tradeIntentRecord) error {
	if d.isTradeProcessed(intent.ID) {
		return nil
	}

	// intent.Amount is in quote currency (e.g., USDT)
	amountQuoteCurrency := intent.Amount
	if amountQuoteCurrency.GreaterThan(d.dcaSeries.TotalAmount) {
		amountQuoteCurrency = d.dcaSeries.TotalAmount
	}

	if amountQuoteCurrency.LessThanOrEqual(decimal.Zero) {
		d.markTradeProcessed(intent.ID)
		return d.saveDCASeries()
	}

	isFullSell := intent.IsFullSell || amountQuoteCurrency.Equal(d.dcaSeries.TotalAmount)

	if isFullSell {
		d.l.Info("Full sell executed", zap.String("amountSoldQuote", amountQuoteCurrency.String()))
		d.resetDCASeries(intent.Price)
	} else {
		d.l.Info("Partial sell executed", zap.String("amountSoldQuote", amountQuoteCurrency.String()))
		d.removeAmountFromPurchases(amountQuoteCurrency)

		// check if series is now empty after partial sell
		if len(d.dcaSeries.Purchases) == 0 || d.dcaSeries.TotalAmount.LessThanOrEqual(decimal.Zero) {
			d.l.Info("Total amount became zero after partial sell",
				zap.String("remainingTotalAmount", d.dcaSeries.TotalAmount.String()))
			d.resetDCASeries(intent.Price)
		}
	}

	d.markTradeProcessed(intent.ID)
	return d.saveDCASeries()
}

// resetDCASeries clears series.
func (d *DCAStrategy) resetDCASeries(sellPrice decimal.Decimal) {
	d.l.Info("Resetting DCA series and waiting for price drop",
		zap.String("lastSellPrice", sellPrice.String()),
		zap.String("requiredDropPercent", d.dcaPercentThresholdBuy.String()))

	processedIDs := d.dcaSeries.ProcessedTradeIDs
	if processedIDs == nil {
		processedIDs = make(map[string]bool)
	}

	d.dcaSeries = &DCASeries{
		Purchases:         make([]DCAPurchase, 0),
		ProcessedTradeIDs: processedIDs,
	}
	d.tradePart = decimal.Zero
	d.updateSellState(sellPrice, true)
}
