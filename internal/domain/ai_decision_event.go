package entity

import (
	"strings"
	"time"
)

// AIDecisionEvent represents a trading decision made by AI for a trading pair.
// String fields avoid precision issues when rendered in UI layers.
type AIDecisionEvent struct {
	Timestamp               time.Time `json:"ts"`
	Pair                    string    `json:"pair"`
	Model                   string    `json:"model,omitempty"`
	Action                  string    `json:"action"`
	Reasoning               string    `json:"reasoning"`
	RiskPercent             float64   `json:"risk_percent,omitempty"`
	TakeProfitPrice         float64   `json:"take_profit_price,omitempty"`
	StopLossPrice           float64   `json:"stop_loss_price,omitempty"`
	InvalidationCondition   string    `json:"invalidation_condition,omitempty"`
	CurrentPrice            string    `json:"current_price,omitempty"`
	QuoteBalance            string    `json:"quote_balance,omitempty"`
	PositionAmount          string    `json:"position_amount,omitempty"`
	PositionSide            string    `json:"position_side,omitempty"`
	PositionEntryPrice      string    `json:"position_entry_price,omitempty"`
}

// NewAIDecisionEvent creates a new AIDecisionEvent with normalized model name.
// It removes the gpt://folder_id/ prefix pattern from the model field.
func NewAIDecisionEvent(
	timestamp time.Time,
	pair string,
	model string,
	action string,
	reasoning string,
	riskPercent float64,
	takeProfitPrice float64,
	stopLossPrice float64,
	invalidationCondition string,
	currentPrice string,
	quoteBalance string,
	positionAmount string,
	positionSide string,
	positionEntryPrice string,
) AIDecisionEvent {
	// Normalize model name by removing gpt://folder_id/ prefix
	normalizedModel := model
	if idx := strings.Index(normalizedModel, "gpt://"); idx >= 0 {
		remainder := normalizedModel[idx+6:] // skip "gpt://"
		if slashIdx := strings.Index(remainder, "/"); slashIdx >= 0 {
			normalizedModel = remainder[slashIdx+1:] // take everything after the slash
		}
	}

	return AIDecisionEvent{
		Timestamp:             timestamp,
		Pair:                  pair,
		Model:                 normalizedModel,
		Action:                action,
		Reasoning:             reasoning,
		RiskPercent:           riskPercent,
		TakeProfitPrice:       takeProfitPrice,
		StopLossPrice:         stopLossPrice,
		InvalidationCondition: invalidationCondition,
		CurrentPrice:          currentPrice,
		QuoteBalance:          quoteBalance,
		PositionAmount:        positionAmount,
		PositionSide:          positionSide,
		PositionEntryPrice:    positionEntryPrice,
	}
}

// AIDecisionEventRecord bundles an AI decision event with the log index it originated from.
type AIDecisionEventRecord struct {
	Index uint64
	Event AIDecisionEvent
}
