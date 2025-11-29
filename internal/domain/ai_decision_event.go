package domain

import (
	"time"
)

// AIDecisionEvent trading decision made by AI.
type AIDecisionEvent struct {
	Timestamp             time.Time `json:"ts"`
	Pair                  string    `json:"pair"`
	Model                 string    `json:"model,omitempty"`
	Action                string    `json:"action"`
	Reasoning             string    `json:"reasoning"`
	RiskPercent           float64   `json:"risk_percent,omitempty"`
	TakeProfitPrice       float64   `json:"take_profit_price,omitempty"`
	StopLossPrice         float64   `json:"stop_loss_price,omitempty"`
	InvalidationCondition string    `json:"invalidation_condition,omitempty"`
	CurrentPrice          string    `json:"current_price,omitempty"`
	QuoteBalance          string    `json:"quote_balance,omitempty"`
	PositionAmount        string    `json:"position_amount,omitempty"`
	PositionSide          string    `json:"position_side,omitempty"`
	PositionEntryPrice    string    `json:"position_entry_price,omitempty"`
}

// NewAIDecisionEvent creates a new AIDecisionEvent.
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
	// normalize model name by removing gpt://folder_id/ prefix
	normalizedModel := normalizeModelName(model)

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

// AIDecisionEventRecord bundles an AI decision event.
type AIDecisionEventRecord struct {
	Index uint64
	Event AIDecisionEvent
}
