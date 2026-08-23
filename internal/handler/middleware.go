package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/storage"
)

// APIKeyHeader is the header name used for API key authentication.
// #nosec G101 -- Header name string, not a hardcoded credential
const APIKeyHeader = "X-API-Key"

// ProblemDetails represents RFC 7807 error format.
type ProblemDetails struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// ClientStore interface abstracts storage operations for the middleware.
type ClientStore interface {
	GetClientLimit(ctx context.Context, apiKey string) (int, error)
}

// RateLimitConfig holds the configuration for rate limiting middleware.
type RateLimitConfig struct {
	// DefaultLimit is used when no API key is provided.
	DefaultLimit int

	// Storage is used to fetch dynamic limits per API key (with L1 caching).
	Storage ClientStore

	// Limiter is the rate limiter implementation to use.
	Limiter ratelimit.Limiter

	// Metrics captures Prometheus counters.
	Metrics *metrics.Metrics
}

// RateLimitMiddleware enforces rate limits based on API keys or IP addresses.
func RateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	rateLimiter := cfg.Limiter
	var syncedLimits sync.Map

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientID := resolveClientID(r)
			ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()

			clientLimit := resolveClientLimit(ctx, cfg, r.Header.Get(APIKeyHeader))
			syncLimitIfNeeded(ctx, rateLimiter, &syncedLimits, clientID, clientLimit, cfg.DefaultLimit)

			allowed, err := rateLimiter.Allow(ctx, clientID)
			if err != nil {
				slog.Error("rate limiter failure (failing open)", "client_id", maskAPIKey(clientID), "error", err)
				next.ServeHTTP(w, r)
				return
			}

			tokens, _ := rateLimiter.Tokens(ctx, clientID)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(clientLimit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(tokens))
			w.Header().Set("X-RateLimit-Reset", "1")

			if !allowed {
				if cfg.Metrics != nil {
					cfg.Metrics.RateLimitedTotal.WithLabelValues(r.Method, metrics.SanitizePath(r.URL.Path)).Inc()
				}
				writeProblemDetails(w, http.StatusTooManyRequests, ProblemDetails{
					Type:     "https://tools.ietf.org/html/rfc6585#section-4",
					Title:    "Too Many Requests",
					Status:   http.StatusTooManyRequests,
					Detail:   "Rate limit quota exceeded. Try again in 1 second.",
					Instance: r.URL.Path,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func resolveClientID(r *http.Request) string {
	if apiKey := r.Header.Get(APIKeyHeader); apiKey != "" {
		return apiKey
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func resolveClientLimit(ctx context.Context, cfg RateLimitConfig, apiKey string) int {
	if cfg.Storage == nil || apiKey == "" {
		return cfg.DefaultLimit
	}
	limit, err := cfg.Storage.GetClientLimit(ctx, apiKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		slog.Warn("failed to fetch client rate limit", "api_key", maskAPIKey(apiKey), "error", err)
		return cfg.DefaultLimit
	}
	if limit > 0 {
		return limit
	}
	return cfg.DefaultLimit
}

func syncLimitIfNeeded(ctx context.Context, lim ratelimit.Limiter, synced *sync.Map, clientID string, limit, defaultLimit int) {
	if limit == defaultLimit {
		return
	}
	if last, ok := synced.Load(clientID); !ok || last.(int) != limit {
		if err := lim.SetLimit(ctx, clientID, limit, float64(limit)); err == nil {
			synced.Store(clientID, limit)
		}
	}
}

// RecoveryMiddleware catches panics, logs them, and returns an RFC 7807 500 response.
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
					writeProblemDetails(w, http.StatusInternalServerError, ProblemDetails{
						Type:     "https://tools.ietf.org/html/rfc7231#section-6.6.1",
						Title:    "Internal Server Error",
						Status:   http.StatusInternalServerError,
						Detail:   "An unhandled internal server error occurred.",
						Instance: r.URL.Path,
					})
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

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sw *statusWriter) Hijack() (any, any, error) {
	type hijacker interface {
		Hijack() (any, any, error)
	}
	if h, ok := sw.ResponseWriter.(hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func writeProblemDetails(w http.ResponseWriter, status int, pd ProblemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(pd)
}

func maskAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
}

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
