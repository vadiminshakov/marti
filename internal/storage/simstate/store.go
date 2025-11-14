package simstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/marti/internal/entity"
)

const defaultStateDir = "./wal/simulate"

// Store persists simulator state per trading pair so restarts keep balances and open positions.
type Store struct {
	path string
}

// NewStore creates a simulator state store for the given pair. Directory can be
// overridden via MARTI_SIMULATE_STATE_DIR env var.
func NewStore(pair entity.Pair) (*Store, error) {
	dir := os.Getenv("MARTI_SIMULATE_STATE_DIR")
	if dir == "" {
		dir = defaultStateDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, errors.Wrap(err, "create simulate state dir")
	}
	filename := fmt.Sprintf("%s.json", strings.ToLower(pair.String()))
	return &Store{path: filepath.Join(dir, filename)}, nil
}

// State represents all persisted simulator data.
type State struct {
	Pair       string            `json:"pair"`
	Wallet     map[string]string `json:"wallet"`
	MarginUsed string            `json:"margin_used"`
	Position   *StoredPosition   `json:"position,omitempty"`
}

// StoredPosition is a serializable snapshot of entity.Position.
type StoredPosition struct {
	EntryPrice   string              `json:"entry_price"`
	Amount       string              `json:"amount"`
	StopLoss     string              `json:"stop_loss"`
	TakeProfit   string              `json:"take_profit"`
	Invalidation string              `json:"invalidation"`
	EntryTime    time.Time           `json:"entry_time"`
	Side         entity.PositionSide `json:"side"`
}

// Load reads simulator state from disk.
func (s *Store) Load() (*State, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}
	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "read simulate state")
	}
	if len(payload) == 0 {
		return nil, nil
	}
	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, errors.Wrap(err, "decode simulate state")
	}
	return &state, nil
}

// Save writes simulator state to disk atomically via temp file.
func (s *Store) Save(state State) error {
	if s == nil || s.path == "" {
		return nil
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode simulate state")
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return errors.Wrap(err, "write simulate state temp file")
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return errors.Wrap(err, "persist simulate state")
	}
	return nil
}

// NewStoredPosition converts entity.Position into its stored representation.
func NewStoredPosition(pos *entity.Position) *StoredPosition {
	if pos == nil {
		return nil
	}
	return &StoredPosition{
		EntryPrice:   pos.EntryPrice.String(),
		Amount:       pos.Amount.String(),
		StopLoss:     pos.StopLoss.String(),
		TakeProfit:   pos.TakeProfit.String(),
		Invalidation: pos.Invalidation,
		EntryTime:    pos.EntryTime,
		Side:         pos.Side,
	}
}

// ToPosition reconstructs entity.Position from stored data.
func (sp *StoredPosition) ToPosition() (*entity.Position, error) {
	if sp == nil {
		return nil, nil
	}
	entryPrice, err := decimal.NewFromString(sp.EntryPrice)
	if err != nil {
		return nil, errors.Wrap(err, "decode position entry price")
	}
	amount, err := decimal.NewFromString(sp.Amount)
	if err != nil {
		return nil, errors.Wrap(err, "decode position amount")
	}
	stopLoss := decimal.Zero
	if sp.StopLoss != "" {
		stopLoss, err = decimal.NewFromString(sp.StopLoss)
		if err != nil {
			return nil, errors.Wrap(err, "decode position stop loss")
		}
	}
	takeProfit := decimal.Zero
	if sp.TakeProfit != "" {
		takeProfit, err = decimal.NewFromString(sp.TakeProfit)
		if err != nil {
			return nil, errors.Wrap(err, "decode position take profit")
		}
	}
	return &entity.Position{
		EntryPrice:   entryPrice,
		Amount:       amount,
		StopLoss:     stopLoss,
		TakeProfit:   takeProfit,
		Invalidation: sp.Invalidation,
		EntryTime:    sp.EntryTime,
		Side:         sp.Side,
	}, nil
}
