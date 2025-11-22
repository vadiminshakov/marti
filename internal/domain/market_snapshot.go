package domain

import "github.com/shopspring/decimal"

// MarketSnapshot aggregates relevant market data for a single trading decision cycle.
// This is a value object that represents a complete view of market conditions at a point in time.
type MarketSnapshot struct {
	// PrimaryTimeFrame contains candle and indicator data for the main trading timeframe
	PrimaryTimeFrame *Timeframe
	// HigherTimeFrame contains candle and indicator data for broader market context
	HigherTimeFrame *Timeframe
	// QuoteBalance is the available balance in quote currency for trading
	QuoteBalance decimal.Decimal
	// VolumeAnalysis contains volume metrics and patterns
	VolumeAnalysis VolumeAnalysis
}

// Price returns the latest close price of the primary timeframe if available.
// Returns zero if no price data is available.
func (s MarketSnapshot) Price() decimal.Decimal {
	if s.PrimaryTimeFrame == nil {
		return decimal.Zero
	}

	if s.PrimaryTimeFrame.Summary != nil {
		return s.PrimaryTimeFrame.Summary.Price
	}

	if price, ok := s.PrimaryTimeFrame.LatestPrice(); ok {
		return price
	}

	return decimal.Zero
}
