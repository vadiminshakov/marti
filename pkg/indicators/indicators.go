// Package indicators provides technical analysis indicators (EMA, MACD, RSI, ATR).
package indicators

import (
	"fmt"

	"github.com/cinar/indicator/v2/helper"
	"github.com/cinar/indicator/v2/momentum"
	"github.com/cinar/indicator/v2/trend"
	"github.com/cinar/indicator/v2/volatility"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
)

// PriceData represents OHLC (open, high, low, close) price data.
type PriceData struct {
	Open  decimal.Decimal
	High  decimal.Decimal
	Low   decimal.Decimal
	Close decimal.Decimal
}

// CalculateEMA calculates the Exponential Moving Average for the given period.
func CalculateEMA(closes []decimal.Decimal, period int) ([]decimal.Decimal, error) {
	if len(closes) < period {
		return nil, fmt.Errorf("not enough data points: need %d, got %d", period, len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	ema := trend.NewEmaWithPeriod[float64](period)
	inputChan := helper.SliceToChan(closesFloat)
	outputChan := ema.Compute(inputChan)
	emaFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(emaFloat), nil
}

// CalculateMACD calculates MACD line values.
func CalculateMACD(closes []decimal.Decimal) ([]decimal.Decimal, error) {
	if len(closes) < 26 {
		return nil, fmt.Errorf("not enough data points for MACD: need at least 26, got %d", len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	macd := trend.NewMacd[float64]()
	inputChan := helper.SliceToChan(closesFloat)
	macdChan, signalChan := macd.Compute(inputChan)
	// drain signal channel to prevent blocking
	go func() {
		for range signalChan {
		}
	}()
	macdFloat := helper.ChanToSlice(macdChan)

	return float64ToDecimals(macdFloat), nil
}

// CalculateRSI calculates the Relative Strength Index for the given period.
func CalculateRSI(closes []decimal.Decimal, period int) ([]decimal.Decimal, error) {
	if len(closes) < period+1 {
		return nil, fmt.Errorf("not enough data points for RSI: need %d, got %d", period+1, len(closes))
	}

	closesFloat := decimalsToFloat64(closes)

	rsi := momentum.NewRsiWithPeriod[float64](period)
	inputChan := helper.SliceToChan(closesFloat)
	outputChan := rsi.Compute(inputChan)
	rsiFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(rsiFloat), nil
}

// CalculateATR calculates the Average True Range for the given period.
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

	atr := volatility.NewAtrWithPeriod[float64](period)
	highChan := helper.SliceToChan(highs)
	lowChan := helper.SliceToChan(lows)
	closeChan := helper.SliceToChan(closes)
	outputChan := atr.Compute(highChan, lowChan, closeChan)
	atrFloat := helper.ChanToSlice(outputChan)

	return float64ToDecimals(atrFloat), nil
}

// CalculateAllIndicators calculates all indicators and returns aligned slices.
func CalculateAllIndicators(priceData []PriceData) ([]entity.TechnicalIndicators, error) {
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

	// find minimum length among indicators (handles warmup differences)
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

	// build aligned result applying individual offsets
	offsetEMA20 := len(ema20) - minLen
	offsetEMA50 := len(ema50) - minLen
	offsetMACD := len(macd) - minLen
	offsetRSI7 := len(rsi7) - minLen
	offsetRSI14 := len(rsi14) - minLen
	offsetATR3 := len(atr3) - minLen
	offsetATR14 := len(atr14) - minLen

	result := make([]entity.TechnicalIndicators, minLen)

	for i := 0; i < minLen; i++ {
		result[i] = entity.TechnicalIndicators{
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

// decimalsToFloat64 converts a slice of decimal.Decimal to []float64.
func decimalsToFloat64(decimals []decimal.Decimal) []float64 {
	result := make([]float64, len(decimals))
	for i, d := range decimals {
		result[i], _ = d.Float64()
	}
	return result
}

// float64ToDecimals converts a slice of float64 to []decimal.Decimal.
func float64ToDecimals(floats []float64) []decimal.Decimal {
	result := make([]decimal.Decimal, len(floats))
	for i, f := range floats {
		result[i] = decimal.NewFromFloat(f)
	}
	return result
}
