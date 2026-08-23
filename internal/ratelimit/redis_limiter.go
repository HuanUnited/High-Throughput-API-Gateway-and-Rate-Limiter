package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Atomic Lua script for token bucket operations.
// This ensures thread-safety across multiple gateway instances.
// #nosec G101
const tokenBucketScript = `
local key = KEYS[1]
local defaultCapacity = tonumber(ARGV[1])
local defaultRefillRate = tonumber(ARGV[2])
local requested = tonumber(ARGV[3])
local now = tonumber(ARGV[4])

-- Check for client-specific dynamic overrides
local capacity = defaultCapacity
local refillRate = defaultRefillRate
local cfg = redis.call('HMGET', key .. ':cfg', 'burst', 'rps')
if cfg[1] and cfg[2] then
    capacity = tonumber(cfg[1])
    refillRate = tonumber(cfg[2])
end

-- Get current token state
local data = redis.call('HMGET', key, 'tokens', 'lastRefill')
local tokens = tonumber(data[1])
local lastRefill = tonumber(data[2])

if tokens == nil then
    tokens = capacity
    lastRefill = now
end

-- Calculate refill
local elapsed = now - lastRefill
if elapsed < 0 then
    elapsed = 0
end

local newTokens = tokens + (refillRate * elapsed / 1000)
if newTokens > capacity then
    newTokens = capacity
end

-- Check if we can allow the request
if newTokens >= requested then
    newTokens = newTokens - requested
    redis.call('HMSET', key, 'tokens', newTokens, 'lastRefill', now)
    local ttl = math.ceil((capacity / refillRate) * 2) + 1
    redis.call('EXPIRE', key, ttl)
    return 1
end

-- Update state even if not allowed
redis.call('HMSET', key, 'tokens', newTokens, 'lastRefill', now)
return 0
`

// Script to get current tokens without consuming
// #nosec G101
const getTokensScript = `
local key = KEYS[1]
local defaultCapacity = tonumber(ARGV[1])
local defaultRefillRate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- Check for client-specific dynamic overrides
local capacity = defaultCapacity
local refillRate = defaultRefillRate
local cfg = redis.call('HMGET', key .. ':cfg', 'burst', 'rps')
if cfg[1] and cfg[2] then
    capacity = tonumber(cfg[1])
    refillRate = tonumber(cfg[2])
end

local data = redis.call('HMGET', key, 'tokens', 'lastRefill')
local tokens = tonumber(data[1])
local lastRefill = tonumber(data[2])

if tokens == nil then
    return math.floor(capacity)
end

local elapsed = now - lastRefill
if elapsed < 0 then
    elapsed = 0
end

local newTokens = tokens + (refillRate * elapsed / 1000)
if newTokens > capacity then
    newTokens = capacity
end

return math.floor(newTokens)
`

// RedisLimiter implements a distributed rate limiter using Redis.
// It uses Lua scripts for atomic token bucket operations, ensuring
// consistency even with multiple gateway instances.
type RedisLimiter struct {
	client    *redis.Client
	script    *redis.Script
	getScript *redis.Script
	config    Config
	keyPrefix string
}

// RedisConfig holds Redis connection configuration.
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int

	// Pool settings
	PoolSize     int
	MinIdleConns int

	// Timeouts
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// KeyPrefix to avoid collisions in shared Redis instances
	KeyPrefix string
}

// NewRedisLimiter creates a new Redis-backed rate limiter.
func NewRedisLimiter(redisCfg RedisConfig, limitConfig Config) (*RedisLimiter, error) {
	if limitConfig.BurstSize <= 0 {
		return nil, errors.New("burst size must be positive")
	}
	if limitConfig.TokensPerSecond <= 0 {
		return nil, errors.New("tokens per second must be positive")
	}

	// Build Redis client
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", redisCfg.Host, redisCfg.Port),
		Password:     redisCfg.Password,
		DB:           redisCfg.DB,
		PoolSize:     redisCfg.PoolSize,
		MinIdleConns: redisCfg.MinIdleConns,
		DialTimeout:  redisCfg.DialTimeout,
		ReadTimeout:  redisCfg.ReadTimeout,
		WriteTimeout: redisCfg.WriteTimeout,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	// If no key prefix, use a default
	if redisCfg.KeyPrefix == "" {
		redisCfg.KeyPrefix = "ratelimit:"
	}

	// Load scripts
	script := redis.NewScript(tokenBucketScript)
	getScript := redis.NewScript(getTokensScript)

	return &RedisLimiter{
		client:    client,
		script:    script,
		getScript: getScript,
		config:    limitConfig,
		keyPrefix: redisCfg.KeyPrefix,
	}, nil
}

// key generates the Redis key for a client ID.
func (r *RedisLimiter) key(clientID string) string {
	return fmt.Sprintf("%s%s", r.keyPrefix, clientID)
}

// Allow checks if a request is permitted and consumes a token if available.
func (r *RedisLimiter) Allow(ctx context.Context, clientID string) (bool, error) {
	return r.AllowN(ctx, clientID, 1)
}

// AllowN checks if n requests are permitted and consumes tokens if available.
func (r *RedisLimiter) AllowN(ctx context.Context, clientID string, n int) (bool, error) {
	if n <= 0 {
		return true, nil
	}

	// Execute the Lua script atomically
	result, err := r.script.Run(ctx, r.client,
		[]string{r.key(clientID)},
		r.config.BurstSize,
		r.config.TokensPerSecond,
		n,
		time.Now().UnixMilli(),
	).Result()

	if err != nil {
		return false, fmt.Errorf("execute rate limit check: %w", err)
	}

	// Script returns 1 if allowed, 0 if not
	val, ok := result.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected result type %T", result)
	}

	return val == 1, nil
}

// Tokens returns the current number of available tokens for a client.
func (r *RedisLimiter) Tokens(ctx context.Context, clientID string) (int, error) {
	result, err := r.getScript.Run(ctx, r.client,
		[]string{r.key(clientID)},
		r.config.BurstSize,
		r.config.TokensPerSecond,
		time.Now().UnixMilli(),
	).Result()

	if err != nil {
		return 0, fmt.Errorf("get token count: %w", err)
	}
	val, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected result type %T", result)
	}

	return int(val), nil
}

// SetLimit updates token bucket rate limits in Redis.
func (r *RedisLimiter) SetLimit(ctx context.Context, clientID string, burst int, rps float64) error {
	return r.client.HSet(ctx, r.key(clientID)+":cfg", "burst", burst, "rps", rps).Err()
}

// Reset clears the rate limit state for a client.
func (r *RedisLimiter) Reset(ctx context.Context, clientID string) error {
	return r.client.Del(ctx, r.key(clientID)).Err()
}

// Close releases the Redis connection pool.
func (r *RedisLimiter) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}
