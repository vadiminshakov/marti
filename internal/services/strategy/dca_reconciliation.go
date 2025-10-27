// This file contains reconciliation logic for the DCA strategy.
//
// Reconciliation is the process of recovering and bringing trade intents to a consistent state
// after application restarts or crashes. It handles:
//   - Processing pending trade intents
//   - Waiting for order completion
//   - Applying executed trades to the DCA series
//   - Validating and marking intents as done or failed
//
// This logic is separated from the main trading logic (dca.go) to improve code organization.
package strategy

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// reconcileTradeIntents processes all pending trade intents and brings them to a consistent state.
// This is called during initialization to recover from crashes or restarts.
func (d *DCAStrategy) reconcileTradeIntents(ctx context.Context) error {
	if d.journal == nil {
		return nil
	}

	for _, intent := range d.journal.Intents() {
		switch intent.Status {
		case tradeIntentStatusDone, tradeIntentStatusFailed:
			continue
		case tradeIntentStatusPending:
			if err := d.handlePendingIntent(ctx, intent); err != nil {
				return err
			}
		default:
			d.l.Warn("encountered trade intent with unknown status", zap.String("status", intent.Status), zap.String("intent_id", intent.ID))
		}
	}
	return nil
}

// handlePendingIntent processes a single pending intent, waiting for its completion and applying changes.
func (d *DCAStrategy) handlePendingIntent(ctx context.Context, intent *tradeIntentRecord) error {
	// if trade already processed (applied to series), just mark intent as done
	// this handles cases where app crashed between AddDCAPurchase and MarkDone
	if d.isTradeProcessed(intent.ID) {
		return d.ensureIntentMarkedDone(intent)
	}

	// wait for order completion with retries (waits indefinitely until executed)
	executed, filledAmount, err := d.waitForOrderCompletion(ctx, intent, d.orderCheckInterval)
	if err != nil {
		d.l.Error("failed to verify pending trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
		return err
	}

	if err := d.validateExecutedOrder(intent, executed, filledAmount); err != nil {
		return err
	}

	// check if validation marked intent as failed
	if intent.Status == tradeIntentStatusFailed {
		return nil
	}

	return d.reconcileExecutedIntent(intent, filledAmount)
}

// waitForOrderCompletion polls the order status until it's fully executed.
func (d *DCAStrategy) waitForOrderCompletion(ctx context.Context, intent *tradeIntentRecord, checkInterval time.Duration) (executed bool, filledAmount decimal.Decimal, err error) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		executed, filledAmount, err = d.trader.OrderExecuted(ctx, intent.ID)
		if err != nil {
			return false, decimal.Zero, err
		}

		// order fully executed
		if executed {
			return true, filledAmount, nil
		}

		// not fully executed yet - wait and retry indefinitely
		select {
		case <-ctx.Done():
			d.l.Info("context cancelled while waiting for order completion, stopping reconciliation",
				zap.String("intent_id", intent.ID))
			return false, filledAmount, ctx.Err()
		case <-ticker.C:
			// continue to next iteration
		}
	}
}

// validateExecutedOrder validates the execution result of an order.
func (d *DCAStrategy) validateExecutedOrder(intent *tradeIntentRecord, executed bool, filledAmount decimal.Decimal) error {
	// since waitForOrderCompletion waits indefinitely, executed should always be true here
	if !executed {
		// this should never happen, but handle defensively
		d.l.Error("unexpected: order not executed after waiting",
			zap.String("intent_id", intent.ID),
			zap.String("action", string(intent.Action)))
		d.markIntentFailed(intent, errors.New("order not executed after waiting"))
		return nil
	}

	// executed but with zero amount - should not happen, but handle defensively
	if filledAmount.LessThanOrEqual(decimal.Zero) {
		d.l.Warn("executed intent reported zero filled amount; marking as failed",
			zap.String("intent_id", intent.ID),
			zap.String("action", string(intent.Action)))
		d.markIntentFailed(intent, errors.New("filled amount reported as zero"))
		return nil
	}

	return nil
}

// ensureIntentMarkedDone ensures that an already processed intent is marked as done.
func (d *DCAStrategy) ensureIntentMarkedDone(intent *tradeIntentRecord) error {
	if intent.Status != tradeIntentStatusDone {
		if err := d.journal.MarkDone(intent); err != nil {
			d.l.Error("failed to persist completed trade intent", zap.Error(err), zap.String("intent_id", intent.ID))
			return err
		}
	}
	return nil
}

// reconcileExecutedIntent applies an executed intent and marks it as done.
func (d *DCAStrategy) reconcileExecutedIntent(intent *tradeIntentRecord, filledAmount decimal.Decimal) error {
	// update amount if it differs
	if !filledAmount.Equal(intent.Amount) {
		if err := d.journal.UpdateAmount(intent, filledAmount); err != nil {
			d.l.Error("failed to persist actual filled amount for intent",
				zap.Error(err),
				zap.String("intent_id", intent.ID),
				zap.String("old_amount", intent.Amount.String()),
				zap.String("filled_amount", filledAmount.String()))
			return err
		}
	}

	// apply and mark done
	if err := d.applyExecutedIntent(intent); err != nil {
		d.l.Error("failed to apply executed trade intent", zap.Error(err), zap.String("intent_id", intent.ID))
		return err
	}

	if err := d.journal.MarkDone(intent); err != nil {
		d.l.Error("failed to persist completed trade intent status", zap.Error(err), zap.String("intent_id", intent.ID))
		return err
	}

	return nil
}

// applyExecutedIntent applies the changes from an executed intent to the DCA series.
func (d *DCAStrategy) applyExecutedIntent(intent *tradeIntentRecord) error {
	switch intent.Action {
	case intentActionBuy:
		return d.AddDCAPurchase(intent.ID, intent.Price, intent.Amount, intent.Time, intent.TradePart)
	case intentActionSell:
		return d.applyExecutedSell(intent)
	default:
		return fmt.Errorf("unknown trade intent action: %s", intent.Action)
	}
}

// applyExecutedSell applies a sell intent to the DCA series.
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

	// Apply virtual trade if using simulation trader
	// Convert quote currency to base currency for simulation trader
	amountBaseCurrency := amountQuoteCurrency.Div(intent.Price)
	if err := d.applySimulationTrade(intent.Price, amountBaseCurrency, "sell"); err != nil {
		return err
	}

	d.markTradeProcessed(intent.ID)
	return d.saveDCASeries()
}

// resetDCASeries resets the DCA series after a full sell.
func (d *DCAStrategy) resetDCASeries(sellPrice decimal.Decimal) {
	d.l.Info("Resetting DCA series and waiting for price drop",
		zap.String("lastSellPrice", sellPrice.String()),
		zap.String("requiredDropPercent", d.dcaPercentThresholdBuy.String()))

	d.dcaSeries = &DCASeries{
		Purchases:         make([]DCAPurchase, 0),
		ProcessedTradeIDs: d.dcaSeries.ProcessedTradeIDs,
	}
	d.tradePart = decimal.Zero
	d.updateSellState(sellPrice, true)
}
