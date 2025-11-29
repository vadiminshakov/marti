// Package domain defines core data structures used throughout the trading bot.
package domain

import "fmt"

// Pair cryptocurrency trading pair.
type Pair struct {
	// From base currency symbol.
	From string
	// To quote currency symbol.
	To string
}

// String returns the string representation.
func (p *Pair) String() string {
	return fmt.Sprintf("%s_%s", p.From, p.To)
}

// Symbol returns the concatenated symbol representation.
func (p *Pair) Symbol() string {
	return fmt.Sprintf("%s%s", p.From, p.To)
}
