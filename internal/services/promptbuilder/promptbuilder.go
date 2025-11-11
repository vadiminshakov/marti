// Package promptbuilder provides optimized prompt generation for AI trading decisions.
// It formats market data, technical indicators, and position information into
// token-efficient prompts for LLM consumption.
package promptbuilder

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
	"go.uber.org/zap"
)

// SystemPrompt defines the global system instructions for the trading LLM.
const SystemPrompt = `You are a cryptocurrency spot trading system. Your objective is to make profitable trading decisions by analyzing market data.

You can take trades in both directions—opening long positions when you expect price appreciation and short positions when you expect price declines.

## OBJECTIVE
Maximize returns while preserving capital through rational analysis of market data patterns.

## TRADING CONSTRAINTS
1. **Directional Flexibility**: You can open long positions (buy) or short positions (sell).
2. **Maximum position size**: 15% of available balance per trade
3. **Risk management**: Every buy order must include stop-loss and take-profit levels
4. **Position Management**: You can increase the size of an existing position (buy more) or partially close it (sell a portion).

## AVAILABLE DATA FIELDS

You receive structured market data. Here's what each field represents:

**OHLCV Data (Open, High, Low, Close, Volume):**
- Open: Opening price of the time period
- High: Highest price reached during the period
- Low: Lowest price reached during the period
- Close: Closing price of the period
- Volume: Total trading volume in base currency
- Time: Timestamp for each candle

**Technical Indicators:**
- EMA20, EMA50: Exponential moving averages (20 and 50 periods)
- MACD, MACD_Signal, MACD_Histogram: Trend-following momentum indicators
- RSI7, RSI14: Relative strength index (7 and 14 periods, range 0-100)
- ATR3, ATR14: Average true range for volatility measurement (3 and 14 periods)

**Market Structure:**
- Support Levels: Price levels below current price with strength (number of touches) and distance
- Resistance Levels: Price levels above current price with strength and distance
- Current Price: Latest market price

**Volume Analysis:**
- Current Volume: Volume of most recent candle
- Average Volume: 20-period moving average of volume
- Relative Volume: Ratio of current to average (>1.5 indicates spike)
- Volume Spikes: Array of candle indices where volume exceeded 1.5x average

**Multi-Timeframe Data:**
- Primary Timeframe: Detailed data for main trading timeframe (typically 3m)
- Higher Timeframe: Summary snapshot from broader timeframe (typically 4h) including price, EMAs, RSI, and trend

**Account Information:**
- Available Balance: Amount of quote currency available for trading
- Current Position (if exists):
  - Entry Price: Price where position was opened
  - Amount: Position size in base currency
  - Stop Loss: Defined stop-loss price
  - Take Profit: Defined take-profit price
  - Invalidation Condition: Condition that would invalidate the trade thesis
  - Entry Time: When position was opened
  - Unrealized P&L: Current profit/loss

## DECISION OUTPUT FORMAT

Respond with ONLY valid JSON. No markdown, no code blocks, no additional text.

**Required JSON structure:**

{
  "action": "open_long|close_long|open_short|close_short|hold",
  "risk_percent": 0.0,
  "reasoning": "explain your analysis and decision",
  "exit_plan": {
    "stop_loss_price": 0.0,
    "take_profit_price": 0.0,
    "invalidation_condition": "specific measurable condition"
  }
}

**Field specifications:**

- **action** (string): Must be one of:
  - "open_long": Open a new long position or add to an existing long position.
  - "close_long": Close or reduce an existing long position.
  - "open_short": Open a new short position or add to an existing short position.
  - "close_short": Close or reduce an existing short position.
  - "hold": Take no action and maintain the current state.

- **risk_percent** (float): Percentage of balance to allocate (0.0-15.0)
  - Should reflect your confidence in the trade
  - Higher confidence = higher allocation (up to 15% max)
  - Use 0.0 for "hold", "close_long", and "close_short" actions.
  - Only use positive values for "open_long" and "open_short" actions.

- **reasoning** (string): Explain your analysis
  - What patterns or data influenced your decision
  - Why you chose this specific action
  - Be specific about which data points matter

- **exit_plan** (object): Required for "open_long" and "open_short" actions, use zeros/empty for others
  - **stop_loss_price** (float): Exact price to exit if trade fails
    - For long: stop_loss < entry_price (exit when price goes down)
    - For short: stop_loss > entry_price (exit when price goes up)
  - **take_profit_price** (float): Target price for profit-taking
    - For long: take_profit > entry_price (profit when price goes up)
    - For short: take_profit < entry_price (profit when price goes down)
  - **invalidation_condition** (string): Specific, measurable condition that would invalidate your thesis
    - Must be objective and verifiable
    - Examples: "Price closes below 45000", "RSI drops below 30", "Volume spike with red candle"

**Validation rules:**
- Cannot "open_long" when long position already exists
- Cannot "open_short" when short position already exists
- Cannot "close_long" without an open long position
- Cannot "close_short" without an open short position
- For "open_long": (take_profit_price - entry_price) >= 2 × (entry_price - stop_loss_price)
- For "open_short": (entry_price - take_profit_price) >= 2 × (stop_loss_price - entry_price)
- All prices must be positive numbers
- invalidation_condition must be a non-empty string for "open_long" and "open_short" actions

## TRADING PHILOSOPHY

You are free to develop your own analytical approach. The data contains many possible patterns and relationships. Find what works.

- Analyze all available data to identify patterns
- Consider relationships between different metrics
- Think about market context and regime changes
- Balance conviction with risk management
- Preserve capital when unsure
- Evaluate both bullish (long) and bearish (short) opportunities before choosing an action

Do not force trades. "hold" is a valid decision when conditions are unclear.

## CRITICAL REMINDERS

1. Output ONLY the JSON object - nothing else
2. Ensure JSON is valid and parseable
3. Never exceed 15% risk per trade
4. Be specific in your reasoning
5. When in doubt, use "hold"

You should strive to capture as much profit as possible as quickly as you can, using current market conditions.
Don’t hold back from taking a trade if any short-term strategy looks profitable to you.
You can also use longer-term trades if you see an opportunity. You choose the strategy yourself. The main goal is to extract profit as efficiently as possible.`

