package domain

import "github.com/shopspring/decimal"

// MarketSnapshot market data for a single trading decision cycle.
type MarketSnapshot struct {
	// PrimaryTimeFrame candle and indicator data for the main trading timeframe.
	PrimaryTimeFrame *Timeframe
	// HigherTimeFrame candle and indicator data for broader market context.
	HigherTimeFrame *Timeframe
	// QuoteBalance available balance in quote currency.
	QuoteBalance decimal.Decimal
	// VolumeAnalysis volume metrics and patterns.
	VolumeAnalysis VolumeAnalysis
}

// Price returns the latest close price.
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
