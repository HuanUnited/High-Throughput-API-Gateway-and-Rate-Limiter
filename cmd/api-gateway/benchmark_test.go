package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/handler"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/limiter"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
)

// setupBenchmarkServer creates a complete gateway server for benchmarking
func setupBenchmarkServer(b *testing.B, rateLimitRPS int, burstSize int) *httptest.Server {
	// Set up test upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		})
	}))
	b.Cleanup(upstream.Close)

	// Parse upstream URL
	targetURL, _ := url.Parse(upstream.URL)

	// Initialize metrics (disabled for pure latency benchmarks)
	metricsInstance := metrics.New(metrics.Config{
		Enabled:   false,
		Namespace: "api_gateway",
	})

	// Initialize rate limiter with generous limits for benchmark
	bucket := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: float64(rateLimitRPS),
		BurstSize:       burstSize,
		CleanupInterval: time.Minute,
	})

	// Build handler chain
	proxyHandler := handler.NewProxyHandler(handler.ProxyConfig{
		Target:  targetURL,
		Timeout: 30 * time.Second,
	})

	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: rateLimitRPS,
		Storage:      nil, // No DB for benchmark
		Limiter:      bucket,
	}

	// Set up logger to discard for benchmarks
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	mainHandler := metricsInstance.Middleware(
		handler.RecoveryMiddleware(logger)(
			handler.LoggingMiddleware(logger)(
				handler.RateLimitMiddleware(rateLimitConfig)(proxyHandler),
			),
		),
	)

	// Create test server
	server := httptest.NewServer(mainHandler)
	b.Cleanup(server.Close)

	return server
}

