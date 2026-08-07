// Package metrics provides Prometheus metric collectors and helpers.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "api_gateway"
	subsystem = "http"
)

// Metrics encapsulates Prometheus performance collectors.
type Metrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	ActiveRequests   prometheus.Gauge
	RateLimitedTotal *prometheus.CounterVec
}

// Config specifies metrics collection configuration.
type Config struct {
	Enabled    bool
	Buckets    []float64
	Namespace  string
	Registerer prometheus.Registerer
}

// New initializes and registers Prometheus metric collectors.
func New(cfg Config) *Metrics {
	ns := cfg.Namespace
	if ns == "" {
		ns = namespace
	}

	reg := cfg.Registerer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	factory := promauto.With(reg)

	buckets := cfg.Buckets
	if len(buckets) == 0 {
		buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}

	m := &Metrics{
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "requests_total",
				Help:      "Total number of HTTP requests processed",
			},
			[]string{"method", "path", "status"},
		),

		RequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "request_duration_seconds",
				Help:      "HTTP request latency in seconds",
				Buckets:   buckets,
			},
			[]string{"method", "path"},
		),

		ActiveRequests: factory.NewGauge(
			prometheus.GaugeOpts{
				Namespace: ns,
				Subsystem: subsystem,
				Name:      "active_requests",
				Help:      "Number of HTTP requests currently being processed",
			},
		),

		RateLimitedTotal: factory.NewCounterVec(
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

// Middleware records response latency and HTTP status metrics.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}
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

func (rw *responseWriter) Hijack() (any, any, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// SanitizePath strips variable segments from URL paths to limit metric cardinality.
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
	sb := strings.Builder{}
	for _, p := range parts {
		sb.WriteString("/")
		sb.WriteString(p)
	}
	if sb.String() == "" {
		return "/"
	}
	return sb.String()
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
		} else if !isHexDigit(c) {
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
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
