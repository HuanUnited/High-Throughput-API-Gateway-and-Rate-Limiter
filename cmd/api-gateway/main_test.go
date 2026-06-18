package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		Port:           8080,
		UpstreamURL:    "http://localhost:3000",
		RateLimitRPS:   10,
		RateLimitBurst: 20,
	}

	handler := newRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestDefaultEndpointReturnsServiceUnavailable(t *testing.T) {
	cfg := &config.Config{
		Port:           8080,
		UpstreamURL:    "http://localhost:3000",
		RateLimitRPS:   10,
		RateLimitBurst: 20,
	}

	handler := newRouter(cfg)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestParseLogLevel(t *testing.T) {
	testCases := []struct {
		input string
		want  string
	}{
		{"debug", "DEBUG"},
		{"info", "INFO"},
		{"warn", "WARN"},
		{"error", "ERROR"},
		{"invalid", "INFO"}, // falls back to info
	}

	for _, tc := range testCases {
		level := parseLogLevel(tc.input)
		require.NotNil(t, level)
		assert.Equal(t, tc.want, level.String())
	}
}
