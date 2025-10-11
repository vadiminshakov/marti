package entity

//go:generate stringer -type=Action

// Action represents the type of trading action to be performed.
type Action int

const (
	// ActionNull represents no action or an undefined action
	ActionNull Action = iota
	// ActionBuy represents a buy order action
	ActionBuy
	// ActionSell represents a sell order action
	ActionSell
)
