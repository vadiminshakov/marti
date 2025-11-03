package entity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// Decision represents the AI's trading decision.
type Decision struct {
	Action      string   `json:"action"`
	RiskPercent float64  `json:"risk_percent"`
	Reasoning   string   `json:"reasoning"`
	ExitPlan    ExitPlan `json:"exit_plan"`
}

// ExitPlan defines exit strategy for a trade.
type ExitPlan struct {
	StopLossPrice         float64 `json:"stop_loss_price"`
	TakeProfitPrice       float64 `json:"take_profit_price"`
	InvalidationCondition string  `json:"invalidation_condition"`
}

// NewDecision builds a validated trading decision from raw LLM response.
func NewDecision(raw string, hasPosition bool) (*Decision, error) {
	response := sanitizeDecisionPayload(raw)

	if !json.Valid([]byte(response)) {
		return nil, errors.New("invalid JSON structure")
	}

	var decision Decision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		return nil, errors.Wrap(err, "JSON unmarshal error")
	}

	if err := validateDecisionRequiredFields(&decision); err != nil {
		return nil, errors.Wrap(err, "missing required fields")
	}

	if err := validateDecisionAction(&decision); err != nil {
		return nil, err
	}

	if err := validateDecisionRiskPercent(&decision); err != nil {
		return nil, err
	}

	if err := validateDecisionActionConsistency(&decision, hasPosition); err != nil {
		return nil, errors.Wrap(err, "action consistency error")
	}

	if decision.Action == "buy" {
		if err := validateDecisionExitPlan(&decision); err != nil {
			return nil, errors.Wrap(err, "exit plan validation error")
		}
	}

	return &decision, nil
}

func sanitizeDecisionPayload(raw string) string {
	response := strings.TrimSpace(raw)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	return strings.TrimSpace(response)
}

func validateDecisionRequiredFields(decision *Decision) error {
	if decision.Action == "" {
		return errors.New("action field is required")
	}
	if decision.Reasoning == "" {
		return errors.New("reasoning field is required")
	}
	return nil
}

func validateDecisionAction(decision *Decision) error {
	validActions := map[string]bool{"buy": true, "sell": true, "hold": true, "close": true}
	if !validActions[decision.Action] {
		return fmt.Errorf("Invalid action: %s", decision.Action)
	}
	return nil
}

func validateDecisionRiskPercent(decision *Decision) error {
	if decision.RiskPercent < 0 || decision.RiskPercent > 15 {
		return fmt.Errorf("Invalid risk_percent: %f (must be 0.0-15.0)", decision.RiskPercent)
	}
	return nil
}

func validateDecisionActionConsistency(decision *Decision, hasPosition bool) error {
	action := decision.Action

	if action == "buy" && hasPosition {
		return errors.New("cannot buy when position already exists")
	}

	if action == "close" && !hasPosition {
		return errors.New("cannot close when no position exists")
	}

	return nil
}

func validateDecisionExitPlan(decision *Decision) error {
	exitPlan := decision.ExitPlan

	if exitPlan.StopLossPrice <= 0 {
		return errors.New("stop_loss_price must be greater than 0")
	}

	if exitPlan.TakeProfitPrice <= 0 {
		return errors.New("take_profit_price must be greater than 0")
	}

	if exitPlan.InvalidationCondition == "" {
		return errors.New("invalidation_condition is required")
	}

	if exitPlan.StopLossPrice >= exitPlan.TakeProfitPrice {
		return errors.New("stop_loss_price must be less than take_profit_price")
	}

	return nil
}
