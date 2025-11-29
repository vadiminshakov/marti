package domain
import (
	"github.com/shopspring/decimal"
	"time"
)

// TrendDirection qualitative direction of price action.
type TrendDirection string

const (
	TrendDirectionBullish TrendDirection = "bullish"
	TrendDirectionBearish TrendDirection = "bearish"
	TrendDirectionNeutral TrendDirection = "neutral"
)

// Title returns a human-readable representation.
func (t TrendDirection) Title() string {
	switch t {
	case TrendDirectionBullish:
		return "Bullish"
	case TrendDirectionBearish:
		return "Bearish"
	default:
		return "Neutral"
	}
}

// TechnicalIndicators snapshot of derived technical signals.
type TechnicalIndicators struct {
	EMA20 decimal.Decimal
	EMA50 decimal.Decimal
	MACD  decimal.Decimal
	RSI7  decimal.Decimal
	RSI14 decimal.Decimal
	ATR3  decimal.Decimal
	ATR14 decimal.Decimal
}

// MarketCandle single OHLCV candlestick.
type MarketCandle struct {
	OpenTime  time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
	CloseTime time.Time
}

// TimeframeSummary headline metrics for a timeframe.
type TimeframeSummary struct {
	Interval string
	Price    decimal.Decimal
	EMA20    decimal.Decimal
	EMA50    decimal.Decimal
	RSI14    decimal.Decimal
	Trend    TrendDirection
}

// Timeframe candlestick and indicator data.
type Timeframe struct {
	Interval        string
	Candles         []MarketCandle
	Indicators      []TechnicalIndicators
	indicatorOffset int
	Summary         *TimeframeSummary
}

// NewTimeframe constructs a Timeframe.
func NewTimeframe(interval string, candles []MarketCandle, indicators []TechnicalIndicators) *Timeframe {
	offset := 0
	if len(candles) > len(indicators) {
		offset = len(candles) - len(indicators)
	}

	tf := &Timeframe{
		Interval:        interval,
		Candles:         candles,
		Indicators:      indicators,
		indicatorOffset: offset,
	}

	tf.Summary = tf.buildSummary()

	return tf
}

// IndicatorForCandle returns indicator values.
func (t *Timeframe) IndicatorForCandle(candleIdx int) (TechnicalIndicators, bool) {
	index, ok := t.indicatorIndexForCandle(candleIdx)
	if !ok {
		return TechnicalIndicators{}, false
	}
	return t.Indicators[index], true
}

// LatestCandle returns the most recent candlestick.
func (t *Timeframe) LatestCandle() (MarketCandle, bool) {
	if t == nil || len(t.Candles) == 0 {
		return MarketCandle{}, false
	}
	return t.Candles[len(t.Candles)-1], true
}

// LatestIndicator returns the indicator values.
func (t *Timeframe) LatestIndicator() (TechnicalIndicators, bool) {
	if t == nil || len(t.Candles) == 0 {
		return TechnicalIndicators{}, false
	}
	return t.IndicatorForCandle(len(t.Candles) - 1)
}

// LatestPrice returns the close price.
func (t *Timeframe) LatestPrice() (decimal.Decimal, bool) {
	candle, ok := t.LatestCandle()
	if !ok {
		return decimal.Zero, false
	}
	return candle.Close, true
}

func (t *Timeframe) buildSummary() *TimeframeSummary {
	if t == nil {
		return nil
	}

	candle, ok := t.LatestCandle()
	if !ok {
		return nil
	}

	indicator, ok := t.LatestIndicator()
	if !ok {
		return nil
	}

	return &TimeframeSummary{
		Interval: t.Interval,
		Price:    candle.Close,
		EMA20:    indicator.EMA20,
		EMA50:    indicator.EMA50,
		RSI14:    indicator.RSI14,
		Trend:    determineTrendDirection(candle.Close, indicator.EMA20, indicator.EMA50),
	}
}

func (t *Timeframe) indicatorIndexForCandle(candleIdx int) (int, bool) {
	if t == nil || len(t.Indicators) == 0 {
		return 0, false
	}

	index := candleIdx - t.indicatorOffset
	if index < 0 || index >= len(t.Indicators) {
		return 0, false
	}

	return index, true
}

func determineTrendDirection(price, ema20, ema50 decimal.Decimal) TrendDirection {
	if price.GreaterThan(ema20) && ema20.GreaterThan(ema50) {
		return TrendDirectionBullish
	} else if price.LessThan(ema20) && ema20.LessThan(ema50) {
		return TrendDirectionBearish
	}
	return TrendDirectionNeutral
}