// BenchmarkProxyThroughput measures raw proxying throughput
func BenchmarkProxyThroughput(b *testing.B) {
	server := setupBenchmarkServer(b, 100000, 100000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
		},
		Timeout: 5 * time.Second,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(server.URL + "/api/v1/test")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// BenchmarkRateLimitedThroughput measures throughput with rate limiting enabled
func BenchmarkRateLimitedThroughput(b *testing.B) {
	server := setupBenchmarkServer(b, 1000, 1000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
		},
		Timeout: 5 * time.Second,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(server.URL + "/api/v1/test")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// BenchmarkCompositeRequest benchmarks a complete request cycle
func BenchmarkCompositeRequest(b *testing.B) {
	server := setupBenchmarkServer(b, 100000, 100000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
		},
		Timeout: 5 * time.Second,
	}

	// Create a standard request body
	payload := map[string]any{
		"user_id":   12345,
		"action":    "read",
		"resource":  "data",
		"timestamp": time.Now().Unix(),
	}
	body, _ := json.Marshal(payload)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("POST", server.URL+"/api/v1/data", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "benchmark-key")

			resp, err := client.Do(req)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// BenchmarkMemoryAllocation measures memory allocation per request
func BenchmarkMemoryAllocation(b *testing.B) {
	server := setupBenchmarkServer(b, 100000, 100000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
		},
		Timeout: 5 * time.Second,
	}

	b.ReportAllocs()

	for b.Loop() {
		resp, err := client.Get(server.URL + "/api/v1/bench")
		if err != nil {
			b.Error(err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// BenchmarkLatencyPercentiles measures latency percentiles
func BenchmarkLatencyPercentiles(b *testing.B) {
	server := setupBenchmarkServer(b, 100000, 100000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        50,
			MaxIdleConnsPerHost: 50,
		},
		Timeout: 5 * time.Second,
	}

	// Pre-warm the connection pool
	for range 10 {
		resp, _ := client.Get(server.URL + "/warmup")
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	b.ResetTimer()

	latencies := make([]time.Duration, 0, b.N)
	startTime := time.Now()

	for i := 0; i < b.N; i++ {
		reqStart := time.Now()
		resp, err := client.Get(server.URL + "/api/v1/latency")
		if err != nil {
			b.Error(err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		latencies = append(latencies, time.Since(reqStart))
	}

	elapsed := time.Since(startTime)
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	// Calculate percentiles
	percentiles := calculatePercentiles(latencies)
	b.ReportMetric(percentiles[50].Seconds()*1000, "p50_ms")
	b.ReportMetric(percentiles[95].Seconds()*1000, "p95_ms")
	b.ReportMetric(percentiles[99].Seconds()*1000, "p99_ms")
}

// BenchmarkConcurrentLoad benchmarks under high concurrency
func BenchmarkConcurrentLoad(b *testing.B) {
	server := setupBenchmarkServer(b, 100000, 100000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
		},
		Timeout: 5 * time.Second,
	}

	b.ResetTimer()

	var wg sync.WaitGroup
	concurrency := 50
	wg.Add(concurrency)

	for range concurrency {
		go func() {
			defer wg.Done()

			for range b.N / concurrency {
				resp, err := client.Get(server.URL + "/api/v1/load")
				if err != nil {
					b.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}

	wg.Wait()
}

// BenchmarkHealthEndpoint benchmarks the health check endpoint (no rate limiting)
func BenchmarkHealthEndpoint(b *testing.B) {
	// Create a simple health endpoint handler
	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	server := httptest.NewServer(handlerFunc)
	defer server.Close()

	client := &http.Client{Timeout: 1 * time.Second}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(server.URL + "/healthz")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// BenchmarkRateLimiter benchmarks the rate limiter in isolation
func BenchmarkRateLimiter(b *testing.B) {
	bucket := limiter.NewTokenBucket(1000000, 100000)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if !bucket.Allow() {
				b.Error("rate limiter rejected request")
				return
			}
		}
	})
}

// BenchmarkRedisRateLimiter benchmarks the Redis rate limiter (if enabled)
func BenchmarkRedisRateLimiter(b *testing.B) {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		b.Skip("REDIS_HOST not set, skipping Redis benchmark")
	}

	redisCfg := ratelimit.RedisConfig{
		Host:     redisHost,
		Port:     6379,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       2,
	}

	limitCfg := ratelimit.Config{
		TokensPerSecond: 100000,
		BurstSize:       100000,
	}

	newRedisLimiter, err := ratelimit.NewRedisLimiter(redisCfg, limitCfg)
	if err != nil {
		b.Skipf("Redis not available: %v", err)
	}
	defer func() { _ = newRedisLimiter.Close() }()

	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := newRedisLimiter.Allow(ctx, "benchmark-client")
			if err != nil {
				b.Error(err)
				return
			}
		}
	})
}

// Helper function to calculate percentiles
func calculatePercentiles(data []time.Duration) map[int]time.Duration {
	if len(data) == 0 {
		return map[int]time.Duration{50: 0, 95: 0, 99: 0}
	}

	sorted := slices.Clone(data)
	slices.Sort(sorted)

	percentiles := make(map[int]time.Duration)
	percentiles[50] = sorted[int(float64(len(sorted))*0.50)]
	percentiles[95] = sorted[int(float64(len(sorted))*0.95)]
	percentiles[99] = sorted[int(float64(len(sorted))*0.99)]

	return percentiles
}

//nolint:unused // reserved for future benchmark implementations
type redisLimiter struct {
	count int64
}

//nolint:unused // satisfies upcoming limiter interface additions
func (r *redisLimiter) Allow(_ context.Context, _ string) (bool, error) {
	r.count++
	return true, nil
}

//nolint:unused // satisfies upcoming limiter interface additions
func (r *redisLimiter) Close() error {
	return nil
}

// BenchmarkTable benchmarks different configurations
func BenchmarkTable(b *testing.B) {
	benches := []struct {
		name         string
		rateLimitRPS int
		burstSize    int
	}{
		{"NoRateLimit", 1000000, 1000000},
		{"RateLimit_100rps", 100, 100},
		{"RateLimit_1000rps", 1000, 1000},
		{"RateLimit_10000rps", 10000, 10000},
	}

	for _, bb := range benches {
		b.Run(bb.name, func(b *testing.B) {
			server := setupBenchmarkServer(b, bb.rateLimitRPS, bb.burstSize)

			client := &http.Client{
				Transport: &http.Transport{
					MaxIdleConns:        100,
					MaxIdleConnsPerHost: 100,
				},
				Timeout: 5 * time.Second,
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					resp, err := client.Get(server.URL + "/api/v1/table")
					if err != nil {
						b.Error(err)
						return
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			})
		})
	}
}

// TestMain to set up test environment
func TestMain(m *testing.M) {
	// Set up any test environment variables
	_ = os.Setenv("BENCHMARK_MODE", "true")

	// Run tests
	code := m.Run()

	// Clean up
	_ = os.Unsetenv("BENCHMARK_MODE")

	os.Exit(code)
}

// Example output format for benchmarks
func ExampleBenchmark() {
	fmt.Println("Benchmark output format:")
	fmt.Println("BenchmarkProxyThroughput-8   	   50000	     24677 ns/op")
	fmt.Println("BenchmarkProxyThroughput-8   	   50000	      4055 req/s")
	fmt.Println("BenchmarkRateLimited-8       	   30000	     39812 ns/op")
	fmt.Println("PASS")
}
