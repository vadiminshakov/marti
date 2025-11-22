package entity

//go:generate stringer -type=Action

// Action represents the type of trading action to be performed.
type Action int

const (
	ActionNull Action = iota
	ActionOpenLong
	ActionCloseLong
	ActionOpenShort
	ActionCloseShort
)
