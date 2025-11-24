package retrier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetrier_Do(t *testing.T) {
	t.Run("success on first attempt", func(t *testing.T) {
		r := New()
		attempts := 0
		err := r.Do(context.Background(), func(ctx context.Context) error {
			attempts++
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 1, attempts)
	})

	t.Run("success after retries", func(t *testing.T) {
		r := New(WithMaxRetries(3), WithInitialInterval(1*time.Millisecond))
		attempts := 0
		err := r.Do(context.Background(), func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("fail")
			}
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 3, attempts)
	})

	t.Run("fail after max retries", func(t *testing.T) {
		r := New(WithMaxRetries(2), WithInitialInterval(1*time.Millisecond))
		attempts := 0
		err := r.Do(context.Background(), func(ctx context.Context) error {
			attempts++
			return errors.New("fail")
		})
		assert.Error(t, err)
		assert.Equal(t, 3, attempts) // 1 initial + 2 retries
	})

	t.Run("context cancellation", func(t *testing.T) {
		r := New(WithMaxRetries(5), WithInitialInterval(100*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())

		attempts := 0
		err := r.Do(ctx, func(ctx context.Context) error {
			attempts++
			if attempts == 2 {
				cancel()
			}
			return errors.New("fail")
		})
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 2, attempts)
	})
}

func TestRetrier_DoWithData(t *testing.T) {
	t.Run("success returns data", func(t *testing.T) {
		r := New()
		val, err := DoWithData(r, context.Background(), func(ctx context.Context) (string, error) {
			return "success", nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "success", val)
	})

	t.Run("fail returns error", func(t *testing.T) {
		r := New(WithMaxRetries(1), WithInitialInterval(1*time.Millisecond))
		val, err := DoWithData(r, context.Background(), func(ctx context.Context) (string, error) {
			return "", errors.New("fail")
		})
		assert.Error(t, err)
		assert.Empty(t, val)
	})
}
