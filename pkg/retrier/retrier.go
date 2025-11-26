package retrier

import (
	"context"
	"math/rand"
	"time"
)

const (
	defaultInitialInterval = 1 * time.Second
	defaultMaxInterval     = 30 * time.Second
	defaultMultiplier      = 2.0
	defaultMaxRetries      = 5
	defaultJitter          = 0.1
)

// Retrier implements exponential backoff with jitter.
type Retrier struct {
	initialInterval time.Duration
	maxInterval     time.Duration
	multiplier      float64
	maxRetries      int
	jitter          float64
}

// Option defines a function to configure the Retrier.
type Option func(*Retrier)

// WithInitialInterval sets the initial retry interval.
func WithInitialInterval(d time.Duration) Option {
	return func(r *Retrier) {
		r.initialInterval = d
	}
}

// WithMaxInterval sets the maximum retry interval.
func WithMaxInterval(d time.Duration) Option {
	return func(r *Retrier) {
		r.maxInterval = d
	}
}

// WithMultiplier sets the backoff multiplier.
func WithMultiplier(m float64) Option {
	return func(r *Retrier) {
		r.multiplier = m
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(n int) Option {
	return func(r *Retrier) {
		r.maxRetries = n
	}
}

// WithJitter sets the jitter factor (0.0 to 1.0).
func WithJitter(j float64) Option {
	return func(r *Retrier) {
		r.jitter = j
	}
}

// New creates a new Retrier with default values and optional overrides.
func New(opts ...Option) *Retrier {
	r := &Retrier{
		initialInterval: defaultInitialInterval,
		maxInterval:     defaultMaxInterval,
		multiplier:      defaultMultiplier,
		maxRetries:      defaultMaxRetries,
		jitter:          defaultJitter,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Do executes the given function with retries.
func (r *Retrier) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	var err error
	interval := r.initialInterval

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			jitter := (rand.Float64()*2 - 1) * r.jitter * float64(interval)
			sleepDuration := time.Duration(float64(interval) + jitter)

			if sleepDuration < 0 {
				sleepDuration = 0
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration):
			}

			interval = time.Duration(float64(interval) * r.multiplier)
			if interval > r.maxInterval {
				interval = r.maxInterval
			}
		}

		err = fn(ctx)
		if err == nil {
			return nil
		}
	}

	return err
}

// DoWithData executes the given function with retries and returns a value.
func DoWithData[T any](r *Retrier, ctx context.Context, fn func(ctx context.Context) (T, error)) (T, error) {
	var result T
	err := r.Do(ctx, func(ctx context.Context) error {
		var e error
		result, e = fn(ctx)
		return e
	})
	return result, err
}
