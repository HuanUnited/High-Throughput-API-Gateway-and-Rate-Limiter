package storage

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cacheEntry holds the cached rate limit and expiration time.
type cacheEntry struct {
	limit     int
	expiresAt time.Time
}

// ClientStore abstracts client storage operations.
type ClientStore interface {
	GetClientLimit(ctx context.Context, apiKey string) (int, error)
}

// CachedStore wraps a ClientStore with a thread-safe L1 memory TTL cache
// to eliminate database round-trips on hot paths.
type CachedStore struct {
	store    ClientStore
	ttl      time.Duration
	mu       sync.RWMutex
	cache    map[string]cacheEntry
	stop     chan struct{}
	stopOnce sync.Once
}

// NewCachedStore initializes a new L1 cache wrapper around a ClientStore.
func NewCachedStore(store ClientStore, ttl time.Duration) *CachedStore {
	if ttl <= 0 {
		ttl = time.Minute
	}
	cs := &CachedStore{
		store: store,
		ttl:   ttl,
		cache: make(map[string]cacheEntry),
		stop:  make(chan struct{}),
	}
	go cs.cleanupLoop()
	return cs
}

// GetClientLimit attempts to fetch the limit from the L1 memory cache.
// On a cache miss, it queries the underlying store and caches the result.
func (c *CachedStore) GetClientLimit(ctx context.Context, apiKey string) (int, error) {
	if apiKey == "" {
		return 0, ErrInvalidAPIKey
	}

	c.mu.RLock()
	entry, found := c.cache[apiKey]
	c.mu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		return entry.limit, nil
	}

	limit, err := c.store.GetClientLimit(ctx, apiKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.mu.Lock()
			c.cache[apiKey] = cacheEntry{
				limit:     0,
				expiresAt: time.Now().Add(c.ttl),
			}
			c.mu.Unlock()
		}
		return 0, err
	}

	c.mu.Lock()
	c.cache[apiKey] = cacheEntry{
		limit:     limit,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return limit, nil
}

// Invalidate removes an API key entry from the cache immediately.
func (c *CachedStore) Invalidate(apiKey string) {
	c.mu.Lock()
	delete(c.cache, apiKey)
	c.mu.Unlock()
}

// Close - lifecycle control for goroutines - explains itself
func (c *CachedStore) Close() error {
	c.stopOnce.Do(func() { close(c.stop) })
	return nil
}

// cleanupLoop periodically sweeps and removes expired entries from the cache.
func (c *CachedStore) cleanupLoop() {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for key, entry := range c.cache {
				if now.After(entry.expiresAt) {
					delete(c.cache, key)
				}
			}
			c.mu.Unlock()
		}
	}
}
