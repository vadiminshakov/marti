package decisions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	domain "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/pkg/telegram"
)

// NotifyingStore wraps WALStore and sends Telegram notifications on trade decisions.
// Notification failures are logged and never propagate as errors.
type NotifyingStore struct {
	inner  *WALStore
	tg     *telegram.Notifier
	logger *zap.Logger
}

// NewNotifyingStore wraps inner with Telegram notifications.
// When tg is nil or not configured, behaviour is identical to inner.
func NewNotifyingStore(inner *WALStore, tg *telegram.Notifier, logger *zap.Logger) *NotifyingStore {
	return &NotifyingStore{inner: inner, tg: tg, logger: logger}
}

// SaveAI delegates to inner and notifies for actionable decisions (not "hold").
func (s *NotifyingStore) SaveAI(event domain.AIDecisionEvent) error {
	if err := s.inner.SaveAI(event); err != nil {
		return err
	}

	if event.Action != "hold" {
		go s.notify(formatAIMessage(event))
	}

	return nil
}

// SaveDCA delegates to inner and always notifies (DCA events are always buy or sell).
func (s *NotifyingStore) SaveDCA(event domain.DCADecisionEvent) error {
	if err := s.inner.SaveDCA(event); err != nil {
		return err
	}

	go s.notify(formatDCAMessage(event))
	
	return nil
}

// EventsAfter delegates to inner.
func (s *NotifyingStore) EventsAfter(index uint64) ([]domain.DecisionEventRecord, error) {
	return s.inner.EventsAfter(index)
}

// CurrentIndex delegates to inner.
func (s *NotifyingStore) CurrentIndex() uint64 {
	return s.inner.CurrentIndex()
}

// Close delegates to inner.
func (s *NotifyingStore) Close() error {
	return s.inner.Close()
}

func (s *NotifyingStore) notify(text string) {
	if !s.tg.IsConfigured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.tg.Send(ctx, text); err != nil {
		s.logger.Warn("telegram notification failed", zap.Error(err))
	}
}

func formatDCAMessage(e domain.DCADecisionEvent) string {
	action := strings.ToUpper(e.Action)
	quote := quoteCurrency(e.Pair)

	msg := fmt.Sprintf("[DCA] %s %s\nPrice: %s\nAvg Entry: %s\nTrade #%d\nBalance: %s %s\n%s",
		action,
		e.Pair,
		e.CurrentPrice.StringFixed(2),
		e.AverageEntryPrice.StringFixed(2),
		e.TradePart,
		e.QuoteBalance.StringFixed(2),
		quote,
		e.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
	)
	return msg
}

func formatAIMessage(e domain.AIDecisionEvent) string {
	actionLabel := map[string]string{
		"open_long":   "OPEN LONG",
		"close_long":  "CLOSE LONG",
		"open_short":  "OPEN SHORT",
		"close_short": "CLOSE SHORT",
	}
	label, ok := actionLabel[e.Action]
	if !ok {
		label = strings.ToUpper(e.Action)
	}

	msg := fmt.Sprintf("[AI] %s %s\nModel: %s\nPrice: %s\nRisk: %.1f%%",
		label, e.Pair, e.Model, e.CurrentPrice, e.RiskPercent)

	if e.TakeProfitPrice > 0 {
		msg += fmt.Sprintf("\nTP: %.2f  SL: %.2f", e.TakeProfitPrice, e.StopLossPrice)
	}
	if e.PositionAmount != "" {
		msg += fmt.Sprintf("\nPosition: %s %s @ %s",
			e.PositionAmount, baseCurrency(e.Pair), e.PositionEntryPrice)
	}
	if e.Reasoning != "" {
		r := e.Reasoning
		if len(r) > 200 {
			r = r[:197] + "..."
		}
		msg += "\nReason: " + r
	}
	msg += "\n" + e.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC")
	return msg
}

func quoteCurrency(pair string) string {
	_, after, ok := strings.Cut(pair, "_")
	if !ok {
		return pair
	}
	return after
}

func baseCurrency(pair string) string {
	before, _, ok := strings.Cut(pair, "_")
	if !ok {
		return pair
	}
	return before
}
