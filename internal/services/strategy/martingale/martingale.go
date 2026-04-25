// Package martingale provides the Martingale strategy constructor.
// The core engine lives in the averaging package; this package configures it
// with geometrically increasing position steps.
package martingale

import (
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	entity "github.com/vadiminshakov/marti/internal/domain"
	"github.com/vadiminshakov/marti/internal/services/strategy/averaging"
)

// NewMartingaleStrategy creates a Martingale trading strategy.
// Each subsequent buy is larger by the given multiplier factor.
func NewMartingaleStrategy(
	l *zap.Logger,
	stateKey string,
	pair entity.Pair,
	amountPercent decimal.Decimal,
	pricer averaging.Pricer,
	trader averaging.Tradersvc,
	recorder averaging.DecisionRecorder,
	maxTrades int,
	buyThreshold decimal.Decimal,
	sellThreshold decimal.Decimal,
	multiplier decimal.Decimal,
) (*averaging.Strategy, error) {
	return averaging.NewStrategy(
		l,
		stateKey,
		pair,
		amountPercent,
		pricer,
		trader,
		recorder,
		maxTrades,
		buyThreshold,
		sellThreshold,
		averaging.MartingaleSizer(multiplier),
		"martingale",
	)
}
