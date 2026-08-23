package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/handler"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/limiter"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
)

func setupBenchmarkServer(b *testing.B, rateLimitRPS int, burstSize int) *httptest.Server {
	b.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		})
	}))
	b.Cleanup(upstream.Close)

	targetURL, _ := url.Parse(upstream.URL)

	metricsInstance := metrics.New(metrics.Config{
		Enabled:   false,
		Namespace: "api_gateway",
	})

	bucket := ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: float64(rateLimitRPS),
		BurstSize:       burstSize,
		CleanupInterval: time.Hour,
	})
	b.Cleanup(func() { _ = bucket.Close() })

	proxyHandler := handler.NewProxyHandler(handler.ProxyConfig{
		Target:  targetURL,
		Timeout: 30 * time.Second,
	})

	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: rateLimitRPS,
		Storage:      nil,
		Limiter:      bucket,
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	mainHandler := metricsInstance.Middleware(
		handler.RecoveryMiddleware(logger)(
			handler.LoggingMiddleware(logger)(
				handler.RateLimitMiddleware(rateLimitConfig)(proxyHandler),
			),
		),
	)

	server := httptest.NewServer(mainHandler)
	b.Cleanup(server.Close)

	return server
}

func BenchmarkProxyThroughput(b *testing.B) {
	server := setupBenchmarkServer(b, 1000000, 1000000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
			MaxConnsPerHost:     500,
		},
		Timeout: 5 * time.Second,
	}

	b.ReportAllocs()
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

func BenchmarkCompositeRequest(b *testing.B) {
	server := setupBenchmarkServer(b, 1000000, 1000000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
			MaxConnsPerHost:     500,
		},
		Timeout: 5 * time.Second,
	}

	payload := map[string]any{
		"user_id":   12345,
		"action":    "read",
		"resource":  "data",
		"timestamp": time.Now().Unix(),
	}
	body, _ := json.Marshal(payload)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/data", bytes.NewReader(body))
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

func BenchmarkLatencyPercentiles(b *testing.B) {
	server := setupBenchmarkServer(b, 1000000, 1000000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
		Timeout: 5 * time.Second,
	}

	// Warm up connections
	for range 10 {
		resp, err := client.Get(server.URL + "/warmup")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}

	maxSamples := min(b.N, 100000)
	latencies := make([]time.Duration, 0, maxSamples)

	b.ReportAllocs()
	b.ResetTimer()

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

		if len(latencies) < maxSamples {
			latencies = append(latencies, time.Since(reqStart))
		}
	}

	elapsed := time.Since(startTime)
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "req/s")

	percentiles := calculatePercentiles(latencies)
	b.ReportMetric(percentiles[50].Seconds()*1000, "p50_ms")
	b.ReportMetric(percentiles[95].Seconds()*1000, "p95_ms")
	b.ReportMetric(percentiles[99].Seconds()*1000, "p99_ms")
}

func BenchmarkConcurrentLoad(b *testing.B) {
	server := setupBenchmarkServer(b, 1000000, 1000000)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
			MaxConnsPerHost:     500,
		},
		Timeout: 5 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(server.URL + "/api/v1/load")
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

func BenchmarkRateLimiter(b *testing.B) {
	bucket := limiter.NewTokenBucket(b.N+1000, float64(b.N+1000))

	b.ReportAllocs()
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

func BenchmarkRedisRateLimiter(b *testing.B) {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		b.Skip("REDIS_HOST not set, skipping Redis benchmark")
	}

	redisCfg := ratelimit.RedisConfig{
		Host:         redisHost,
		Port:         6379,
		Password:     os.Getenv("REDIS_PASSWORD"),
		DB:           4,
		PoolSize:     100,
		MinIdleConns: 20,
	}

	limitCfg := ratelimit.Config{
		TokensPerSecond: float64(b.N + 100000),
		BurstSize:       b.N + 100000,
	}

	newRedisLimiter, err := ratelimit.NewRedisLimiter(redisCfg, limitCfg)
	if err != nil {
		b.Skipf("Redis not available: %v", err)
	}
	defer func() { _ = newRedisLimiter.Close() }()

	ctx := context.Background()
	b.ReportAllocs()
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
