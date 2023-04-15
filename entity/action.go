package entity

type Action int8

const (
	ActionNull Action = iota
	ActionBuy
	ActionSell
)
