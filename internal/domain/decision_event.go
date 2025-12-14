package domain

// DecisionType enum for decision types
type DecisionType string

const (
	DecisionTypeAI  DecisionType = "ai"
	DecisionTypeDCA DecisionType = "dca"
)

// DecisionEventRecord bundles a decision event (AI or DCA) with its index.
type DecisionEventRecord struct {
	Index uint64
	Type  DecisionType
	// Event is either AIDecisionEvent or DCADecisionEvent
	Event any
}
