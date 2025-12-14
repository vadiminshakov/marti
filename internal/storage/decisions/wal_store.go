package decisions

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/domain"
)

const (
	DefaultDir   = "./wal/aidecisions" // Keep using the same dir to preserve history
	segmentLimit = 100
	maxSegments  = 10

	aiDecisionKeyPrefix  = "ai_decision_"
	dcaDecisionKeyPrefix = "dca_decision_"
)

// WALStore persists decision events in a WAL.
type WALStore struct {
	wal *gowal.Wal
	mu  sync.RWMutex
}

// NewWALStore initializes a WAL-backed decision store.
func NewWALStore(dir string) (*WALStore, error) {
	if dir == "" {
		dir = DefaultDir
	}

	cfg := gowal.Config{
		Dir:              dir,
		Prefix:           "decision_",
		SegmentThreshold: segmentLimit,
		MaxSegments:      maxSegments,
		IsInSyncDiskMode: true,
	}

	wal, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "init decision WAL")
	}

	return &WALStore{wal: wal}, nil
}

// SaveAI writes the AI decision event to WAL.
func (s *WALStore) SaveAI(event domain.AIDecisionEvent) error {
	if s == nil || s.wal == nil {
		return errors.New("decision store is not initialized")
	}
	if event.Pair == "" {
		return fmt.Errorf("AI decision event pair is required")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "marshal AI decision event")
	}

	key := fmt.Sprintf("%s%s", aiDecisionKeyPrefix, event.Pair)

	s.mu.Lock()
	defer s.mu.Unlock()

	nextIndex := s.wal.CurrentIndex() + 1
	return s.wal.Write(nextIndex, key, payload)
}

// SaveDCA writes the DCA decision event to WAL.
func (s *WALStore) SaveDCA(event domain.DCADecisionEvent) error {
	if s == nil || s.wal == nil {
		return errors.New("decision store is not initialized")
	}
	if event.Pair == "" {
		return fmt.Errorf("DCA decision event pair is required")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return errors.Wrap(err, "marshal DCA decision event")
	}

	key := fmt.Sprintf("%s%s", dcaDecisionKeyPrefix, event.Pair)

	s.mu.Lock()
	defer s.mu.Unlock()

	nextIndex := s.wal.CurrentIndex() + 1
	return s.wal.Write(nextIndex, key, payload)
}

// EventsAfter returns all decision events written after the provided WAL index.
func (s *WALStore) EventsAfter(index uint64) ([]domain.DecisionEventRecord, error) {
	if s == nil || s.wal == nil {
		return nil, errors.New("decision store is not initialized")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.wal.CurrentIndex()
	if current <= index {
		return nil, nil
	}

	records := make([]domain.DecisionEventRecord, 0, current-index)
	for idx := index + 1; idx <= current; idx++ {
		key, payload, err := s.wal.Get(idx)
		if err != nil {
			continue
		}

		if strings.HasPrefix(key, aiDecisionKeyPrefix) {
			var event domain.AIDecisionEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				return nil, errors.Wrap(err, "decode AI decision event")
			}
			records = append(records, domain.DecisionEventRecord{
				Index: idx,
				Type:  domain.DecisionTypeAI,
				Event: event,
			})
		} else if strings.HasPrefix(key, dcaDecisionKeyPrefix) {
			var event domain.DCADecisionEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				return nil, errors.Wrap(err, "decode DCA decision event")
			}
			records = append(records, domain.DecisionEventRecord{
				Index: idx,
				Type:  domain.DecisionTypeDCA,
				Event: event,
			})
		}
	}

	return records, nil
}

// CurrentIndex returns the latest WAL index stored.
func (s *WALStore) CurrentIndex() uint64 {
	if s == nil || s.wal == nil {
		return 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.wal.CurrentIndex()
}

// Close closes the underlying WAL.
func (s *WALStore) Close() error {
	if s == nil || s.wal == nil {
		return errors.New("decision store is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.wal.Close()
}
