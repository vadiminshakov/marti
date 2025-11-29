package domain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// Decision AI's trading decision.
type Decision struct {
	Action      string   `json:"action"`
	RiskPercent float64  `json:"risk_percent"`
	Reasoning   string   `json:"reasoning"`
	ExitPlan    ExitPlan `json:"exit_plan"`
}

// ExitPlan exit strategy for a trade.
type ExitPlan struct {
	StopLossPrice         float64 `json:"stop_loss_price"`
	TakeProfitPrice       float64 `json:"take_profit_price"`
	InvalidationCondition string  `json:"invalidation_condition"`
}

// NewDecision builds a validated trading decision.
func NewDecision(raw string) (*Decision, error) {
	response := sanitizeDecisionPayload(raw)

	if !json.Valid([]byte(response)) {
		return nil, errors.New("invalid JSON structure")
	}

	var decision Decision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		return nil, errors.Wrap(err, "JSON unmarshal error")
	}

	if err := decision.Validate(); err != nil {
		return nil, err
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

// Validate validates the decision.
func (d *Decision) Validate() error {
	if err := d.validateRequiredFields(); err != nil {
		return errors.Wrap(err, "missing required fields")
	}

	if err := d.validateAction(); err != nil {
		return err
	}

	if err := d.validateRiskPercent(); err != nil {
		return err
	}

	// exit plan is required for opening positions (open_long, open_short)
	if d.Action == actionStringOpenLong || d.Action == actionStringOpenShort {
		if err := d.validateExitPlan(); err != nil {
			return errors.Wrap(err, "exit plan validation error")
		}
	}

	return nil
}

func (d *Decision) validateRequiredFields() error {
	if d.Action == "" {
		return errors.New("action field is required")
	}
	if d.Reasoning == "" {
		return errors.New("reasoning field is required")
	}
	return nil
}

func (d *Decision) validateAction() error {
	if !isValidActionString(d.Action) {
		return fmt.Errorf("Invalid action: %s", d.Action)
	}
	return nil
}

func (d *Decision) validateRiskPercent() error {
	if d.RiskPercent < 0 || d.RiskPercent > 15 {
		return fmt.Errorf("Invalid risk_percent: %f (must be 0.0-15.0)", d.RiskPercent)
	}
	return nil
}

func (d *Decision) validateExitPlan() error {
	exitPlan := d.ExitPlan

	if exitPlan.StopLossPrice <= 0 {
		return errors.New("stop_loss_price must be greater than 0")
	}

	if exitPlan.TakeProfitPrice <= 0 {
		return errors.New("take_profit_price must be greater than 0")
	}

	if exitPlan.InvalidationCondition == "" {
		return errors.New("invalidation_condition is required")
	}

	switch d.Action {
	case actionStringOpenLong:
		if exitPlan.StopLossPrice >= exitPlan.TakeProfitPrice {
			return errors.New("stop_loss_price must be less than take_profit_price for long positions")
		}
	case actionStringOpenShort:
		if exitPlan.StopLossPrice <= exitPlan.TakeProfitPrice {
			return errors.New("stop_loss_price must be greater than take_profit_price for short positions")
		}
	}

	return nil
}

// ToAction converts decision action string to typed Action.
func (d *Decision) ToAction() Action {
	switch d.Action {
	case actionStringOpenLong:
		return ActionOpenLong
	case actionStringCloseLong:
		return ActionCloseLong
	case actionStringOpenShort:
		return ActionOpenShort
	case actionStringCloseShort:
		return ActionCloseShort
	default:
		return ActionOpenLong // default fallback
	}
}
