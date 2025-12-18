package dca

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
)

// reconcileTradeIntents applies pending trade intents.
func (d *Strategy) reconcileTradeIntents(ctx context.Context) error {
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

			return err
		}
	}

	return nil
}

// processPendingIntent waits for intent execution and applies it.
func (d *Strategy) processPendingIntent(ctx context.Context, intent *tradeIntentRecord) error {
	// if already applied to series, ensure journal reflects done and return
	if d.isTradeProcessed(intent.ID) {
		_ = d.journal.MarkDone(intent)

		return nil
	}

	// defensive default for polling interval.
	if d.orderCheckInterval <= 0 {
		d.orderCheckInterval = defaultOrderCheckInterval
	}

	for {
		executed, filledBaseAmount, err := d.trader.OrderExecuted(ctx, intent.ID)
		if err != nil {
			return errors.Wrapf(err, "failed to check order execution for intent %s", intent.ID)
		}

		var filledQuoteAmount decimal.Decimal

		requestBaseAmount := intent.BaseAmount
		if requestBaseAmount.LessThanOrEqual(decimal.Zero) {
			requestBaseAmount = filledBaseAmount
		}

		switch intent.Action {
		case intentActionSell:
			if requestBaseAmount.GreaterThan(decimal.Zero) {
				filledQuoteAmount = intent.Amount.Mul(filledBaseAmount).Div(requestBaseAmount).Round(8)
			}
		default:
			filledQuoteAmount = filledBaseAmount.Mul(intent.Price).Round(8)
		}

		if intent.Action == intentActionBuy {
			if filledQuoteAmount.GreaterThan(decimal.Zero) && !filledQuoteAmount.Equal(intent.Amount.Round(8)) {
				_ = d.journal.UpdateAmount(intent, filledQuoteAmount)
				intent.Amount = filledQuoteAmount
			}
		}

		if !executed {
			timer := time.NewTimer(d.orderCheckInterval)
			select {
			case <-ctx.Done():
				timer.Stop()

				return errors.Wrap(ctx.Err(), "reconciliation canceled")
			case <-timer.C:
			}

			continue
		}

		d.l.Info("Order executed, applying to DCA series",
			zap.String("intent_id", intent.ID),
			zap.String("action", string(intent.Action)),
			zap.String("filled_amount_base", filledBaseAmount.String()),
			zap.String("filled_amount_quote", filledQuoteAmount.String()))

		if filledBaseAmount.GreaterThan(decimal.Zero) {
			intent.BaseAmount = filledBaseAmount
		}

		switch intent.Action {
		case intentActionBuy:
			// executed buy with zero amount is invalid -> mark failed.
			if filledBaseAmount.LessThanOrEqual(decimal.Zero) {
				if markErr := d.journal.MarkFailed(intent, errors.New("zero filled amount")); markErr != nil {
					return markErr
				}

				return nil
			}

			if applyErr := d.applyExecutedBuy(intent); applyErr != nil {
				return errors.Wrapf(applyErr, "failed to apply executed buy for intent %s", intent.ID)
			}
		case intentActionSell:
			if applyErr := d.applyExecutedSell(intent); applyErr != nil {
				return errors.Wrapf(applyErr, "failed to apply executed sell for intent %s", intent.ID)
			}
		default:
			return errors.Errorf("unknown intent action: %s", intent.Action)
		}

		// mark intent done after applying to series.
		if err := d.journal.MarkDone(intent); err != nil {
			return errors.Wrapf(err, "failed to mark intent as done: %s", intent.ID)
		}

		return nil
	}
}

// applyExecutedBuy applies executed buy.
func (d *Strategy) applyExecutedBuy(intent *tradeIntentRecord) error {
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
func (d *Strategy) applyExecutedSell(intent *tradeIntentRecord) error {
	if d.isTradeProcessed(intent.ID) {
		return nil
	}

	amountBaseCurrency := intent.BaseAmount
	totalBaseAmount := d.dcaSeries.TotalBaseAmount()
	if amountBaseCurrency.GreaterThan(totalBaseAmount) {
		amountBaseCurrency = totalBaseAmount
	}

	if amountBaseCurrency.LessThanOrEqual(decimal.Zero) {
		d.markTradeProcessed(intent.ID)

		return d.saveDCASeries()
	}

	isFullSell := intent.IsFullSell || amountBaseCurrency.Equal(totalBaseAmount)

	if isFullSell {
		d.l.Info("Full sell executed",
			zap.String("amountSoldBase", amountBaseCurrency.String()))
		d.resetDCASeries(intent.Price)
	} else {
		d.l.Info("Partial sell executed",
			zap.String("amountSoldBase", amountBaseCurrency.String()))
		d.dcaSeries.RemoveBaseAmount(amountBaseCurrency)
		d.tradePart = decimal.NewFromInt(int64(len(d.dcaSeries.Purchases)))

		// update LastSellPrice for step-by-step sell strategy
		d.updateSellState(intent.Price, false)

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
func (d *Strategy) resetDCASeries(sellPrice decimal.Decimal) {
	d.l.Info("Resetting DCA series and waiting for price drop",
		zap.String("lastSellPrice", sellPrice.String()),
		zap.String("requiredDropPercent", d.thresholds.BuyThresholdPercent.String()))

	processedIDs := d.dcaSeries.ProcessedTradeIDs
	if processedIDs == nil {
		processedIDs = make(map[string]bool)
	}

	d.dcaSeries = entity.NewDCASeries()
	d.dcaSeries.ProcessedTradeIDs = processedIDs
	d.tradePart = decimal.Zero
	d.updateSellState(sellPrice, true)
}
