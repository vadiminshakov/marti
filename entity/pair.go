package entity

import "fmt"

type Pair struct {
	From string
	To   string
}

func (p *Pair) String() string {
	return fmt.Sprintf("%s_%s", p.From, p.To)
}
