package balancesnapshots

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/vadiminshakov/gowal"
	"github.com/vadiminshakov/marti/internal/entity"
)

const (
	defaultSnapshotDir   = "./wal/balance"
	snapshotSegmentLimit = 1000
	snapshotMaxSegments  = 100
	snapshotKeyPrefix    = "balance_snapshot_"
)

// WALStore persists balance snapshots in a WAL for recovery/streaming purposes.
type WALStore struct {
	wal *gowal.Wal
	mu  sync.RWMutex
}

// NewWALStore initializes a WAL-backed snapshot store under the provided directory.
func NewWALStore(dir string) (*WALStore, error) {
	if dir == "" {
		dir = defaultSnapshotDir
	}

	cfg := gowal.Config{
		Dir:              dir,
		Prefix:           "snapshot_",
		SegmentThreshold: snapshotSegmentLimit,
		MaxSegments:      snapshotMaxSegments,
		IsInSyncDiskMode: true,
	}

	wal, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "init balance snapshot WAL")
	}

	return &WALStore{wal: wal}, nil
}

// Save writes the snapshot to WAL. Callers must ensure snapshot.Pair is set.
func (s *WALStore) Save(snapshot entity.BalanceSnapshot) error {
	if s == nil || s.wal == nil {
		return errors.New("balance snapshot store is not initialized")
	}
	if snapshot.Pair == "" {
		return fmt.Errorf("balance snapshot pair is required")
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return errors.Wrap(err, "marshal balance snapshot")
	}

	key := fmt.Sprintf("%s%s", snapshotKeyPrefix, snapshot.Pair)

	s.mu.Lock()
	defer s.mu.Unlock()

	nextIndex := s.wal.CurrentIndex() + 1
	return s.wal.Write(nextIndex, key, payload)
}

// SnapshotsAfter returns all balance snapshots written after the provided WAL index.
func (s *WALStore) SnapshotsAfter(index uint64) ([]entity.BalanceSnapshotRecord, error) {
	if s == nil || s.wal == nil {
		return nil, errors.New("balance snapshot store is not initialized")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.wal.CurrentIndex()
	if current <= index {
		return nil, nil
	}

	records := make([]entity.BalanceSnapshotRecord, 0, current-index)
	for idx := index + 1; idx <= current; idx++ {
		key, payload, ok := s.wal.Get(idx)
		if !ok || !strings.HasPrefix(key, snapshotKeyPrefix) {
			continue
		}
		var snapshot entity.BalanceSnapshot
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return nil, errors.Wrap(err, "decode balance snapshot")
		}
		records = append(records, entity.BalanceSnapshotRecord{
			Index:    idx,
			Snapshot: snapshot,
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
		return errors.New("balance snapshot store is not initialized")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.wal.Close()
}
