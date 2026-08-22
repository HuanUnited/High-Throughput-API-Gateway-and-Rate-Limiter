package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/config"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/handler"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
)

// TestParseLogLevel verifies the log level parsing function.
func TestParseLogLevel(t *testing.T) {
	testCases := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"DEBUG", slog.LevelDebug},  // case-insensitive
		{"INFO", slog.LevelInfo},    // case-insensitive
		{"invalid", slog.LevelInfo}, // falls back to info
		{"", slog.LevelInfo},        // empty falls back to info
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseLogLevel(tc.input)
			if got != tc.expected {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

// TestRateLimiterFactory tests the rate limiter factory function
func TestRateLimiterFactory(t *testing.T) {
	t.Run("MemoryLimiter", func(t *testing.T) {
		cfg := &config.Config{
			RateLimitBackend: "memory",
			RateLimitRPS:     10,
			RateLimitBurst:   100,
		}

		limiter, err := createRateLimiter(cfg, slog.Default())
		if err != nil {
			t.Fatalf("failed to create memory limiter: %v", err)
		}
		defer func() { _ = limiter.Close() }()

		// Test basic functionality
		ctx := context.Background()
		allowed, allowErr := limiter.Allow(ctx, "test-client")
		if allowErr != nil {
			t.Fatalf("allow failed: %v", err)
		}
		if !allowed {
			t.Error("expected request to be allowed")
		}
	})

	t.Run("RedisLimiter_Unavailable", func(t *testing.T) {
		cfg := &config.Config{
			RateLimitBackend: "redis",
			RedisHost:        "invalid-host",
			RedisPort:        6379,
			RateLimitRPS:     10,
			RateLimitBurst:   100,
		}

		// Should fall back to memory limiter if Redis is unavailable
		limiter, err := createRateLimiter(cfg, slog.Default())
		if err != nil {
			t.Fatalf("factory should not return error, got: %v", err)
		}
		defer func() { _ = limiter.Close() }()

		// Verify it's a memory limiter
		_, ok := limiter.(*ratelimit.MemoryLimiter)
		if !ok {
			t.Error("expected memory limiter fallback when Redis is unavailable")
		}
	})
}

// TestRedisRateLimiter tests the Redis rate limiter if available
func TestRedisRateLimiter(t *testing.T) {
	// Skip if Redis is not configured
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		t.Skip("REDIS_HOST not set, skipping Redis tests")
	}

	redisCfg := ratelimit.RedisConfig{
		Host:     redisHost,
		Port:     6379,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       1, // Use separate DB for tests
	}

	limitCfg := ratelimit.Config{
		TokensPerSecond: 10,
		BurstSize:       100,
	}

	limiter, err := ratelimit.NewRedisLimiter(redisCfg, limitCfg)
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer func() { _ = limiter.Close() }()

	ctx := context.Background()

	t.Run("AllowAndTokens", func(t *testing.T) {
		// Reset the client's state
		err := limiter.Reset(ctx, "test-client")
		if err != nil {
			t.Fatalf("failed to reset: %v", err)
		}

		// Allow some requests
		for range 5 {
			allowed, allowedErr := limiter.Allow(ctx, "test-client")
			if allowedErr != nil {
				t.Fatalf("allow failed: %v", err)
			}
			if !allowed {
				t.Error("expected request to be allowed")
			}
		}

		// Check remaining tokens
		tokens, err := limiter.Tokens(ctx, "test-client")
		if err != nil {
			t.Fatalf("failed to get tokens: %v", err)
		}

		if tokens != 95 {
			t.Errorf("expected 95 tokens, got %d", tokens)
		}

		// Reset and verify
		err = limiter.Reset(ctx, "test-client")
		if err != nil {
			t.Fatalf("failed to reset: %v", err)
		}

		tokens, err = limiter.Tokens(ctx, "test-client")
		if err != nil {
			t.Fatalf("failed to get tokens after reset: %v", err)
		}

		if tokens != 100 {
			t.Errorf("expected 100 tokens after reset, got %d", tokens)
		}
	})

	t.Run("RateLimitExceeded", func(t *testing.T) {
		// Create a new client ID
		clientID := fmt.Sprintf("limited-client-%d", time.Now().UnixNano())

		// Use up all tokens
		limited := false
		for i := 0; i <= 100; i++ {
			allowed, err := limiter.Allow(ctx, clientID)
			if err != nil {
				t.Fatalf("allow failed: %v", err)
			}
			if !allowed {
				limited = true
				break
			}
		}

		if !limited {
			t.Error("expected rate limit to be exceeded after 100 requests")
		}

		// Clean up
		err := limiter.Reset(ctx, clientID)
		if err != nil {
			t.Fatalf("failed to cleanup: %v", err)
		}
	})
}

