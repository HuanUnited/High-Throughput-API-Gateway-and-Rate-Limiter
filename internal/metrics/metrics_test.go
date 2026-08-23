package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{"/api/v1/users", "/api/v1/users"},
		{"/api/v1/users/12345", "/api/v1/users/{id}"},
		{"/api/v1/orders/550e8400-e29b-41d4-a716-446655440000", "/api/v1/orders/{id}"},
		{"/api/v1/items/abcdef0123456789", "/api/v1/items/{id}"},
		{"/api/v1/users/123/orders/456", "/api/v1/users/{id}/orders/{id}"},
		{"/invalid-uuid/550e8400-e29b-41d4-a716-44665544000Z", "/invalid-uuid/550e8400-e29b-41d4-a716-44665544000Z"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, SanitizePath(tt.input))
		})
	}
}

func TestMetrics_MiddlewareAndCollectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	cfg := Config{
		Enabled:   true,
		Namespace: "test_gateway",
	}

	m := New(cfg)
	reg.MustRegister(m.RequestsTotal, m.RequestDuration, m.ActiveRequests, m.RateLimitedTotal)

	handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("accepted"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/123", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	// Exercise RateLimitedTotal collector
	m.RateLimitedTotal.WithLabelValues("POST", "/api/v1/orders/{id}").Inc()

	metricFamilies, err := reg.Gather()
	require.NoError(t, err)

	var foundRequests, foundRateLimited bool
	for _, mf := range metricFamilies {
		if mf.GetName() == "test_gateway_http_requests_total" {
			for _, metric := range mf.GetMetric() {
				if hasLabel(metric, "path", "/api/v1/orders/{id}") && hasLabel(metric, "status", "202") {
					assert.InEpsilon(t, 1.0, metric.GetCounter().GetValue(), 0.001)
					foundRequests = true
				}
			}
		}
		if mf.GetName() == "test_gateway_http_rate_limited_total" {
			for _, metric := range mf.GetMetric() {
				if hasLabel(metric, "path", "/api/v1/orders/{id}") {
					assert.InEpsilon(t, 1.0, metric.GetCounter().GetValue(), 0.001)
					foundRateLimited = true
				}
			}
		}
	}

	assert.True(t, foundRequests)
	assert.True(t, foundRateLimited)
}

func TestResponseWriter_Delegation(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNoContent)
	assert.Equal(t, http.StatusNoContent, rw.statusCode)

	rw.Flush()
	_, _, err := rw.Hijack()
	assert.ErrorIs(t, err, http.ErrNotSupported)
}

func hasLabel(m *dto.Metric, name, val string) bool {
	for _, pair := range m.GetLabel() {
		if pair.GetName() == name && pair.GetValue() == val {
			return true
		}
	}
	return false
}
