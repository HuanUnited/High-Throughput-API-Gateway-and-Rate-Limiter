package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestRedisConfig(t *testing.T) RedisConfig {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		t.Skip("REDIS_HOST not set, skipping Redis integration test")
	}

	return RedisConfig{
		Host:         host,
		Port:         6379,
		Password:     os.Getenv("REDIS_PASSWORD"),
		DB:           3,
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		KeyPrefix:    "test_ratelimit:",
	}
}

func TestRedisLimiter_Integration(t *testing.T) {
	redisCfg := getTestRedisConfig(t)
	limitCfg := Config{
		TokensPerSecond: 10,
		BurstSize:       5,
	}

	limiter, err := NewRedisLimiter(redisCfg, limitCfg)
	require.NoError(t, err)
	defer func() { _ = limiter.Close() }()

	ctx := context.Background()

	t.Run("consume burst tokens", func(t *testing.T) {
		clientID := "redis-burst-client"
		_ = limiter.Reset(ctx, clientID)

		for range 5 {
			allowed, allowErr := limiter.Allow(ctx, clientID)
			require.NoError(t, allowErr)
			assert.True(t, allowed)
		}

		allowed, allowErr := limiter.Allow(ctx, clientID)
		require.NoError(t, allowErr)
		assert.False(t, allowed)
	})

	t.Run("token count accuracy", func(t *testing.T) {
		clientID := "redis-tokens-client"
		_ = limiter.Reset(ctx, clientID)

		tokens, tokenErr := limiter.Tokens(ctx, clientID)
		require.NoError(t, tokenErr)
		assert.Equal(t, 5, tokens)

		_, _ = limiter.Allow(ctx, clientID)
		tokens, tokenErr = limiter.Tokens(ctx, clientID)
		require.NoError(t, tokenErr)
		assert.Equal(t, 4, tokens)
	})

	t.Run("dynamic limit override via SetLimit", func(t *testing.T) {
		clientID := "redis-override-client"
		_ = limiter.Reset(ctx, clientID)

		err := limiter.SetLimit(ctx, clientID, 20, 20.0)
		require.NoError(t, err)

		allowed, allowErr := limiter.Allow(ctx, clientID)
		require.NoError(t, allowErr)
		assert.True(t, allowed)

		tokens, tokenErr := limiter.Tokens(ctx, clientID)
		require.NoError(t, tokenErr)
		assert.Equal(t, 19, tokens)
	})

	t.Run("allow batch consumption with AllowN", func(t *testing.T) {
		clientID := "redis-batch-client"
		_ = limiter.Reset(ctx, clientID)

		allowed, allowErr := limiter.AllowN(ctx, clientID, 3)
		require.NoError(t, allowErr)
		assert.True(t, allowed)

		allowed, allowErr = limiter.AllowN(ctx, clientID, 3)
		require.NoError(t, allowErr)
		assert.False(t, allowed)

		allowedZero, allowErr := limiter.AllowN(ctx, clientID, 0)
		require.NoError(t, allowErr)
		assert.True(t, allowedZero)
	})
}

func TestNewRedisLimiter_Validation(t *testing.T) {
	redisCfg := RedisConfig{Host: "localhost", Port: 6379}

	_, err := NewRedisLimiter(redisCfg, Config{BurstSize: 0, TokensPerSecond: 10})
	require.Error(t, err)

	_, err = NewRedisLimiter(redisCfg, Config{BurstSize: 10, TokensPerSecond: 0})
	require.Error(t, err)
}
