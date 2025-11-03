// Package indicators provides technical analysis indicators for trading strategies.
// It uses the cinar/indicator library to calculate common trading indicators
// such as EMA, MACD, RSI, and ATR from price data.
package indicators

import (
	"fmt"

	"github.com/cinar/indicator/v2/helper"
	"github.com/cinar/indicator/v2/momentum"
	"github.com/cinar/indicator/v2/trend"
	"github.com/cinar/indicator/v2/volatility"
	"github.com/shopspring/decimal"
)

// IndicatorData holds calculated technical indicators for a specific time period
type IndicatorData struct {
	// EMA20 is the 20-period Exponential Moving Average
	EMA20 decimal.Decimal
	// EMA50 is the 50-period Exponential Moving Average
	EMA50 decimal.Decimal
	// MACD is the MACD indicator value
	MACD decimal.Decimal
	// RSI7 is the 7-period Relative Strength Index
	RSI7 decimal.Decimal
	// RSI14 is the 14-period Relative Strength Index
	RSI14 decimal.Decimal
	// ATR3 is the 3-period Average True Range
	ATR3 decimal.Decimal
	// ATR14 is the 14-period Average True Range
	ATR14 decimal.Decimal
}

// PriceData represents OHLC (Open, High, Low, Close) price data
type PriceData struct {
	Open  decimal.Decimal
	High  decimal.Decimal
	Low   decimal.Decimal
	Close decimal.Decimal
}

