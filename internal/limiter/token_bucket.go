// Package limiter provides a thread-safe token bucket rate limiter
// implemented from scratch using only the standard library.
package limiter

import (
	"sync"
	"time"
)

// TokenBucket implements a token bucket rate limiting algorithm.
//
// The bucket is initialized with a fixed capacity. Tokens are added
// continuously at a steady refill rate. Each Allow() call consumes one
// token if available; otherwise the request is rejected.
//
// All methods are safe for concurrent use.
type TokenBucket struct {
	mu sync.Mutex

	capacity   int       // maximum number of tokens the bucket can hold
	refillRate float64   // tokens added per second
	tokens     float64   // current token count (fractional for precision)
	lastRefill time.Time // time of the last refill calculation
}

// NewTokenBucket creates a token bucket with the given capacity and
// refill rate (tokens per second).
//
// capacity must be >= 1 and refillRate must be > 0, otherwise it panics.
func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
	if capacity < 1 {
		panic("limiter: capacity must be >= 1")
	}
	if refillRate <= 0 {
		panic("limiter: refillRate must be > 0")
	}

	now := time.Now()
	return &TokenBucket{
		capacity:   capacity,
		refillRate: refillRate,
		tokens:     float64(capacity), // start full
		lastRefill: now,
	}
}

// Allow attempts to consume a single token from the bucket.
// It returns true if a token was available and consumed, false otherwise.
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN attempts to consume n tokens from the bucket.
// It returns true if all n tokens were available and consumed.
func (tb *TokenBucket) AllowN(n int) bool {
	if n <= 0 {
		return true
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}

	return false
}

// Tokens returns the current number of available tokens (rounded down).
func (tb *TokenBucket) Tokens() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return int(tb.tokens)
}

// Capacity returns the maximum capacity of the bucket.
func (tb *TokenBucket) Capacity() int {
	return tb.capacity
}

// RefillRate returns the configured refill rate in tokens per second.
func (tb *TokenBucket) RefillRate() float64 {
	return tb.refillRate
}

// Reset empties the bucket back to full capacity.
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.tokens = float64(tb.capacity)
	tb.lastRefill = time.Now()
}

// refill adds tokens to the bucket based on the elapsed time since the
// last refill. The bucket is never filled beyond its capacity.
//
// MUST be called with the mutex held.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)

	if elapsed <= 0 {
		return
	}

	// Compute tokens to add based on elapsed time and refill rate.
	// Using float64 for fractional token accumulation over time.
	added := tb.refillRate * elapsed.Seconds()
	tb.tokens += added

	// Clamp to capacity.
	if tb.tokens > float64(tb.capacity) {
		tb.tokens = float64(tb.capacity)
	}

	tb.lastRefill = now
}
