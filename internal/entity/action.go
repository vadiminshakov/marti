package entity

//go:generate stringer -type=Action

// Action represents the type of trading action to be performed.
type Action int

const (
	// ActionNull represents no action or an undefined action
	ActionNull Action = iota
	// ActionOpenLong opens a long position (buy to open)
	ActionOpenLong
	// ActionCloseLong closes a long position (sell to close)
	ActionCloseLong
	// ActionOpenShort opens a short position (sell to open)
	ActionOpenShort
	// ActionCloseShort closes a short position (buy to close)
	ActionCloseShort
)