// CalculateEMA calculates the Exponential Moving Average for the given period
func CalculateEMA(closes []decimal.Decimal, period int) ([]decimal.Decimal, error) {
	if len(closes) < period {
		return nil, fmt.Errorf("not enough data points: need %d, got %d", period, len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	// Create EMA indicator
	ema := trend.NewEmaWithPeriod[float64](period)

	// Convert slice to channel
	inputChan := helper.SliceToChan(closesFloat)

	// Compute EMA
	outputChan := ema.Compute(inputChan)

	// Convert channel back to slice
	emaFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(emaFloat), nil
}

// CalculateMACD calculates the MACD indicator
// Returns MACD line values
func CalculateMACD(closes []decimal.Decimal) ([]decimal.Decimal, error) {
	if len(closes) < 26 {
		return nil, fmt.Errorf("not enough data points for MACD: need at least 26, got %d", len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	// Create MACD indicator
	macd := trend.NewMacd[float64]()

	// Convert slice to channel
	inputChan := helper.SliceToChan(closesFloat)

	// Compute MACD - returns both MACD and signal channels
	macdChan, signalChan := macd.Compute(inputChan)

	// IMPORTANT: Must drain signal channel in goroutine to prevent blocking
	go func() {
		for range signalChan {
			// Just drain it
		}
	}()

	// Convert channel back to slice
	macdFloat := helper.ChanToSlice(macdChan)

	return float64ToDecimals(macdFloat), nil
}

// CalculateRSI calculates the Relative Strength Index for the given period
func CalculateRSI(closes []decimal.Decimal, period int) ([]decimal.Decimal, error) {
	if len(closes) < period+1 {
		return nil, fmt.Errorf("not enough data points for RSI: need %d, got %d", period+1, len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	// Create RSI indicator
	rsi := momentum.NewRsiWithPeriod[float64](period)

	// Convert slice to channel
	inputChan := helper.SliceToChan(closesFloat)

	// Compute RSI
	outputChan := rsi.Compute(inputChan)

	// Convert channel back to slice
	rsiFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(rsiFloat), nil
}

// CalculateATR calculates the Average True Range for the given period
func CalculateATR(priceData []PriceData, period int) ([]decimal.Decimal, error) {
	if len(priceData) < period+1 {
		return nil, fmt.Errorf("not enough data points for ATR: need %d, got %d", period+1, len(priceData))
	}

	highs := make([]float64, len(priceData))
	lows := make([]float64, len(priceData))
	closes := make([]float64, len(priceData))

	for i, pd := range priceData {
		highs[i], _ = pd.High.Float64()
		lows[i], _ = pd.Low.Float64()
		closes[i], _ = pd.Close.Float64()
	}

	// Create ATR indicator
	atr := volatility.NewAtrWithPeriod[float64](period)

	// Convert slices to channels
	highChan := helper.SliceToChan(highs)
	lowChan := helper.SliceToChan(lows)
	closeChan := helper.SliceToChan(closes)

	// Compute ATR
	outputChan := atr.Compute(highChan, lowChan, closeChan)

	// Convert channel back to slice
	atrFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(atrFloat), nil
}

// CalculateAllIndicators calculates all technical indicators for the given price data
// Returns a slice of IndicatorData, one for each data point where all indicators can be calculated
func CalculateAllIndicators(priceData []PriceData) ([]IndicatorData, error) {
	if len(priceData) < 50 {
		return nil, fmt.Errorf("not enough data points: need at least 50, got %d", len(priceData))
	}

	closes := make([]decimal.Decimal, len(priceData))
	for i, pd := range priceData {
		closes[i] = pd.Close
	}

	ema20, err := CalculateEMA(closes, 20)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate EMA20: %w", err)
	}

	ema50, err := CalculateEMA(closes, 50)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate EMA50: %w", err)
	}

	macd, err := CalculateMACD(closes)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate MACD: %w", err)
	}

	rsi7, err := CalculateRSI(closes, 7)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate RSI7: %w", err)
	}

	rsi14, err := CalculateRSI(closes, 14)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate RSI14: %w", err)
	}

	atr3, err := CalculateATR(priceData, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate ATR3: %w", err)
	}

	atr14, err := CalculateATR(priceData, 14)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate ATR14: %w", err)
	}

	// Find the minimum length (some indicators may return fewer values due to warmup period)
	minLen := len(ema20)
	if len(ema50) < minLen {
		minLen = len(ema50)
	}
	if len(macd) < minLen {
		minLen = len(macd)
	}
	if len(rsi7) < minLen {
		minLen = len(rsi7)
	}
	if len(rsi14) < minLen {
		minLen = len(rsi14)
	}
	if len(atr3) < minLen {
		minLen = len(atr3)
	}
	if len(atr14) < minLen {
		minLen = len(atr14)
	}

	// Build result starting from the point where all indicators are available
	// Calculate offset for each indicator separately
	offsetEMA20 := len(ema20) - minLen
	offsetEMA50 := len(ema50) - minLen
	offsetMACD := len(macd) - minLen
	offsetRSI7 := len(rsi7) - minLen
	offsetRSI14 := len(rsi14) - minLen
	offsetATR3 := len(atr3) - minLen
	offsetATR14 := len(atr14) - minLen

	result := make([]IndicatorData, minLen)

	for i := 0; i < minLen; i++ {
		result[i] = IndicatorData{
			EMA20: ema20[offsetEMA20+i],
			EMA50: ema50[offsetEMA50+i],
			MACD:  macd[offsetMACD+i],
			RSI7:  rsi7[offsetRSI7+i],
			RSI14: rsi14[offsetRSI14+i],
			ATR3:  atr3[offsetATR3+i],
			ATR14: atr14[offsetATR14+i],
		}
	}

	return result, nil
}

// decimalsToFloat64 converts a slice of decimal.Decimal to []float64
func decimalsToFloat64(decimals []decimal.Decimal) []float64 {
	result := make([]float64, len(decimals))
	for i, d := range decimals {
		result[i], _ = d.Float64()
	}
	return result
}

// float64ToDecimals converts a slice of float64 to []decimal.Decimal
func float64ToDecimals(floats []float64) []decimal.Decimal {
	result := make([]decimal.Decimal, len(floats))
	for i, f := range floats {
		result[i] = decimal.NewFromFloat(f)
	}
	return result
}
