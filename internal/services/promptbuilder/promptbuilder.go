// Package promptbuilder provides optimized prompt generation for AI trading decisions.
package promptbuilder

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/domain"
	"go.uber.org/zap"
)

// PromptBuilder constructs optimized prompts for the LLM.
type PromptBuilder struct {
	pair   domain.Pair
	logger *zap.Logger
}

// NewPromptBuilder creates a new PromptBuilder instance.
func NewPromptBuilder(pair domain.Pair, logger *zap.Logger) *PromptBuilder {
	return &PromptBuilder{
		pair:   pair,
		logger: logger,
	}
}

// MarketContext data needed for prompt building.
type MarketContext struct {
	Primary         *domain.Timeframe
	VolumeAnalysis  domain.VolumeAnalysis
	HigherTimeframe *domain.Timeframe
	CurrentPosition *domain.Position
	Balance         decimal.Decimal
}

// BuildUserPrompt constructs the complete user prompt.
func (pb *PromptBuilder) BuildUserPrompt(ctx MarketContext) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Market Analysis for %s\n\n", pb.pair.String()))

	if overview := pb.formatMultiTimeframe(ctx); overview != "" {
		sb.WriteString(overview)
	}

	sb.WriteString(pb.formatRecentData(ctx.Primary, 20))

	sb.WriteString(pb.formatHistoricalSummary(ctx.Primary))

	sb.WriteString(pb.formatVolumeAnalysis(ctx.VolumeAnalysis))

	if ctx.CurrentPosition != nil {
		currentPrice := decimal.Zero
		if ctx.Primary != nil {
			if price, ok := ctx.Primary.LatestPrice(); ok {
				currentPrice = price
			}
		}
		sb.WriteString(pb.formatPosition(ctx.CurrentPosition, currentPrice))
	} else {
		sb.WriteString("## Current Position\n\n")
		sb.WriteString("**Status:** No open position\n\n")
	}

	sb.WriteString("## Account Information\n\n")
	sb.WriteString(fmt.Sprintf("**Available Balance (%s):** %s\n\n", pb.pair.To, ctx.Balance.StringFixed(2)))

	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Analyze the market data and provide your trading decision in JSON format.\n")
	if ctx.CurrentPosition != nil {
		if ctx.CurrentPosition.Side == domain.PositionSideLong {
			sb.WriteString("You currently have an open LONG position - decide whether to hold, close_long, or add to it.\n")
		} else if ctx.CurrentPosition.Side == domain.PositionSideShort {
			sb.WriteString("You currently have an open SHORT position - decide whether to hold, close_short, or add to it.\n")
		}
	} else {
		sb.WriteString("You have no open position - decide whether to open_long, open_short, or hold (wait).\n")
	}

	return sb.String()
}

// formatRecentData formats the last N candles.
func (pb *PromptBuilder) formatRecentData(primary *domain.Timeframe, limit int) string {
	var sb strings.Builder

	sb.WriteString("## Recent Market Data (Last 20 Candles)\n\n")

	if primary == nil || len(primary.Candles) == 0 {
		sb.WriteString("No data available\n\n")
		return sb.String()
	}

	klines := primary.Candles

	// calculate start index for last N candles
	startIdx := len(klines) - limit
	if startIdx < 0 {
		startIdx = 0
	}

	// table header
	sb.WriteString("```\n")
	sb.WriteString("Time     | Open     | High     | Low      | Close    | Volume   | EMA20    | EMA50    | MACD   | RSI7  | RSI14 | ATR14\n")
	sb.WriteString("---------|----------|----------|----------|----------|----------|----------|----------|--------|-------|-------|-------\n")

	// table rows
	for i := startIdx; i < len(klines); i++ {
		k := klines[i]
		timeStr := k.OpenTime.Format("15:04")

		// get corresponding indicator data if available
		ind, hasIndicators := primary.IndicatorForCandle(i)

		// format row with 2 decimal places for prices, appropriate precision for indicators
		sb.WriteString(fmt.Sprintf("%-8s | %8.2f | %8.2f | %8.2f | %8.2f | %8.2f",
			timeStr,
			toFloat(k.Open),
			toFloat(k.High),
			toFloat(k.Low),
			toFloat(k.Close),
			toFloat(k.Volume),
		))

		if hasIndicators {
			sb.WriteString(fmt.Sprintf(" | %8.2f | %8.2f | %6.2f | %5.1f | %5.1f | %5.2f",
				toFloat(ind.EMA20),
				toFloat(ind.EMA50),
				toFloat(ind.MACD),
				toFloat(ind.RSI7),
				toFloat(ind.RSI14),
				toFloat(ind.ATR14),
			))
		} else {
			sb.WriteString(" |        - |        - |      - |     - |     - |     -")
		}

		sb.WriteString("\n")
	}

	sb.WriteString("```\n\n")

	return sb.String()
}