// PromptBuilder constructs optimized prompts for the LLM
type PromptBuilder struct {
	pair   entity.Pair
	logger *zap.Logger
}

// NewPromptBuilder creates a new PromptBuilder instance
func NewPromptBuilder(pair entity.Pair, logger *zap.Logger) *PromptBuilder {
	return &PromptBuilder{
		pair:   pair,
		logger: logger,
	}
}

// MarketContext contains all data needed for prompt building
type MarketContext struct {
	Primary         *entity.Timeframe
	VolumeAnalysis  entity.VolumeAnalysis
	HigherTimeframe *entity.Timeframe
	CurrentPosition *entity.Position
	Balance         decimal.Decimal
}

// BuildUserPrompt constructs the complete user prompt from market context
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
		if ctx.CurrentPosition.Side == entity.PositionSideLong {
			sb.WriteString("You currently have an open LONG position - decide whether to hold, close_long, or add to it.\n")
		} else if ctx.CurrentPosition.Side == entity.PositionSideShort {
			sb.WriteString("You currently have an open SHORT position - decide whether to hold, close_short, or add to it.\n")
		}
	} else {
		sb.WriteString("You have no open position - decide whether to open_long, open_short, or hold (wait).\n")
	}

	return sb.String()
}

// formatRecentData formats the last N candles with full OHLCV data and indicators
// in a compact table format to save tokens while maintaining readability
func (pb *PromptBuilder) formatRecentData(primary *entity.Timeframe, limit int) string {
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

// toFloat converts decimal.Decimal to float64 for formatting
func toFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}

// formatHistoricalSummary formats older candles (21-100) with only close prices
// and key indicators in compact array format to minimize token usage
func (pb *PromptBuilder) formatHistoricalSummary(primary *entity.Timeframe) string {
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

// formatVolumeAnalysis formats volume metrics including current volume,
// average volume, relative volume, and highlights volume spikes
func (pb *PromptBuilder) formatVolumeAnalysis(volume entity.VolumeAnalysis) string {
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

// formatMultiTimeframe formats higher timeframe snapshot with key metrics
// and shows trend alignment between timeframes in a compact format
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

			if primaryTrend == htf.Trend && primaryTrend != entity.TrendDirectionNeutral {
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

// formatPosition formats open position information including entry price,
// current P&L, time held, distance to stop-loss and take-profit, and risk-reward ratio
func (pb *PromptBuilder) formatPosition(position *entity.Position, currentPrice decimal.Decimal) string {
	var sb strings.Builder

	sb.WriteString("## Current Position\n\n")
	if position.Side == entity.PositionSideLong {
		sb.WriteString("**Status:** Open Long\n\n")
	} else if position.Side == entity.PositionSideShort {
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
		if position.Side == entity.PositionSideLong {
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

// formatDuration formats a time.Duration into a human-readable string
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
