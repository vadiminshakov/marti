package detector

import (
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/entity"
)

// Detector defines the interface for trading signal detection
type Detector interface {
	NeedAction(price decimal.Decimal) (entity.Action, error)
	LastAction() entity.Action
}
