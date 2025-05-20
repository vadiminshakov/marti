package entity

//go:generate stringer -type=Action
type Action int

const (
	ActionNull Action = iota
	ActionBuy
	ActionSell
)
