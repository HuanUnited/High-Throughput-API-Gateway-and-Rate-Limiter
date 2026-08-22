package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
)

// APIKeyHeader is the header name used for API key authentication.
const APIKeyHeader = "X-API-Key"

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

	// Limiter is the rate limiter implementation to use.
	Limiter ratelimit.Limiter

	Metrics *metrics.Metrics
}

// RateLimitMiddleware enforces rate limits based on API keys.
// It supports both static and dynamic (database-backed) limits.
func RateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	rateLimiter := cfg.Limiter
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract API key from headers
			apiKey := r.Header.Get(APIKeyHeader)

			// Determine the client ID (API key or IP address)
			clientID := apiKey
			if clientID == "" {
				clientID = r.RemoteAddr
			}

			// Create context with timeout for rate limit check
			ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()

			// Check if rate limiter has the client
			// If storage is configured, check for dynamic limits
			if cfg.Storage != nil && apiKey != "" {
				limit, err := cfg.Storage.GetClientLimit(ctx, apiKey)
				if err != nil {
					slog.Warn("failed to fetch client rate limit", "api_key", maskAPIKey(apiKey), "error", err)
				} else if limit > 0 {
					if err := rateLimiter.SetLimit(ctx, clientID, limit, float64(limit)); err != nil {
						slog.Warn("failed to apply custom rate limit", "client_id", maskAPIKey(clientID), "error", err)
					}
				}
			}

			// Enforce rate limiting using the interface-based limiter
			allowed, err := rateLimiter.Allow(ctx, clientID)
			if err != nil {
				// Log the error and allow the request (fail-open)
				slog.Error("rate limiter error",
					"client_id", maskAPIKey(clientID),
					"error", err,
				)
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				if cfg.Metrics != nil {
					cfg.Metrics.RateLimitedTotal.WithLabelValues(r.Method, metrics.SanitizePath(r.URL.Path)).Inc()
				}
				writeRateLimitError(w)
				return
			}

			// Pass to next handler
			next.ServeHTTP(w, r)
		})
	}
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

			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = generateRequestID()
				ww.Header().Set("X-Request-ID", requestID)
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

// Simplifies middleware chain
func BuildMiddlewareChain(metrics *metrics.Metrics, logger *slog.Logger, rlCfg RateLimitConfig, proxy http.Handler) http.Handler {
	return metrics.Middleware(
		RecoveryMiddleware(logger)(
			LoggingMiddleware(logger)(
				RateLimitMiddleware(rlCfg)(proxy),
			),
		),
	)
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
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
