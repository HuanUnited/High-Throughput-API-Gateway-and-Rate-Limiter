// internal/ratelimit/ratelimit.go
package ratelimit

import (
	"context"
	"time"
)

// Limiter defines the interface for rate limiting backends.
// Both in-memory and Redis implementations must satisfy this interface.
type Limiter interface {
	// Allow checks if a request is permitted for the given client.
	// Returns true if the request is allowed, false if rate limited.
	Allow(ctx context.Context, clientID string) (bool, error)

	// Tokens returns the current number of available tokens for a client.
	Tokens(ctx context.Context, clientID string) (int, error)

	// Reset clears the rate limit state for a client.
	Reset(ctx context.Context, clientID string) error

	// Sets the rate limit for a client
	SetLimit(ctx context.Context, clientID string, burst int, rps float64) error

	// Close releases any resources held by the limiter.
	Close() error
}

// Config holds common configuration for rate limiters.
type Config struct {
	// TokensPerSecond is the refill rate (tokens added per second)
	TokensPerSecond float64

	// BurstSize is the maximum number of tokens the bucket can hold
	BurstSize int

	// CleanupInterval is how often expired entries are cleaned up (in-memory only)
	CleanupInterval time.Duration
}
