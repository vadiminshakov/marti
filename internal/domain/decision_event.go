package domain

// DecisionType enum for decision types
type DecisionType string

const (
	DecisionTypeAI        DecisionType = "ai"
	DecisionTypeAveraging DecisionType = "averaging"
)

// DecisionEventRecord bundles a decision event (AI or Averaging) with its index.
type DecisionEventRecord struct {
	Index uint64
	Type  DecisionType
	// Event is either AIDecisionEvent or AveragingDecisionEvent
	Event any
}
