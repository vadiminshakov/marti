package domain

// Action represents the type of trading action to be performed.
type Action int

const (
	ActionOpenLong Action = iota
	ActionCloseLong
	ActionOpenShort
	ActionCloseShort
)

// action string constants to avoid magic strings
const (
	actionStringOpenLong   = "open_long"
	actionStringCloseLong  = "close_long"
	actionStringOpenShort  = "open_short"
	actionStringCloseShort = "close_short"
)

// isValidActionString checks if the string is a valid action
func isValidActionString(s string) bool {
	switch s {
	case actionStringOpenLong, actionStringCloseLong,
		actionStringOpenShort, actionStringCloseShort:
		return true
	}
	return false
}

// String returns the string representation of the action
func (a Action) String() string {
	switch a {
	case ActionOpenLong:
		return actionStringOpenLong
	case ActionCloseLong:
		return actionStringCloseLong
	case ActionOpenShort:
		return actionStringOpenShort
	case ActionCloseShort:
		return actionStringCloseShort
	default:
		return "unknown"
	}
}
