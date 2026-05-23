package averaging

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
)

// reconcileTradeIntents finds all intents still in "pending" status and calls
// processPendingIntent for each one.  It is called at the start of Initialize
// and at the start of every Trade tick to catch fills that arrived while the
// process was stopped or between ticks.
//
// If any intent fails to reconcile, the error is returned and no further
// intents are processed that tick — the caller retries on the next tick.
func (d *Strategy) reconcileTradeIntents(ctx context.Context) error {
	if d.journal == nil {
		return nil
	}

	intents := d.journal.Intents()

	pending := make([]*tradeIntentRecord, 0, len(intents))

	for _, it := range intents {
		if it != nil && (it.Status == tradeIntentStatusPending || it.Status == tradeIntentStatusPrepare) {
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

// processPendingIntent polls the exchange until the order for intent is fully
// executed, then applies the fill to the cycle.
//
// If the intent was already applied to the cycle (isTradeProcessed returns
// true) it simply marks it done in the journal and returns — this handles the
// case where the process crashed after applying the fill but before writing
// "done" to the WAL.
//
// The polling loop blocks the caller and respects ctx cancellation.  Each
// iteration sleeps for orderCheckInterval before re-querying the exchange.
func (d *Strategy) processPendingIntent(ctx context.Context, intent *tradeIntentRecord) error {
	// intent is in "prepare" state: ExecuteAction may or may not have been called
	// before the crash. Ask the exchange to resolve the ambiguity.
	if intent.Status == tradeIntentStatusPrepare {
		exists, err := d.trader.OrderExists(ctx, intent.ID)
		if err != nil {
			return errors.Wrapf(err, "failed to check order existence for prepare intent %s", intent.ID)
		}

		if !exists {
			// order was never sent — safe to mark failed; next Trade tick creates a new one.
			d.l.Warn("Intent never submitted to exchange, marking failed",
				zap.String("intent_id", intent.ID),
				zap.String("action", string(intent.Action)))

			return d.journal.MarkFailed(intent, errors.New("order never submitted to exchange (pre-submit crash)"))
		}

		// order exists on exchange — promote to pending and fall through to normal poll.
		d.l.Info("Prepare intent found on exchange, promoting to pending",
			zap.String("intent_id", intent.ID))

		if err := d.journal.MarkSubmitted(intent); err != nil {
			return errors.Wrapf(err, "failed to promote prepare intent %s to pending", intent.ID)
		}
	}

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

// applyExecutedBuy records the confirmed buy fill in the cycle by calling
// AddPurchase.  The call is idempotent: if the intent ID is already in
// cycle.ProcessedTradeIDs it returns immediately without modifying the cycle.
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

	if err := d.AddPurchase(intent.ID, intent.Price, amountQuoteCurrency, intent.Time, intent.TradePart); err != nil {
		return errors.Wrap(err, "failed to add DCA purchase")
	}

	return nil
}

// applyExecutedSell removes the sold base amount from the cycle and persists
// the updated snapshot.
//
// Full sell (intent.IsFullSell or sold amount == total held): calls
// resetSeries, which clears all purchases and sets WaitingForDip = true.
//
// Partial sell: calls cycle.RemoveBaseAmount, updates tradePart, and sets
// LastSellPrice so the next sell threshold is calculated from the new price.
// If after the removal the cycle is empty (rounding exhausted the position),
// it also calls resetSeries to avoid a ghost cycle with zero balance.
//
// Like applyExecutedBuy, the call is idempotent via ProcessedTradeIDs.
func (d *Strategy) applyExecutedSell(intent *tradeIntentRecord) error {
	if d.isTradeProcessed(intent.ID) {
		return nil
	}

	amountBaseCurrency := intent.BaseAmount
	totalBaseAmount := d.cycle.TotalBaseAmount()
	if amountBaseCurrency.GreaterThan(totalBaseAmount) {
		amountBaseCurrency = totalBaseAmount
	}

	if amountBaseCurrency.LessThanOrEqual(decimal.Zero) {
		d.markTradeProcessed(intent.ID)

		return d.saveSeries()
	}

	isFullSell := intent.IsFullSell || amountBaseCurrency.Equal(totalBaseAmount)

	if isFullSell {
		d.l.Info("Full sell executed",
			zap.String("amountSoldBase", amountBaseCurrency.String()))
		d.resetSeries(intent.Price)
	} else {
		d.l.Info("Partial sell executed",
			zap.String("amountSoldBase", amountBaseCurrency.String()))
		d.cycle.RemoveBaseAmount(amountBaseCurrency)
		d.tradePart = decimal.NewFromInt(int64(len(d.cycle.Purchases)))

		// update LastSellPrice for step-by-step sell strategy
		d.updateSellState(intent.Price, false)

		// check if series is now empty after partial sell
		if len(d.cycle.Purchases) == 0 || d.cycle.TotalAmount.LessThanOrEqual(decimal.Zero) {
			d.l.Info("Total amount became zero after partial sell",
				zap.String("remainingTotalAmount", d.cycle.TotalAmount.String()))
			d.resetSeries(intent.Price)
		}
	}

	d.markTradeProcessed(intent.ID)

	return d.saveSeries()
}

// resetSeries clears all purchases and starts the post-sell waiting phase.
//
// It preserves ProcessedTradeIDs across the reset so that fills from the just-
// finished cycle are not re-applied if the WAL is replayed again.  After the
// reset, cycle.WaitingForDip == true and cycle.LastSellPrice == sellPrice, so
// the next Trade tick will wait for a dip before opening a new cycle.
func (d *Strategy) resetSeries(sellPrice decimal.Decimal) {
	d.l.Info("Resetting series and waiting for price drop",
		zap.String("lastSellPrice", sellPrice.String()),
		zap.String("requiredDropPercent", d.thresholds.BuyThresholdPercent.String()))

	processedIDs := d.cycle.ProcessedTradeIDs
	if processedIDs == nil {
		processedIDs = make(map[string]bool)
	}

	d.cycle = entity.NewAveragingSeries()
	d.cycle.ProcessedTradeIDs = processedIDs
	d.tradePart = decimal.Zero
	d.updateSellState(sellPrice, true)
}
