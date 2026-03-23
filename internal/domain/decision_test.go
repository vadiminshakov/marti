package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDecision_HoldRequiresZeroRiskAndEmptyExitPlan(t *testing.T) {
	t.Parallel()

	decision, err := NewDecision(`{
		"action":"hold",
		"risk_percent":0,
		"reasoning":"No clear edge",
		"exit_plan":{
			"stop_loss_price":0,
			"take_profit_price":0,
			"invalidation_condition":""
		}
	}`)

	require.NoError(t, err)
	require.Equal(t, ActionHold, decision.ToAction())
}

func TestNewDecision_HoldRejectsNonZeroRisk(t *testing.T) {
	t.Parallel()

	_, err := NewDecision(`{
		"action":"hold",
		"risk_percent":1,
		"reasoning":"Still tempted",
		"exit_plan":{
			"stop_loss_price":0,
			"take_profit_price":0,
			"invalidation_condition":""
		}
	}`)

	require.ErrorContains(t, err, "must be 0 for hold")
}

func TestNewDecision_OpenRejectsZeroRisk(t *testing.T) {
	t.Parallel()

	_, err := NewDecision(`{
		"action":"open_long",
		"risk_percent":0,
		"reasoning":"Breakout",
		"exit_plan":{
			"stop_loss_price":95,
			"take_profit_price":110,
			"invalidation_condition":"Price closes below 95"
		}
	}`)

	require.ErrorContains(t, err, "must be > 0 for open actions")
}

func TestNewDecision_CloseRejectsNonEmptyExitPlan(t *testing.T) {
	t.Parallel()

	_, err := NewDecision(`{
		"action":"close_long",
		"risk_percent":0,
		"reasoning":"Target hit",
		"exit_plan":{
			"stop_loss_price":95,
			"take_profit_price":0,
			"invalidation_condition":""
		}
	}`)

	require.ErrorContains(t, err, "non-entry actions")
}
