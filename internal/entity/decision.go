package entity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// TradingDecision represents the AI's trading decision.
type TradingDecision struct {
	Decision DecisionDetails `json:"decision"`
}

// DecisionDetails contains the details of the trading decision.
type DecisionDetails struct {
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

// DecisionSeverity represents diagnostic level for decision parsing.
type DecisionSeverity string

const (
	DecisionSeverityInfo  DecisionSeverity = "info"
	DecisionSeverityWarn  DecisionSeverity = "warn"
	DecisionSeverityError DecisionSeverity = "error"
)

// DecisionParseOutcome captures the result of parsing the AI decision payload.
type DecisionParseOutcome struct {
	Decision  *TradingDecision
	Defaulted bool
	Reason    string
	Severity  DecisionSeverity
}

// ParseTradingDecision builds a validated trading decision from raw LLM response.
func ParseTradingDecision(raw string, hasPosition bool) (*DecisionParseOutcome, error) {
	response := sanitizeDecisionPayload(raw)

	if !json.Valid([]byte(response)) {
		reason := "Invalid JSON structure"
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityError,
		}, nil
	}

	var decision TradingDecision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		reason := "JSON unmarshal error"
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityError,
		}, nil
	}

	if err := validateDecisionRequiredFields(&decision); err != nil {
		reason := fmt.Sprintf("Missing required fields: %v", err)
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityError,
		}, nil
	}

	if err := validateDecisionAction(&decision); err != nil {
		reason := err.Error()
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityError,
		}, nil
	}

	if err := validateDecisionRiskPercent(&decision); err != nil {
		reason := err.Error()
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityError,
		}, nil
	}

	if err := validateDecisionActionConsistency(&decision, hasPosition); err != nil {
		reason := fmt.Sprintf("Action consistency error: %v", err)
		return &DecisionParseOutcome{
			Decision:  createDefaultHoldDecision(reason),
			Defaulted: true,
			Reason:    reason,
			Severity:  DecisionSeverityWarn,
		}, nil
	}

	if decision.Decision.Action == "buy" {
		if err := validateDecisionExitPlan(&decision); err != nil {
			reason := fmt.Sprintf("Exit plan validation error: %v", err)
			return &DecisionParseOutcome{
				Decision:  createDefaultHoldDecision(reason),
				Defaulted: true,
				Reason:    reason,
				Severity:  DecisionSeverityError,
			}, nil
		}
	}

	return &DecisionParseOutcome{
		Decision:  &decision,
		Defaulted: false,
		Reason:    "Decision validation passed",
		Severity:  DecisionSeverityInfo,
	}, nil
}

func sanitizeDecisionPayload(raw string) string {
	response := strings.TrimSpace(raw)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	return strings.TrimSpace(response)
}

func validateDecisionRequiredFields(decision *TradingDecision) error {
	if decision.Decision.Action == "" {
		return errors.New("action field is required")
	}
	if decision.Decision.Reasoning == "" {
		return errors.New("reasoning field is required")
	}
	return nil
}

func validateDecisionAction(decision *TradingDecision) error {
	validActions := map[string]bool{"buy": true, "sell": true, "hold": true, "close": true}
	if !validActions[decision.Decision.Action] {
		return fmt.Errorf("Invalid action: %s", decision.Decision.Action)
	}
	return nil
}

func validateDecisionRiskPercent(decision *TradingDecision) error {
	if decision.Decision.RiskPercent < 0 || decision.Decision.RiskPercent > 15 {
		return fmt.Errorf("Invalid risk_percent: %f (must be 0.0-15.0)", decision.Decision.RiskPercent)
	}
	return nil
}

func validateDecisionActionConsistency(decision *TradingDecision, hasPosition bool) error {
	action := decision.Decision.Action

	if action == "buy" && hasPosition {
		return errors.New("cannot buy when position already exists")
	}

	if action == "close" && !hasPosition {
		return errors.New("cannot close when no position exists")
	}

	return nil
}

func validateDecisionExitPlan(decision *TradingDecision) error {
	exitPlan := decision.Decision.ExitPlan

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

func createDefaultHoldDecision(reason string) *TradingDecision {
	return &TradingDecision{
		Decision: DecisionDetails{
			Action:      "hold",
			RiskPercent: 0.0,
			Reasoning:   fmt.Sprintf("Validation failed: %s. Defaulting to hold for safety.", reason),
			ExitPlan: ExitPlan{
				StopLossPrice:         0.0,
				TakeProfitPrice:       0.0,
				InvalidationCondition: "",
			},
		},
	}
}
