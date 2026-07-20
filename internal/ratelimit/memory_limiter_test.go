package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLimiter(t *testing.T) {
	ctx := context.Background()

	t.Run("allow and token consumption", func(t *testing.T) {
		lim := NewMemoryLimiter(Config{
			TokensPerSecond: 10,
			BurstSize:       5,
			CleanupInterval: time.Minute,
		})
		defer func() { _ = lim.Close() }()

		for range 5 {
			allowed, err := lim.Allow(ctx, "client-1")
			require.NoError(t, err)
			assert.True(t, allowed)
		}

		allowed, err := lim.Allow(ctx, "client-1")
		require.NoError(t, err)
		assert.False(t, allowed)
	})

	t.Run("set dynamic limit overrides", func(t *testing.T) {
		lim := NewMemoryLimiter(Config{
			TokensPerSecond: 1,
			BurstSize:       1,
			CleanupInterval: time.Minute,
		})
		defer func() { _ = lim.Close() }()

		allowed, err := lim.Allow(ctx, "dynamic-client")
		require.NoError(t, err)
		assert.True(t, allowed)

		allowed, err = lim.Allow(ctx, "dynamic-client")
		require.NoError(t, err)
		assert.False(t, allowed)

		err = lim.SetLimit(ctx, "dynamic-client", 10, 10.0)
		require.NoError(t, err)

		allowed, err = lim.Allow(ctx, "dynamic-client")
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("reset client state", func(t *testing.T) {
		lim := NewMemoryLimiter(Config{
			TokensPerSecond: 1,
			BurstSize:       2,
			CleanupInterval: time.Minute,
		})
		defer func() { _ = lim.Close() }()

		_, _ = lim.AllowN(ctx, "reset-client", 2)
		tokens, err := lim.Tokens(ctx, "reset-client")
		require.NoError(t, err)
		assert.Equal(t, 0, tokens)

		err = lim.Reset(ctx, "reset-client")
		require.NoError(t, err)

		tokens, err = lim.Tokens(ctx, "reset-client")
		require.NoError(t, err)
		assert.Equal(t, 2, tokens)
	})

	t.Run("cleanup removes idle buckets", func(t *testing.T) {
		lim := NewMemoryLimiter(Config{
			TokensPerSecond: 1,
			BurstSize:       5,
			CleanupInterval: 20 * time.Millisecond,
		})
		defer func() { _ = lim.Close() }()

		_, _ = lim.Allow(ctx, "idle-client")

		lim.mu.Lock()
		_, exists := lim.buckets["idle-client"]
		lim.mu.Unlock()
		assert.True(t, exists)

		time.Sleep(60 * time.Millisecond)

		lim.mu.Lock()
		_, exists = lim.buckets["idle-client"]
		lim.mu.Unlock()
		assert.False(t, exists)
	})
}
