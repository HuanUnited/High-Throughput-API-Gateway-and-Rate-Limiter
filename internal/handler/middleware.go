package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/limiter"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/storage"
)

// APIKeyHeader is the header name used for API key authentication.
const APIKeyHeader = "X-API-Key"

// RateLimiter interface abstracts the rate limiter for middleware.
type RateLimiter interface {
	Allow() bool
}

// ClientStore interface abstracts storage operations for the middleware.
type ClientStore interface {
	GetClientLimit(ctx context.Context, apiKey string) (int, error)
}

// RateLimitConfig holds the configuration for rate limiting middleware.
type RateLimitConfig struct {
	// DefaultLimit is used when no API key is provided.
	DefaultLimit int

	// Storage is used to fetch dynamic limits per API key.
	// If nil, the default limit is always used.
	Storage ClientStore
}

// RateLimitMiddleware enforces rate limits based on API keys.
// It supports both static and dynamic (database-backed) limits.
func RateLimitMiddleware(defaultBucket *limiter.TokenBucket, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract API key from headers
			apiKey := r.Header.Get(APIKeyHeader)

			var bucket RateLimiter = defaultBucket

			// If we have storage and an API key, fetch dynamic limit
			if cfg.Storage != nil && apiKey != "" {
				ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
				defer cancel()

				limit, err := cfg.Storage.GetClientLimit(ctx, apiKey)
				if err != nil {
					// Log the error but continue with default limit
					slog.Warn("failed to fetch client rate limit",
						"api_key", maskAPIKey(apiKey),
						"error", err,
					)
					bucket = defaultBucket
				} else {
					// Create a bucket with the client's specific limit
					bucket = newDynamicBucket(limit)
				}
			}

			// Enforce rate limiting
			if !bucket.Allow() {
				writeRateLimitError(w)
				return
			}

			// Pass to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// newDynamicBucket creates a token bucket for a dynamic client limit.
func newDynamicBucket(limit int) *limiter.TokenBucket {
	// Use the limit as both capacity and refill rate (tokens per second)
	return limiter.NewTokenBucket(limit, float64(limit))
}

// RecoveryMiddleware catches panics, logs them, and returns a 500 response.
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						"error", err,
						"path", r.URL.Path,
						"method", r.Method,
						"stack", string(debug.Stack()),
					)
					writeJSONError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// LoggingMiddleware logs each request with status code, duration, and path.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the ResponseWriter to capture the status code.
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			// Add request ID if not present
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = generateRequestID()
				w.Header().Set("X-Request-ID", requestID)
			}

			next.ServeHTTP(ww, r)

			logger.Info("request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"request_id", requestID,
				"api_key", maskAPIKey(r.Header.Get(APIKeyHeader)),
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the response status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code before delegating.
func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}

// Write captures the byte count (can be extended for response size tracking).
func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher to support streaming responses.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker for websocket support if needed.
func (sw *statusWriter) Hijack() (interface{}, interface{}, error) {
	type hijacker interface {
		Hijack() (interface{}, interface{}, error)
	}
	if h, ok := sw.ResponseWriter.(hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// writeRateLimitError writes a standardized 429 rate limit error response.
func writeRateLimitError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "rate limit exceeded",
	})
}

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

// maskAPIKey masks an API key for logging purposes.
// Shows only the first 4 and last 4 characters.
func maskAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
}

// generateRequestID creates a simple request ID.
// In production, use a proper UUID library.
func generateRequestID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = chars[time.Now().UnixNano()%int64(len(chars))]
	}
	return string(b)
}

// `storage` package is referenced to satisfy the import.
// The actual usage is through the ClientStore interface.
var _ storage.Postgres
