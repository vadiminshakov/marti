// Package dca provides the Dollar-Cost Averaging strategy constructor.
// The core engine lives in the averaging package; this package is a thin wrapper
// that configures it with equal-sized position steps.
package dca

import (
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/strategy/averaging"
)

// NewDCAStrategy creates a DCA trading strategy that buys equal amounts at each step.
func NewDCAStrategy(
	l *zap.Logger,
	stateKey string,
	pair entity.Pair,
	amountPercent decimal.Decimal,
	pricer averaging.Pricer,
	trader averaging.Tradersvc,
	recorder averaging.DecisionRecorder,
	maxDcaTrades int,
	dcaPercentThresholdBuy decimal.Decimal,
	dcaPercentThresholdSell decimal.Decimal,
) (*averaging.Strategy, error) {
	return averaging.NewStrategy(
		l,
		stateKey,
		pair,
		amountPercent,
		pricer,
		trader,
		recorder,
		maxDcaTrades,
		dcaPercentThresholdBuy,
		dcaPercentThresholdSell,
		averaging.EqualSizer,
		"dca",
	)
}
