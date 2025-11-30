package pricer

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/vadiminshakov/marti/internal/domain"
)

// HyperliquidPricer fetches prices from Hyperliquid public Info API.
type HyperliquidPricer struct {
	info *hyperliquid.Info
}

func NewHyperliquidPricer(info *hyperliquid.Info) *HyperliquidPricer {
	return &HyperliquidPricer{info: info}
}

func (p *HyperliquidPricer) GetPrice(ctx context.Context, pair domain.Pair) (decimal.Decimal, error) {
	if p.info == nil {
		return decimal.Zero, fmt.Errorf("hyperliquid info client is nil")
	}

	mids, err := p.info.AllMids(ctx)
	if err != nil {
		return decimal.Zero, err
	}

	// Hyperliquid mids are keyed by base coin (e.g., "BTC").
	mid, ok := mids[pair.From]
	if !ok || mid == "" {
		return decimal.Zero, fmt.Errorf("hyperliquid API returned empty mid price for %s", pair.From)
	}
	return decimal.NewFromString(mid)
}
