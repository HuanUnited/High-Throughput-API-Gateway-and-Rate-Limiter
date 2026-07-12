// Package metrics provides Prometheus metrics collection and middleware.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Server-side metrics namespace.
const (
	namespace = "api_gateway"
	subsystem = "http"
)

// Metrics holds all Prometheus collectors for the gateway.
type Metrics struct {
	// RequestsTotal counts all HTTP requests by method, path, and status.
	RequestsTotal *prometheus.CounterVec

	// RequestDuration measures HTTP request latency by method and path.
	RequestDuration *prometheus.HistogramVec

	// RateLimitedTotal counts requests rejected by the rate limiter by API key.
	RateLimitedTotal *prometheus.CounterVec

	// ActiveRequests tracks in-flight requests.
	ActiveRequests prometheus.Gauge

	// UpstreamDuration measures upstream proxy latency.
	UpstreamDuration *prometheus.HistogramVec

	// TokenBucketTokens tracks current token bucket capacity (if exposed).
	TokenBucketTokens *prometheus.GaugeVec
}

// Config holds options for metrics initialization.
type Config struct {
	// Enabled determines if metrics collection is active.
	Enabled bool

	// Buckets customizes histogram buckets for request duration.
	// If empty, defaults are used.
	Buckets []float64

	// Namespace overrides the default metric namespace.
	Namespace string
}

// New creates and registers all gateway metrics.
func New(cfg Config) *Metrics {
	ns := cfg.Namespace
	if ns == "" {
		ns = namespace
	}

	buckets := cfg.Buckets
	if len(buckets) == 0 {
		// Default buckets: 5ms to 10s
		buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}

	m := &Metrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "requests_total",
				Help:      "Total number of HTTP requests processed",
			},
			[]string{"method", "path", "status"},
		),

		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "request_duration_seconds",
				Help:      "HTTP request latency in seconds",
				Buckets:   buckets,
			},
			[]string{"method", "path"},
		),

		RateLimitedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns,
				Subsystem: "ratelimit",
				Name:      "limited_total",
				Help:      "Total number of requests rejected by rate limiter",
			},
			[]string{"api_key"},
		),

		ActiveRequests: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "active_requests",
				Help:      "Number of HTTP requests currently being processed",
			},
		),

		UpstreamDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: ns,
				Subsystem: "proxy",
				Name:      "upstream_duration_seconds",
				Help:      "Upstream proxy request latency in seconds",
				Buckets:   buckets,
			},
			[]string{"method", "upstream"},
		),

		TokenBucketTokens: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: ns,
				Subsystem: "ratelimit",
				Name:      "bucket_tokens",
				Help:      "Current number of tokens in rate limit bucket",
			},
			[]string{"bucket_type"},
		),
	}

	return m
}

// Middleware creates HTTP middleware that tracks request metrics.
// It automatically records:
// - Request count by method, path, status
// - Request duration histogram
// - Active in-flight requests gauge
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Track start time
		start := time.Now()

		// Increment active requests
		m.ActiveRequests.Inc()

		// Create response writer wrapper to capture status code
		rw := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Process request
		next.ServeHTTP(rw, r)

		// Record metrics
		duration := time.Since(start).Seconds()

		// Normalize path for metrics (avoid high cardinality)
		path := sanitizePath(r.URL.Path)

		// Record request count with status
		m.RequestsTotal.WithLabelValues(
			r.Method,
			path,
			strconv.Itoa(rw.statusCode),
		).Inc()

		// Record request duration
		m.RequestDuration.WithLabelValues(r.Method, path).Observe(duration)

		// Decrement active requests
		m.ActiveRequests.Dec()
	})
}

// RecordRateLimited increments the rate limit rejection counter.
func (m *Metrics) RecordRateLimited(apiKey string) {
	// Mask or anonymize API key to avoid high cardinality
	maskedKey := maskKey(apiKey)
	m.RateLimitedTotal.WithLabelValues(maskedKey).Inc()
}

// RecordUpstreamDuration records upstream proxy latency.
func (m *Metrics) RecordUpstreamDuration(method, upstream string, duration time.Duration) {
	m.UpstreamDuration.WithLabelValues(method, upstream).Observe(duration.Seconds())
}

// UpdateBucketTokens updates gauge with current bucket token count.
func (m *Metrics) UpdateBucketTokens(bucketType string, tokens float64) {
	m.TokenBucketTokens.WithLabelValues(bucketType).Set(tokens)
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker.
func (rw *responseWriter) Hijack() (interface{}, interface{}, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// sanitizePath normalizes dynamic paths to prevent high cardinality.
// Examples:
//
//	/api/users/123 -> /api/users/{id}
//	/v1/orders/abc123 -> /v1/orders/{id}
func sanitizePath(path string) string {
	if path == "" {
		return "/"
	}

	// Simple heuristic: replace numeric IDs and UUIDs with {id}
	parts := splitPath(path)
	for i, part := range parts {
		if isNumeric(part) || isUUID(part) || isHexID(part) {
			parts[i] = "{id}"
		}
	}

	return joinPath(parts)
}

// splitPath splits a URL path into its segments.
func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// joinPath joins path segments back into a URL path.
func joinPath(parts []string) string {
	result := ""
	for _, p := range parts {
		result += "/" + p
	}
	if result == "" {
		return "/"
	}
	return result
}

// isNumeric checks if a string is all digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isUUID checks if a string is a UUID format.
func isUUID(s string) bool {
	// UUID format: 8-4-4-4-12 hex digits
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !isHexDigit(byte(c)) {
			return false
		}
	}
	return true
}

// isHexID checks if a string is a long hex string (likely an ID).
func isHexID(s string) bool {
	// Hex strings longer than 8 chars are likely IDs
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !isHexDigit(byte(c)) {
			return false
		}
	}
	return true
}

// isHexDigit checks if a byte is a hexadecimal digit.
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F')
}

// maskKey anonymizes API keys for metrics labels.
// Keeps only the first 8 characters to prevent data leakage.
func maskKey(key string) string {
	if key == "" {
		return "anonymous"
	}
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return "***"
}
