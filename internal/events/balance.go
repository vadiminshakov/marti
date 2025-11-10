package events

import (
	"sync"
	"time"
)

// BalanceSnapshot is a domain event representing wallet state for a pair.
// Uses string fields to avoid float precision issues when consumed by web/UI layers.
type BalanceSnapshot struct {
	Timestamp  time.Time `json:"ts"`
	Pair       string    `json:"pair"`
	Base       string    `json:"base"`
	Quote      string    `json:"quote"`
	TotalQuote string    `json:"total_quote,omitempty"`
	Price      string    `json:"price,omitempty"`
}

// BalanceBroadcaster fans out snapshots to all subscribers via buffered channels.
// It keeps the API intentionally small so call sites can stay straightforward.
type BalanceBroadcaster struct {
	mu     sync.RWMutex
	subs   map[chan BalanceSnapshot]struct{}
	buffer int
}

// NewBalanceBroadcaster creates a broadcaster with the given per-subscriber buffer.
func NewBalanceBroadcaster(buffer int) *BalanceBroadcaster {
	if buffer < 1 {
		buffer = 64
	}
	return &BalanceBroadcaster{
		subs:   make(map[chan BalanceSnapshot]struct{}),
		buffer: buffer,
	}
}

// DefaultBalanceBroadcaster is the shared broadcaster used across the app.
var DefaultBalanceBroadcaster = NewBalanceBroadcaster(256)

// Publish sends the snapshot to all subscribers, dropping if a reader is slow.
func (b *BalanceBroadcaster) Publish(s BalanceSnapshot) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- s:
		default:
			// drop slow consumer
		}
	}
}

// Subscribe returns a channel that receives snapshots until Unsubscribe is called.
func (b *BalanceBroadcaster) Subscribe() chan BalanceSnapshot {
	ch := make(chan BalanceSnapshot, b.buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes the channel and closes it.
func (b *BalanceBroadcaster) Unsubscribe(ch chan BalanceSnapshot) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}
