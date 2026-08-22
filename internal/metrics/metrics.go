// internal/metrics/metrics.go
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "api_gateway"
	subsystem = "http"
)

type Metrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	ActiveRequests   prometheus.Gauge
	RateLimitedTotal *prometheus.CounterVec
}

type Config struct {
	Enabled   bool
	Buckets   []float64
	Namespace string
}

func New(cfg Config) *Metrics {
	ns := cfg.Namespace
	if ns == "" {
		ns = namespace
	}

	buckets := cfg.Buckets
	if len(buckets) == 0 {
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

		ActiveRequests: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "active_requests",
				Help:      "Number of HTTP requests currently being processed",
			},
		),

		RateLimitedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns, Subsystem: subsystem,
				Name: "rate_limited_total",
				Help: "Total number of requests rejected due to rate limiting",
			},
			[]string{"method", "path"},
		),
	}

	return m
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.ActiveRequests.Inc()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start).Seconds()
		path := SanitizePath(r.URL.Path)
		m.RequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rw.statusCode)).Inc()
		m.RequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		m.ActiveRequests.Dec()
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (interface{}, interface{}, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func SanitizePath(path string) string {
	if path == "" {
		return "/"
	}
	parts := splitPath(path)
	for i, part := range parts {
		if isNumeric(part) || isUUID(part) || isHexID(part) {
			parts[i] = "{id}"
		}
	}
	return joinPath(parts)
}

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

func isUUID(s string) bool {
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

func isHexID(s string) bool {
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

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F')
}
