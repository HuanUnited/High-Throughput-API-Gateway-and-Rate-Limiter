// Package ratelimit implements rate limiting algorithms.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/limiter"
)

// MemoryLimiter implements the Limiter interface using in-memory token buckets.
// This is suitable for single-instance deployments or testing.
type MemoryLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucketEntry
	config   Config
	stop     chan struct{}
	stopOnce sync.Once
}

// bucketEntry wraps a token bucket with last-used tracking for cleanup.
type bucketEntry struct {
	bucket   *limiter.TokenBucket
	lastUsed time.Time
}

// NewMemoryLimiter creates a new in-memory rate limiter.
func NewMemoryLimiter(config Config) *MemoryLimiter {
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 1 * time.Second
	}

	lim := &MemoryLimiter{
		buckets: make(map[string]*bucketEntry),
		config:  config,
		stop:    make(chan struct{}),
	}
	go lim.cleanupLoop()
	return lim
}

// Allow checks if a request is permitted and consumes a token if available.
func (m *MemoryLimiter) Allow(ctx context.Context, clientID string) (bool, error) {
	return m.AllowN(ctx, clientID, 1)
}

// AllowN checks if n requests are permitted and consumes tokens if available.
func (m *MemoryLimiter) AllowN(_ context.Context, clientID string, n int) (bool, error) {
	bucket := m.getOrCreateBucket(clientID)
	return bucket.AllowN(n), nil
}

// Tokens returns the current number of available tokens for a client.
func (m *MemoryLimiter) Tokens(_ context.Context, clientID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.buckets[clientID]
	if !exists {
		return int(m.config.BurstSize), nil
	}

	return entry.bucket.Tokens(), nil
}

// Reset clears the rate limit state for a client.
func (m *MemoryLimiter) Reset(_ context.Context, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.buckets, clientID)
	return nil
}

// SetLimit updates token bucket limits for a client.
func (m *MemoryLimiter) SetLimit(_ context.Context, clientID string, burst int, rps float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buckets[clientID] = &bucketEntry{
		bucket:   limiter.NewTokenBucket(burst, rps),
		lastUsed: time.Now(),
	}
	return nil
}

// Close stops the cleanup goroutine.
func (m *MemoryLimiter) Close() error {
	var err error
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	return err
}

// getOrCreateBucket returns the token bucket for a client, creating it if needed.
func (m *MemoryLimiter) getOrCreateBucket(clientID string) *limiter.TokenBucket {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.buckets[clientID]
	if !exists {
		entry = &bucketEntry{
			bucket: limiter.NewTokenBucket(
				m.config.BurstSize,
				m.config.TokensPerSecond,
			),
			lastUsed: time.Now(),
		}
		m.buckets[clientID] = entry
	} else {
		entry.lastUsed = time.Now()
	}

	return entry.bucket
}

// cleanupLoop periodically removes idle buckets to prevent memory leaks.
func (m *MemoryLimiter) cleanupLoop() {
	if m.config.CleanupInterval <= 0 {
		return
	}

	ticker := time.NewTicker(m.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}

// cleanup removes buckets that haven't been used recently.
func (m *MemoryLimiter) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get the current time
	now := time.Now()

	// Calculate idle threshold (e.g., 2x cleanup interval)
	idleThreshold := now.Add(-2 * m.config.CleanupInterval)

	for clientID, entry := range m.buckets {
		if entry.lastUsed.Before(idleThreshold) {
			delete(m.buckets, clientID)
		}
	}
}