// TestMainIntegration validates the full server wiring with a mock upstream.
func TestMainIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a mock upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// #nosec G705
		_, _ = w.Write([]byte(`{"upstream":"response","path":"` + r.URL.Path + `"}`))
	}))
	defer upstream.Close()

	// Set test environment variables
	t.Setenv("PORT", "6379")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("RATE_LIMIT_RPS", "100")
	t.Setenv("RATE_LIMIT_BURST", "200")
	t.Setenv("RATE_LIMIT_BACKEND", "memory") // Use memory for tests
	t.Setenv("LOG_LEVEL", "debug")

	// Load config
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Create rate limiter
	rateLimiter, err := createRateLimiter(cfg, slog.Default())
	if err != nil {
		t.Fatalf("failed to create rate limiter: %v", err)
	}
	defer func() {
		_ = rateLimiter.Close()
	}()

	// Build the handler chain similar to run()
	targetURL, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		t.Fatalf("failed to parse upstream URL: %v", err)
	}

	proxyHandler := handler.NewProxyHandler(handler.ProxyConfig{
		Target:  targetURL,
		Timeout: 30 * time.Second,
	})

	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: cfg.RateLimitRPS,
		Storage:      nil,
		Limiter:      rateLimiter,
	}

	mux := http.NewServeMux()
	mux.Handle("/", handler.RateLimitMiddleware(rateLimitConfig)(
		handler.RecoveryMiddleware(slog.Default())(
			handler.LoggingMiddleware(slog.Default())(proxyHandler),
		),
	))

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
	})

	// Create a test server using the mux
	server := httptest.NewServer(mux)
	defer server.Close()

	// Test health endpoint
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health endpoint: expected status 200, got %d", resp.StatusCode)
	}

	var healthBody map[string]string
	if decodeErr := json.NewDecoder(resp.Body).Decode(&healthBody); decodeErr != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	if healthBody["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", healthBody["status"])
	}

	// Test proxied request
	proxiedResp, err := http.Get(server.URL + "/api/test")
	if err != nil {
		t.Fatalf("proxied request failed: %v", err)
	}
	defer func() { _ = proxiedResp.Body.Close() }()

	if proxiedResp.StatusCode != http.StatusOK {
		t.Errorf("proxy endpoint: expected status 200, got %d", proxiedResp.StatusCode)
	}

	// Verify gateway headers are set
	if gw := proxiedResp.Header.Get("X-Gateway"); gw != "go-limiter" {
		t.Errorf("expected X-Gateway header 'go-limiter', got '%s'", gw)
	}
	if fh := proxiedResp.Header.Get("X-Forwarded-Host"); fh == "" {
		t.Error("expected X-Forwarded-Host header to be set")
	}

	// Decode proxy response
	var proxyBody map[string]string
	if err := json.NewDecoder(proxiedResp.Body).Decode(&proxyBody); err != nil {
		t.Fatalf("failed to decode proxy response: %v", err)
	}
	if proxyBody["upstream"] != "response" {
		t.Errorf("expected upstream response, got '%s'", proxyBody["upstream"])
	}
	if proxyBody["path"] != "/api/test" {
		t.Errorf("expected path /api/test, got '%s'", proxyBody["path"])
	}
}

