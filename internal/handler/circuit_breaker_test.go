package handler

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	t.Run("starts in closed state and allows requests", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 50*time.Millisecond)
		assert.Equal(t, StateClosed, cb.state)
		assert.True(t, cb.Allow())
	})

	t.Run("trips to open state when failure threshold is reached", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 50*time.Millisecond)

		cb.RecordResult(false)
		cb.RecordResult(false)
		assert.True(t, cb.Allow())
		assert.Equal(t, StateClosed, cb.state)

		cb.RecordResult(false)
		assert.Equal(t, StateOpen, cb.state)
		assert.False(t, cb.Allow())
	})

	t.Run("transitions from open to half-open after cooldown and recovers on success", func(t *testing.T) {
		cooldown := 30 * time.Millisecond
		cb := NewCircuitBreaker(2, cooldown)

		cb.RecordResult(false)
		cb.RecordResult(false)
		require.False(t, cb.Allow())

		time.Sleep(cooldown + 10*time.Millisecond)

		// First request after cooldown enters half-open
		assert.True(t, cb.Allow())
		assert.Equal(t, StateHalfOpen, cb.state)

		// Success closes the circuit
		cb.RecordResult(true)
		assert.Equal(t, StateClosed, cb.state)
		assert.Equal(t, 0, cb.failures)
		assert.True(t, cb.Allow())
	})

	t.Run("transitions from half-open back to open on failure", func(t *testing.T) {
		cooldown := 30 * time.Millisecond
		cb := NewCircuitBreaker(2, cooldown)

		cb.RecordResult(false)
		cb.RecordResult(false)
		require.False(t, cb.Allow())

		time.Sleep(cooldown + 10*time.Millisecond)

		assert.True(t, cb.Allow())
		assert.Equal(t, StateHalfOpen, cb.state)

		// Failure during half-open immediately re-opens circuit
		cb.RecordResult(false)
		assert.Equal(t, StateOpen, cb.state)
		assert.False(t, cb.Allow())
	})

	t.Run("success in closed state resets failure counter", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 50*time.Millisecond)

		cb.RecordResult(false)
		cb.RecordResult(false)
		assert.Equal(t, 2, cb.failures)

		cb.RecordResult(true)
		assert.Equal(t, 0, cb.failures)
		assert.Equal(t, StateClosed, cb.state)
	})
}

func TestCircuitBreaker_Concurrency(_ *testing.T) {
	cb := NewCircuitBreaker(100, 50*time.Millisecond)
	var wg sync.WaitGroup
	workers := 20
	iterations := 200

	wg.Add(workers * 2)

	// Concurrent Allow callers
	for range workers {
		go func() {
			defer wg.Done()
			for range iterations {
				_ = cb.Allow()
			}
		}()
	}

	// Concurrent RecordResult callers
	for i := range workers {
		go func(id int) {
			defer wg.Done()
			for range iterations {
				cb.RecordResult(id%2 == 0)
			}
		}(i)
	}

	wg.Wait()
}