// toFloat converts decimal.Decimal to float64.
func toFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}

// formatHistoricalSummary formats older candles.
func (pb *PromptBuilder) formatHistoricalSummary(primary *domain.Timeframe) string {
	var sb strings.Builder

	if primary == nil || len(primary.Candles) <= 20 {
		return ""
	}

	klines := primary.Candles

	sb.WriteString("## Historical Context (Older Candles)\n\n")

	// get historical data (everything except the last 20)
	endIdx := len(klines) - 20
	if endIdx <= 0 {
		return ""
	}

	// close prices
	sb.WriteString("**Close Prices:** [")
	for i := 0; i < endIdx; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%.2f", toFloat(klines[i].Close)))
	}
	sb.WriteString("]\n\n")

	// EMA20 (if available)
	if len(primary.Indicators) > 0 {
		sb.WriteString("**EMA20:** [")
		first := true
		for i := 0; i < endIdx; i++ {
			if ind, ok := primary.IndicatorForCandle(i); ok {
				if !first {
					sb.WriteString(",")
				}
				sb.WriteString(fmt.Sprintf("%.2f", toFloat(ind.EMA20)))
				first = false
			}
		}
		sb.WriteString("]\n\n")

		// RSI14 (if available)
		sb.WriteString("**RSI14:** [")
		first = true
		for i := 0; i < endIdx; i++ {
			if ind, ok := primary.IndicatorForCandle(i); ok {
				if !first {
					sb.WriteString(",")
				}
				sb.WriteString(fmt.Sprintf("%.1f", toFloat(ind.RSI14)))
				first = false
			}
		}
		sb.WriteString("]\n\n")
	}

	return sb.String()
}