// TestRateLimit429 verifies that the rate limiter returns 429.
func TestRateLimit429(t *testing.T) {
	// Create a rate limiter with capacity 1 and very slow refill
	memLimiter := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: 0.001,
		BurstSize:       1,
	})

	// Create the middleware
	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: 1,
		Storage:      nil,
		Limiter:      memLimiter,
	}

	middleware := handler.RateLimitMiddleware(rateLimitConfig)
	handlerRateLimit := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should succeed
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handlerRateLimit.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rec.Code)
	}

	// Second request should be rate limited
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	handlerRateLimit.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rec.Code)
	}

	// Verify JSON error body
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Errorf("expected 'rate limit exceeded', got '%s'", body["error"])
	}

	// Verify Content-Type header
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type, got '%s'", ct)
	}
}

// TestRecoveryMiddleware verifies that panics are recovered.
func TestRecoveryMiddleware(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	middleware := handler.RecoveryMiddleware(logger)
	handlerMiddleware := middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// This should not crash
	handlerMiddleware.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// TestLoggingMiddleware verifies the logging middleware works.
func TestLoggingMiddleware(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	middleware := handler.LoggingMiddleware(logger)
	handlerMiddleWare := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()

	handlerMiddleWare.ServeHTTP(rec, req)

	// Verify log output contains expected fields
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "request completed") {
		t.Error("expected request completed log")
	}
	if !strings.Contains(logOutput, "/test-path") {
		t.Error("expected path in log")
	}
	if !strings.Contains(logOutput, "201") {
		t.Error("expected status code in log")
	}
	if !strings.Contains(logOutput, "GET") {
		t.Error("expected method in log")
	}
}

// TestGracefulShutdown verifies the server shuts down gracefully.
func TestGracefulShutdown(t *testing.T) {
	// Build a simple server
	server := &http.Server{
		Addr:              "127.0.0.1:0",
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), //nolint:golines
	}

	// Start the server
	go func() {
		_ = server.ListenAndServe()
	}()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Send shutdown signal
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("graceful shutdown failed: %v", err)
	}
}

// TestSignalHandling verifies SIGTERM is correctly handled.
func TestSignalHandling(t *testing.T) {
	// Test that signal.NotifyContext works for SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Send SIGTERM to self
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("failed to find self process: %v", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	// Verify context is canceled
	select {
	case <-ctx.Done():
		// Success
	case <-time.After(time.Second):
		t.Error("context was not cancelled after SIGTERM")
	}
}

// TestVersion checks the version variable exists.
func TestVersion(t *testing.T) {
	if version == "" {
		t.Error("version variable should not be empty")
	}
}

// BenchmarkRateLimit benchmarks the rate limiter throughput.
func BenchmarkRateLimit(b *testing.B) {
	memLimiter := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: 100000,
		BurstSize:       100000,
	})

	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: 100000,
		Storage:      nil,
		Limiter:      memLimiter,
	}

	middleware := handler.RateLimitMiddleware(rateLimitConfig)
	handlerMiddleWare := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	for b.Loop() {
		handlerMiddleWare.ServeHTTP(rec, req)
	}
}

// BenchmarkRedisRateLimit benchmarks the Redis rate limiter if available.
func BenchmarkRedisRateLimit(b *testing.B) {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		b.Skip("REDIS_HOST not set, skipping Redis benchmark")
	}

	redisCfg := ratelimit.RedisConfig{
		Host:     redisHost,
		Port:     6379,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       2, // Use separate DB for benchmarks
	}

	limitCfg := ratelimit.Config{
		TokensPerSecond: 100000,
		BurstSize:       100000,
	}

	limiter, err := ratelimit.NewRedisLimiter(redisCfg, limitCfg)
	if err != nil {
		b.Skipf("Redis not available: %v", err)
	}
	defer func() { _ = limiter.Close() }()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := limiter.Allow(ctx, "benchmark-client")
			if err != nil {
				b.Error(err)
				return
			}
		}
	})
}
