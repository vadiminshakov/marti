package averaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/vadiminshakov/gowal"
)

const (
	tradeIntentKeyPrefix     = "dca_trade_intent_"
	tradeIntentStatusPending = "pending"
	tradeIntentStatusDone    = "done"
	tradeIntentStatusFailed  = "failed"
)

type tradeIntentAction string

const (
	intentActionBuy  tradeIntentAction = "buy"
	intentActionSell tradeIntentAction = "sell"
)

// tradeIntentRecord is the WAL-persisted record of a single order submitted to
// the exchange.  It is written before the order is sent and updated as the
// order progresses through pending → done/failed, so the strategy can recover
// open orders after a restart.
type tradeIntentRecord struct {
	// Amount is the quote-currency value of the order (e.g. USDT).
	// For buys: the quote amount to spend.
	// For sells: the quote-currency cost basis of the position being sold
	// (used for PnL accounting, not for the actual order size).
	Amount decimal.Decimal `json:"amount"`

	// BaseAmount is the base-currency size of the order (e.g. BTC).
	// For buys: amount to buy as calculated at intent creation time; may be
	// overridden by UpdateAmount if the actual fill differs.
	// For sells: the exact base amount sent to the exchange.
	BaseAmount decimal.Decimal `json:"base_amount,omitempty"`

	// Price is the market price at the moment the intent was created.
	Price decimal.Decimal `json:"price"`

	// Time is the wall-clock time when the intent was prepared.
	Time time.Time `json:"time"`

	// ID is a UUID assigned at creation and used as the clientOrderID on the
	// exchange, as the WAL key suffix, and as the dedup key in
	// cycle.ProcessedTradeIDs.
	ID string `json:"id"`

	// Status is one of "pending", "done", or "failed".
	Status string `json:"status"`

	Action tradeIntentAction `json:"action"`

	// Error holds the error message when Status == "failed".
	Error string `json:"error,omitempty"`

	// TradePart is the 1-indexed step number within the current cycle.
	TradePart int `json:"trade_part,omitempty"`

	// IsFullSell is true when the sell closes the entire position, triggering
	// a cycle reset after the fill is applied.
	IsFullSell bool `json:"is_full_sell,omitempty"`
}

// tradeJournal is an in-memory index of all trade intents for the current
// process lifetime, backed by the WAL for crash-safety.
//
// Every intent is appended to intents and indexed by ID in index.  On startup
// NewStrategy replays the WAL and passes the recovered intents to
// newTradeJournal, so the journal is fully restored before trading resumes.
type tradeJournal struct {
	// index provides O(1) lookup of an intent by ID, used during reconciliation
	// and dedup checks.
	index map[string]*tradeIntentRecord

	wal *gowal.Wal

	// intents is the append-only ordered list of all intents seen this session.
	// reconcileTradeIntents iterates this to find pending entries.
	intents []*tradeIntentRecord
}

func newTradeJournal(wal *gowal.Wal, intents []*tradeIntentRecord) *tradeJournal {
	index := make(map[string]*tradeIntentRecord)
	for _, intent := range intents {
		index[intent.ID] = intent
	}

	return &tradeJournal{
		wal:     wal,
		intents: intents,
		index:   index,
	}
}

// Prepare creates a new intent with status "pending", persists it to the WAL,
// and adds it to the in-memory index.  It must be called before sending the
// order to the exchange so that a crash between persist and send leaves a
// recoverable pending record.
func (j *tradeJournal) Prepare(action tradeIntentAction, price, amountQuote, amountBase decimal.Decimal, eventTime time.Time, tradePart int, isFullSell bool) (*tradeIntentRecord, error) {
	intent := &tradeIntentRecord{
		ID:         uuid.New().String(),
		Status:     tradeIntentStatusPending,
		Action:     action,
		Amount:     amountQuote,
		BaseAmount: amountBase,
		Price:      price,
		Time:       eventTime,
		TradePart:  tradePart,
		IsFullSell: isFullSell,
	}

	if err := j.persist(intent); err != nil {
		return nil, err
	}

	j.intents = append(j.intents, intent)
	j.index[intent.ID] = intent

	return intent, nil
}

// MarkFailed sets the intent status to "failed", records the error message, and
// persists the updated record to the WAL.  Failed intents are not retried on
// restart; they exist only for audit purposes.
func (j *tradeJournal) MarkFailed(intent *tradeIntentRecord, err error) error {
	if intent == nil {
		return nil
	}

	intent.Status = tradeIntentStatusFailed
	if err != nil {
		intent.Error = err.Error()
	} else {
		intent.Error = ""
	}

	return j.persist(intent)
}

// MarkDone sets the intent status to "done" and persists it.  Called after the
// fill has been successfully applied to the cycle so the intent won't be
// picked up as pending on the next restart.
func (j *tradeJournal) MarkDone(intent *tradeIntentRecord) error {
	if intent == nil {
		return nil
	}

	intent.Status = tradeIntentStatusDone
	intent.Error = ""

	return j.persist(intent)
}

// UpdateAmount overwrites the intent's quote Amount and persists it.  Used
// during reconciliation when the actual filled quote differs from the amount
// recorded at intent creation (e.g. partial or adjusted fills).
func (j *tradeJournal) UpdateAmount(intent *tradeIntentRecord, amount decimal.Decimal) error {
	if intent == nil {
		return nil
	}

	intent.Amount = amount

	return j.persist(intent)
}

// Intents returns the full intent list for this session.  Used by
// reconcileTradeIntents to iterate over pending entries.
func (j *tradeJournal) Intents() []*tradeIntentRecord {
	return j.intents
}

// persist serialises intent to JSON and writes it to the WAL under the key
// "dca_trade_intent_<id>".  Subsequent writes for the same ID overwrite the
// previous record in the WAL replay (last-write-wins), so the recovered state
// always reflects the most recent status.
func (j *tradeJournal) persist(intent *tradeIntentRecord) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return errors.Wrap(err, "failed to marshal trade intent")
	}

	key := fmt.Sprintf("%s%s", tradeIntentKeyPrefix, intent.ID)
	nextIndex := j.wal.CurrentIndex() + 1

	if err := j.wal.Write(gowal.Record{Index: nextIndex, Key: key, Value: data}); err != nil {
		return errors.Wrap(err, "failed to write trade intent")
	}

	return nil
}
