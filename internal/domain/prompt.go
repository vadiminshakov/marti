package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
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
Don't hold back from taking a trade if any short-term strategy looks profitable to you.
You can also use longer-term trades if you see an opportunity. You choose the strategy yourself. The main goal is to extract profit as efficiently as possible.`

// Prompt represents a complete prompt for the LLM with all market context.
type Prompt struct {
	Pair            Pair
	Primary         *Timeframe
	HigherTimeframe *Timeframe
	VolumeAnalysis  VolumeAnalysis
	Position        *Position
	Balance         decimal.Decimal
}

// NewPrompt creates a new Prompt instance.
func NewPrompt(pair Pair) *Prompt {
	return &Prompt{
		Pair: pair,
	}
}

// WithMarketContext populates the prompt with market data.
func (p *Prompt) WithMarketContext(
	primary *Timeframe,
	higherTimeframe *Timeframe,
	volumeAnalysis VolumeAnalysis,
	position *Position,
	balance decimal.Decimal,
) *Prompt {
	p.Primary = primary
	p.HigherTimeframe = higherTimeframe
	p.VolumeAnalysis = volumeAnalysis
	p.Position = position
	p.Balance = balance
	return p
}

// String returns the formatted user prompt for the LLM.
func (p *Prompt) String() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Market Analysis for %s\n\n", p.Pair.String()))

	if overview := p.formatMultiTimeframe(); overview != "" {
		sb.WriteString(overview)
	}

	sb.WriteString(p.formatRecentData(20))

	sb.WriteString(p.formatHistoricalSummary())

	sb.WriteString(p.formatVolumeAnalysis())

	if p.Position != nil {
		currentPrice := decimal.Zero
		if p.Primary != nil {
			if price, ok := p.Primary.LatestPrice(); ok {
				currentPrice = price
			}
		}
		sb.WriteString(p.formatPosition(currentPrice))
	} else {
		sb.WriteString("## Current Position\n\n")
		sb.WriteString("**Status:** No open position\n\n")
	}

	sb.WriteString("## Account Information\n\n")
	sb.WriteString(fmt.Sprintf("**Available Balance (%s):** %s\n\n", p.Pair.To, p.Balance.StringFixed(2)))

	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Analyze the market data and provide your trading decision in JSON format.\n")
	if p.Position != nil {
		if p.Position.Side == PositionSideLong {
			sb.WriteString("You currently have an open LONG position - decide whether to hold, close_long, or add to it.\n")
		} else if p.Position.Side == PositionSideShort {
			sb.WriteString("You currently have an open SHORT position - decide whether to hold, close_short, or add to it.\n")
		}
	} else {
		sb.WriteString("You have no open position - decide whether to open_long, open_short, or hold (wait).\n")
	}

	return sb.String()
}

func (p *Prompt) formatRecentData(limit int) string {
	var sb strings.Builder

	sb.WriteString("## Recent Market Data (Last 20 Candles)\n\n")

	if p.Primary == nil || len(p.Primary.Candles) == 0 {
		sb.WriteString("No data available\n\n")
		return sb.String()
	}

	klines := p.Primary.Candles

	startIdx := len(klines) - limit
	if startIdx < 0 {
		startIdx = 0
	}

	sb.WriteString("```\n")
	sb.WriteString("Time     | Open     | High     | Low      | Close    | Volume   | EMA20    | EMA50    | MACD   | RSI7  | RSI14 | ATR14\n")
	sb.WriteString("---------|----------|----------|----------|----------|----------|----------|----------|--------|-------|-------|-------\n")

	for i := startIdx; i < len(klines); i++ {
		k := klines[i]
		timeStr := k.OpenTime.Format("15:04")

		ind, hasIndicators := p.Primary.IndicatorForCandle(i)

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

func toFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}

