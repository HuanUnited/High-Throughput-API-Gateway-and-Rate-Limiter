package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStorage struct {
	limit int
	err   error
}

func (m *mockStorage) GetClientLimit(_ context.Context, _ string) (int, error) {
	return m.limit, m.err
}

type mockFailingLimiter struct {
	ratelimit.Limiter
}

func (m *mockFailingLimiter) Allow(_ context.Context, _ string) (bool, error) {
	return false, errors.New("redis connection refused")
}

func (m *mockFailingLimiter) Tokens(_ context.Context, _ string) (int, error) {
	return 0, errors.New("redis connection refused")
}

func (m *mockFailingLimiter) SetLimit(_ context.Context, _ string, _ int, _ float64) error {
	return nil
}

func (m *mockFailingLimiter) Reset(_ context.Context, _ string) error {
	return nil
}

func (m *mockFailingLimiter) Close() error {
	return nil
}

func TestRateLimitMiddleware_DynamicTierAndHeaders(t *testing.T) {
	lim := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: 10,
		BurstSize:       10,
		CleanupInterval: time.Minute,
	})
	defer func() { _ = lim.Close() }()

	store := &mockStorage{limit: 50}
	m := metrics.New(metrics.Config{Enabled: true, Namespace: "test_mw"})

	cfg := RateLimitConfig{
		DefaultLimit: 10,
		Storage:      store,
		Limiter:      lim,
		Metrics:      m,
	}

	mw := RateLimitMiddleware(cfg)
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
	req.Header.Set(APIKeyHeader, "tier-test-key")
	rec := httptest.NewRecorder()

	mw(nextHandler).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "50", rec.Header().Get("X-RateLimit-Limit"))
	assert.NotEmpty(t, rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "1", rec.Header().Get("X-RateLimit-Reset"))
}

func TestRateLimitMiddleware_FailOpenOnLimiterError(t *testing.T) {
	cfg := RateLimitConfig{
		DefaultLimit: 10,
		Storage:      nil,
		Limiter:      &mockFailingLimiter{},
	}

	mw := RateLimitMiddleware(cfg)
	executed := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		executed = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/failopen", nil)
	rec := httptest.NewRecorder()

	mw(nextHandler).ServeHTTP(rec, req)

	assert.True(t, executed)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimitMiddleware_IPFallbackWhenNoAPIKey(t *testing.T) {
	lim := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: 1,
		BurstSize:       1,
		CleanupInterval: time.Minute,
	})
	defer func() { _ = lim.Close() }()

	cfg := RateLimitConfig{
		DefaultLimit: 1,
		Storage:      nil,
		Limiter:      lim,
	}

	mw := RateLimitMiddleware(cfg)
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/ip-test", nil)
	req1.RemoteAddr = "192.0.2.1:12345"
	rec1 := httptest.NewRecorder()
	mw(nextHandler).ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/ip-test", nil)
	req2.RemoteAddr = "192.0.2.1:12345"
	rec2 := httptest.NewRecorder()
	mw(nextHandler).ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code)
	assert.Equal(t, "1", rec2.Header().Get("Retry-After"))
}

func TestLoggingMiddleware_RequestIDPreservationAndGeneration(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := LoggingMiddleware(logger)

	t.Run("preserves existing X-Request-ID", func(t *testing.T) {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Request-ID", "custom-req-id-123")
		rec := httptest.NewRecorder()

		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		assert.Contains(t, buf.String(), "custom-req-id-123")
	})

	t.Run("generates new X-Request-ID when missing", func(t *testing.T) {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		var generatedID string
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			generatedID = rec.Header().Get("X-Request-ID")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		assert.NotEmpty(t, generatedID)
		assert.Contains(t, buf.String(), generatedID)
	})
}

func TestRecoveryMiddleware_PanicFormatting(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := RecoveryMiddleware(logger)

	req := httptest.NewRequest(http.MethodGet, "/panic-route", nil)
	rec := httptest.NewRecorder()

	mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("runtime memory violation")
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var pd ProblemDetails
	err := json.NewDecoder(rec.Body).Decode(&pd)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, pd.Status)
	assert.Equal(t, "Internal Server Error", pd.Title)
	assert.Equal(t, "/panic-route", pd.Instance)
	assert.Contains(t, buf.String(), "panic recovered")
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"short", "****"},
		{"12345678", "****"},
		{"123456789", "1234****6789"},
		{"secret-production-token-value", "secr****alue"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, maskAPIKey(tt.input))
		})
	}
}

func TestStatusWriter_Delegation(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}

	n, err := sw.Write([]byte("payload"))
	assert.NoError(t, err)
	assert.Equal(t, 7, n)
	assert.Equal(t, http.StatusOK, sw.status)

	sw.Flush()

	_, _, err = sw.Hijack()
	require.ErrorIs(t, err, http.ErrNotSupported)
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Len(t, id1, 32)
}
