package collector

import (
	"context"
	"fmt"

	bybit "github.com/hirokisan/bybit/v2"
	"github.com/vadiminshakov/marti/internal/domain"
)

// BybitKlineProvider implements KlineProvider for Bybit exchange.
type BybitKlineProvider struct {
	client *bybit.Client
}

// NewBybitKlineProvider creates a new Bybit kline provider.
func NewBybitKlineProvider(client *bybit.Client) *BybitKlineProvider {
	return &BybitKlineProvider{client: client}
}

// GetKlines fetches kline data from Bybit.
func (p *BybitKlineProvider) GetKlines(context.Context, entity.Pair, string, int) ([]entity.MarketCandle, error) {
	return nil, fmt.Errorf("Bybit kline provider for AI strategy is not yet implemented - please use Binance or Simulate platform for AI strategy")
}