func (p *Prompt) formatHistoricalSummary() string {
	var sb strings.Builder

	if p.Primary == nil || len(p.Primary.Candles) <= 20 {
		return ""
	}

	klines := p.Primary.Candles

	endIdx := len(klines) - 20
	if endIdx <= 0 {
		return ""
	}

	sb.WriteString("## Historical Context (Older Candles)\n\n")

	sb.WriteString("**Close Prices:** [")
	for i := 0; i < endIdx; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%.2f", toFloat(klines[i].Close)))
	}
	sb.WriteString("]\n\n")

	if len(p.Primary.Indicators) > 0 {
		sb.WriteString("**EMA20:** [")
		first := true
		for i := 0; i < endIdx; i++ {
			if ind, ok := p.Primary.IndicatorForCandle(i); ok {
				if !first {
					sb.WriteString(",")
				}
				sb.WriteString(fmt.Sprintf("%.2f", toFloat(ind.EMA20)))
				first = false
			}
		}
		sb.WriteString("]\n\n")

		sb.WriteString("**RSI14:** [")
		first = true
		for i := 0; i < endIdx; i++ {
			if ind, ok := p.Primary.IndicatorForCandle(i); ok {
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

func (p *Prompt) formatVolumeAnalysis() string {
	var sb strings.Builder

	sb.WriteString("## Volume Analysis\n\n")

	sb.WriteString(fmt.Sprintf("**Current Volume:** %s\n", p.VolumeAnalysis.CurrentVolume.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**Average Volume (20-period):** %s\n", p.VolumeAnalysis.AverageVolume.StringFixed(2)))

	relVol := p.VolumeAnalysis.RelativeVolume
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

	if len(p.VolumeAnalysis.VolumeSpikes) > 0 {
		sb.WriteString("\n**Volume Spikes (>1.5x avg):** ")
		startIdx := 0
		if len(p.VolumeAnalysis.VolumeSpikes) > 10 {
			startIdx = len(p.VolumeAnalysis.VolumeSpikes) - 10
		}
		sb.WriteString("Candles [")
		for i := startIdx; i < len(p.VolumeAnalysis.VolumeSpikes); i++ {
			if i > startIdx {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("#%d", p.VolumeAnalysis.VolumeSpikes[i]))
		}
		sb.WriteString("]\n")
	} else {
		sb.WriteString("\n**Volume Spikes:** None detected\n")
	}

	sb.WriteString("\n")

	return sb.String()
}

func (p *Prompt) formatMultiTimeframe() string {
	var sb strings.Builder

	hasPrimary := p.Primary != nil && p.Primary.Summary != nil
	hasHigher := p.HigherTimeframe != nil && p.HigherTimeframe.Summary != nil

	if !hasPrimary && !hasHigher {
		return ""
	}

	sb.WriteString("## Multi-Timeframe Overview\n\n")

	if hasPrimary {
		primarySummary := p.Primary.Summary

		sb.WriteString("**Primary Timeframe:**\n")
		sb.WriteString(fmt.Sprintf("- Price: %s | EMA20: %s | EMA50: %s | RSI14: %.1f\n",
			primarySummary.Price.StringFixed(2),
			primarySummary.EMA20.StringFixed(2),
			primarySummary.EMA50.StringFixed(2),
			toFloat(primarySummary.RSI14),
		))
		sb.WriteString(fmt.Sprintf("- Trend: %s\n", primarySummary.Trend.Title()))
	}

	if hasHigher {
		htf := p.HigherTimeframe.Summary
		sb.WriteString(fmt.Sprintf("\n**Higher Timeframe (%s):**\n", htf.Interval))
		sb.WriteString(fmt.Sprintf("- Price: %s | EMA20: %s | EMA50: %s | RSI14: %.1f\n",
			htf.Price.StringFixed(2),
			htf.EMA20.StringFixed(2),
			htf.EMA50.StringFixed(2),
			toFloat(htf.RSI14),
		))
		sb.WriteString(fmt.Sprintf("- Trend: %s\n", htf.Trend.Title()))

		if hasPrimary {
			primaryTrend := p.Primary.Summary.Trend

			if primaryTrend == htf.Trend && primaryTrend != TrendDirectionNeutral {
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

func (p *Prompt) formatPosition(currentPrice decimal.Decimal) string {
	var sb strings.Builder

	sb.WriteString("## Current Position\n\n")
	if p.Position.Side == PositionSideLong {
		sb.WriteString("**Status:** Open Long\n\n")
	} else if p.Position.Side == PositionSideShort {
		sb.WriteString("**Status:** Open Short\n\n")
	}

	sb.WriteString(fmt.Sprintf("**Entry Price:** %s\n", p.Position.EntryPrice.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("**Amount:** %s %s\n", p.Position.Amount.StringFixed(8), p.Pair.From))
	sb.WriteString(fmt.Sprintf("**Current Price:** %s\n", currentPrice.StringFixed(2)))

	pnl := p.Position.PnL(currentPrice)
	pnlPercent := decimal.Zero
	if !p.Position.EntryPrice.IsZero() {
		if p.Position.Side == PositionSideLong {
			pnlPercent = currentPrice.Sub(p.Position.EntryPrice).Div(p.Position.EntryPrice).Mul(decimal.NewFromInt(100))
		} else {
			pnlPercent = p.Position.EntryPrice.Sub(currentPrice).Div(p.Position.EntryPrice).Mul(decimal.NewFromInt(100))
		}
	}

	pnlSign := ""
	if pnl.GreaterThan(decimal.Zero) {
		pnlSign = "+"
	}

	sb.WriteString(fmt.Sprintf("**Unrealized P&L:** %s%s %s (%s%.2f%%)\n",
		pnlSign,
		pnl.StringFixed(2),
		p.Pair.To,
		pnlSign,
		toFloat(pnlPercent),
	))

	if !p.Position.EntryTime.IsZero() {
		duration := time.Since(p.Position.EntryTime)
		sb.WriteString(fmt.Sprintf("**Time Held:** %s\n", formatDuration(duration)))
	}

	sb.WriteString("\n")

	if !p.Position.StopLoss.IsZero() || !p.Position.TakeProfit.IsZero() {
		sb.WriteString("**Exit Levels:**\n")

		if !p.Position.StopLoss.IsZero() {
			distanceToSL := p.Position.StopLoss.Sub(currentPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
			sb.WriteString(fmt.Sprintf("- Stop Loss: %s (%s%.2f%% from current)\n",
				p.Position.StopLoss.StringFixed(2),
				"",
				toFloat(distanceToSL),
			))
		}

		if !p.Position.TakeProfit.IsZero() {
			distanceToTP := p.Position.TakeProfit.Sub(currentPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
			sb.WriteString(fmt.Sprintf("- Take Profit: %s (+%.2f%% from current)\n",
				p.Position.TakeProfit.StringFixed(2),
				toFloat(distanceToTP),
			))
		}

		if !p.Position.StopLoss.IsZero() && !p.Position.TakeProfit.IsZero() {
			risk := p.Position.EntryPrice.Sub(p.Position.StopLoss).Abs()
			reward := p.Position.TakeProfit.Sub(p.Position.EntryPrice).Abs()

			if risk.GreaterThan(decimal.Zero) {
				rrRatio := reward.Div(risk)
				sb.WriteString(fmt.Sprintf("- Risk:Reward Ratio: 1:%.2f\n", toFloat(rrRatio)))
			}
		}

		sb.WriteString("\n")
	}

	if p.Position.Invalidation != "" {
		sb.WriteString(fmt.Sprintf("**Invalidation Condition:** %s\n\n", p.Position.Invalidation))
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}
