package dca

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

type tradeIntentRecord struct {
	ID         string            `json:"id"`
	Status     string            `json:"status"`
	Action     tradeIntentAction `json:"action"`
	Amount     decimal.Decimal   `json:"amount"`
	Price      decimal.Decimal   `json:"price"`
	Time       time.Time         `json:"time"`
	TradePart  int               `json:"trade_part,omitempty"`
	IsFullSell bool              `json:"is_full_sell,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type tradeJournal struct {
	wal     *gowal.Wal
	intents []*tradeIntentRecord
	index   map[string]*tradeIntentRecord
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

func (j *tradeJournal) Prepare(action tradeIntentAction, price, amount decimal.Decimal, eventTime time.Time, tradePart int, isFullSell bool) (*tradeIntentRecord, error) {
	intent := &tradeIntentRecord{
		ID:         uuid.New().String(),
		Status:     tradeIntentStatusPending,
		Action:     action,
		Amount:     amount,
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

func (j *tradeJournal) MarkDone(intent *tradeIntentRecord) error {
	if intent == nil {
		return nil
	}
	intent.Status = tradeIntentStatusDone
	intent.Error = ""
	return j.persist(intent)
}

func (j *tradeJournal) UpdateAmount(intent *tradeIntentRecord, amount decimal.Decimal) error {
	if intent == nil {
		return nil
	}
	intent.Amount = amount
	return j.persist(intent)
}

func (j *tradeJournal) Intents() []*tradeIntentRecord {
	return j.intents
}

func (j *tradeJournal) persist(intent *tradeIntentRecord) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return errors.Wrap(err, "failed to marshal trade intent")
	}
	key := fmt.Sprintf("%s%s", tradeIntentKeyPrefix, intent.ID)
	nextIndex := j.wal.CurrentIndex() + 1
	return j.wal.Write(nextIndex, key, data)
}
