package aidecisions

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
	defaultAIDecisionDir   = "./wal/aidecisions"
	aiDecisionSegmentLimit = 20
	aiDecisionMaxSegments  = 5
	aiDecisionKeyPrefix    = "ai_decision_"
)

// WALStore persists AI decision events in a WAL for recovery/streaming purposes.
type WALStore struct {
	wal *gowal.Wal
	mu  sync.RWMutex
}

// NewWALStore initializes a WAL-backed AI decision store under the provided directory.
func NewWALStore(dir string) (*WALStore, error) {
	if dir == "" {
		dir = defaultAIDecisionDir
	}

	cfg := gowal.Config{
		Dir:              dir,
		Prefix:           "decision_",
		SegmentThreshold: aiDecisionSegmentLimit,
		MaxSegments:      aiDecisionMaxSegments,
		IsInSyncDiskMode: true,
	}

	wal, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "init AI decision WAL")
	}

	return &WALStore{wal: wal}, nil
}

// Save writes the AI decision event to WAL. Callers must ensure event.Pair is set.
func (s *WALStore) Save(event domain.AIDecisionEvent) error {
	if s == nil || s.wal == nil {
		return errors.New("AI decision store is not initialized")
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

// EventsAfter returns all AI decision events written after the provided WAL index.
func (s *WALStore) EventsAfter(index uint64) ([]domain.AIDecisionEventRecord, error) {
	if s == nil || s.wal == nil {
		return nil, errors.New("AI decision store is not initialized")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.wal.CurrentIndex()
	if current <= index {
		return nil, nil
	}

	records := make([]domain.AIDecisionEventRecord, 0, current-index)
	for idx := index + 1; idx <= current; idx++ {
		key, payload, ok := s.wal.Get(idx)
		if !ok || !strings.HasPrefix(key, aiDecisionKeyPrefix) {
			continue
		}
		var event domain.AIDecisionEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, errors.Wrap(err, "decode AI decision event")
		}
		records = append(records, domain.AIDecisionEventRecord{
			Index: idx,
			Event: event,
		})
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
		return errors.New("AI decision store is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.wal.Close()
}
