// Package ratelimit provides the rate-limiting engine.
// It will house the Limiter interface, policy rules, and storage adapters.
package ratelimit

// Limiter enforces rate limits for a given key (client identity).
// Skeleton: concrete implementation added in later commits.
type Limiter interface {
	// Allow reports whether a request for the given key is permitted
	// under the current rate limit policy.
	Allow(key string) (allowed bool, remaining int, retryAfter int)
}

// Strict compile-time interface check helpers will be added as
// implementations are introduced in later commits.