// formatVolumeAnalysis formats volume metrics.
func (pb *PromptBuilder) formatVolumeAnalysis(volume domain.VolumeAnalysis) string {
	var sb strings.Builder

	sb.WriteString("## Volume Analysis\n\n")

	// current and average volume
	sb.WriteString(fmt.Sprintf("**Current Volume:** %s\n", volume.CurrentVolume.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**Average Volume (20-period):** %s\n", volume.AverageVolume.StringFixed(2)))

	// relative volume with interpretation
	relVol := volume.RelativeVolume
	sb.WriteString(fmt.Sprintf("**Relative Volume:** %.2fx", toFloat(relVol)))

	if relVol.GreaterThan(decimal.NewFromFloat(1.5)) {
		sb.WriteString(" (Significantly above average 📈)\n")
	} else if relVol.GreaterThan(decimal.NewFromFloat(1.0)) {
		sb.WriteString(" (Above average)\n")
	} else if relVol.LessThan(decimal.NewFromFloat(0.7)) {
		sb.WriteString(" (Below average 📉)\n")
	} else {
		sb.WriteString(" (Near average)\n")
	}

	// volume spikes
	if len(volume.VolumeSpikes) > 0 {
		sb.WriteString("\n**Volume Spikes (>1.5x avg):** ")
		// show only the most recent spikes (last 10)
		startIdx := 0
		if len(volume.VolumeSpikes) > 10 {
			startIdx = len(volume.VolumeSpikes) - 10
		}
		sb.WriteString("Candles [")
		for i := startIdx; i < len(volume.VolumeSpikes); i++ {
			if i > startIdx {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("#%d", volume.VolumeSpikes[i]))
		}
		sb.WriteString("]\n")
	} else {
		sb.WriteString("\n**Volume Spikes:** None detected\n")
	}

	sb.WriteString("\n")

	return sb.String()
}

// formatMultiTimeframe formats higher timeframe snapshot.
func (pb *PromptBuilder) formatMultiTimeframe(ctx MarketContext) string {
	var sb strings.Builder

	hasPrimary := ctx.Primary != nil && ctx.Primary.Summary != nil
	hasHigher := ctx.HigherTimeframe != nil && ctx.HigherTimeframe.Summary != nil

	if !hasPrimary && !hasHigher {
		return ""
	}

	sb.WriteString("## Multi-Timeframe Overview\n\n")

	// primary timeframe (current)
	if hasPrimary {
		primarySummary := ctx.Primary.Summary

		sb.WriteString("**Primary Timeframe:**\n")
		sb.WriteString(fmt.Sprintf("- Price: %s | EMA20: %s | EMA50: %s | RSI14: %.1f\n",
			primarySummary.Price.StringFixed(2),
			primarySummary.EMA20.StringFixed(2),
			primarySummary.EMA50.StringFixed(2),
			toFloat(primarySummary.RSI14),
		))
		sb.WriteString(fmt.Sprintf("- Trend: %s\n", primarySummary.Trend.Title()))
	}

	// higher timeframe
	if hasHigher {
		htf := ctx.HigherTimeframe.Summary
		sb.WriteString(fmt.Sprintf("\n**Higher Timeframe (%s):**\n", htf.Interval))
		sb.WriteString(fmt.Sprintf("- Price: %s | EMA20: %s | EMA50: %s | RSI14: %.1f\n",
			htf.Price.StringFixed(2),
			htf.EMA20.StringFixed(2),
			htf.EMA50.StringFixed(2),
			toFloat(htf.RSI14),
		))
		sb.WriteString(fmt.Sprintf("- Trend: %s\n", htf.Trend.Title()))

		// check trend alignment
		if hasPrimary {
			primaryTrend := ctx.Primary.Summary.Trend

			if primaryTrend == htf.Trend && primaryTrend != domain.TrendDirectionNeutral {
				sb.WriteString(fmt.Sprintf("\n✅ **Timeframes Aligned:** Both timeframes show %s trend\n", primaryTrend.Title()))
			} else if primaryTrend != htf.Trend {
				sb.WriteString(fmt.Sprintf("\n⚠️ **Timeframe Divergence:** Primary is %s, Higher is %s\n",
					primaryTrend.Title(),
					htf.Trend.Title(),
				))
			}
		}
	}

	sb.WriteString("\n")

	return sb.String()
}

// formatPosition formats open position information.
func (pb *PromptBuilder) formatPosition(position *domain.Position, currentPrice decimal.Decimal) string {
	var sb strings.Builder

	sb.WriteString("## Current Position\n\n")
	if position.Side == domain.PositionSideLong {
		sb.WriteString("**Status:** Open Long\n\n")
	} else if position.Side == domain.PositionSideShort {
		sb.WriteString("**Status:** Open Short\n\n")
	}

	// entry information
	sb.WriteString(fmt.Sprintf("**Entry Price:** %s\n", position.EntryPrice.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**Amount:** %s %s\n", position.Amount.StringFixed(8), pb.pair.From))
	sb.WriteString(fmt.Sprintf("**Current Price:** %s\n", currentPrice.StringFixed(2)))

	// calculate P&L
	pnl := position.PnL(currentPrice)
	pnlPercent := decimal.Zero
	if !position.EntryPrice.IsZero() {
		if position.Side == domain.PositionSideLong {
			// for long: (currentPrice - entryPrice) / entryPrice * 100
			pnlPercent = currentPrice.Sub(position.EntryPrice).Div(position.EntryPrice).Mul(decimal.NewFromInt(100))
		} else {
			// for short: (entryPrice - currentPrice) / entryPrice * 100
			pnlPercent = position.EntryPrice.Sub(currentPrice).Div(position.EntryPrice).Mul(decimal.NewFromInt(100))
		}
	}

	pnlSign := ""
	if pnl.GreaterThan(decimal.Zero) {
		pnlSign = "+"
	}

	sb.WriteString(fmt.Sprintf("**Unrealized P&L:** %s%s %s (%s%.2f%%)\n",
		pnlSign,
		pnl.StringFixed(2),
		pb.pair.To,
		pnlSign,
		toFloat(pnlPercent),
	))

	// time held
	if !position.EntryTime.IsZero() {
		duration := time.Since(position.EntryTime)
		sb.WriteString(fmt.Sprintf("**Time Held:** %s\n", formatDuration(duration)))
	}

	sb.WriteString("\n")

	// exit levels
	if !position.StopLoss.IsZero() || !position.TakeProfit.IsZero() {
		sb.WriteString("**Exit Levels:**\n")

		if !position.StopLoss.IsZero() {
			distanceToSL := position.StopLoss.Sub(currentPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
			sb.WriteString(fmt.Sprintf("- Stop Loss: %s (%s%.2f%% from current)\n",
				position.StopLoss.StringFixed(2),
				"",
				toFloat(distanceToSL),
			))
		}

		if !position.TakeProfit.IsZero() {
			distanceToTP := position.TakeProfit.Sub(currentPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
			sb.WriteString(fmt.Sprintf("- Take Profit: %s (+%.2f%% from current)\n",
				position.TakeProfit.StringFixed(2),
				toFloat(distanceToTP),
			))
		}

		// calculate risk-reward ratio
		if !position.StopLoss.IsZero() && !position.TakeProfit.IsZero() {
			risk := position.EntryPrice.Sub(position.StopLoss).Abs()
			reward := position.TakeProfit.Sub(position.EntryPrice).Abs()

			if risk.GreaterThan(decimal.Zero) {
				rrRatio := reward.Div(risk)
				sb.WriteString(fmt.Sprintf("- Risk:Reward Ratio: 1:%.2f\n", toFloat(rrRatio)))
			}
		}

		sb.WriteString("\n")
	}

	// invalidation condition
	if position.Invalidation != "" {
		sb.WriteString(fmt.Sprintf("**Invalidation Condition:** %s\n\n", position.Invalidation))
	}

	return sb.String()
}

// formatDuration formats a time.Duration.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		days := int(d.Hours()) / 24
		hours := int(d.Hours()) % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
}
