package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/config"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/handler"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/ratelimit"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/storage"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var version = "dev"

func main() {
	healthcheckFlag := flag.Bool("healthcheck", false, "run internal healthcheck probe")
	flag.Parse()

	if *healthcheckFlag {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	slog.Info("starting gateway",
		"version", version,
		"port", cfg.Port,
		"upstream", cfg.UpstreamURL,
		"rate_limit_rps", cfg.RateLimitRPS,
		"rate_limit_burst", cfg.RateLimitBurst,
		"rate_limit_backend", cfg.RateLimitBackend,
		"metrics_enabled", cfg.MetricsEnabled,
	)

	targetURL, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return fmt.Errorf("parse upstream url: %w", err)
	}

	metricsInstance := metrics.New(metrics.Config{
		Enabled:   cfg.MetricsEnabled,
		Namespace: "api_gateway",
	})

	var clientStore handler.ClientStore
	if cfg.DatabaseURL != "" || cfg.DBHost != "" {
		pgCfg := storage.PostgresConfig{
			Host:            cfg.DBHost,
			Port:            cfg.DBPort,
			User:            cfg.DBUser,
			Password:        cfg.DBPassword,
			Database:        cfg.DBName,
			SSLMode:         "disable",
			MaxOpenConns:    50,
			MaxIdleConns:    25, // bottleneck preventions
			ConnMaxLifetime: time.Hour,
			ConnMaxIdleTime: 30 * time.Minute,
		}

		pgStore, dbErr := storage.NewPostgres(pgCfg)
		if dbErr != nil {
			logger.Warn("failed to connect to postgres, using default limits",
				"error", dbErr,
			)
		} else {
			// Wrap PostgreSQL store with L1 Memory Cache (1 minute TTL)
			clientStore = storage.NewCachedStore(pgStore, 1*time.Minute)
			defer func() { _ = pgStore.Close() }()
			logger.Info("postgres storage connected with L1 in-memory cache enabled")
		}
	}

	rateLimiter, err := createRateLimiter(cfg, logger)
	if err != nil {
		return fmt.Errorf("initialize rate limiter: %w", err)
	}
	defer func() { _ = rateLimiter.Close() }()

	proxyHandler := handler.NewProxyHandler(handler.ProxyConfig{
		Target:  targetURL,
		Timeout: 30 * time.Second,
	})

	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: cfg.RateLimitRPS,
		Storage:      clientStore,
		Limiter:      rateLimiter,
		Metrics:      metricsInstance,
	}

	mainHandler := metricsInstance.Middleware(
		handler.RecoveryMiddleware(logger)(
			handler.LoggingMiddleware(logger)(
				handler.RateLimitMiddleware(rateLimitConfig)(proxyHandler),
			),
		),
	)

	mux := http.NewServeMux()
	mux.Handle("/", mainHandler)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version,
		})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if clientStore != nil {
			if cachedStore, ok := clientStore.(*storage.CachedStore); ok {
				_ = cachedStore
			}
		}

		writeJSONResponse(w, http.StatusOK, map[string]string{
			"status": "ready",
		})
	})

	if cfg.MetricsEnabled {
		mux.Handle("/metrics", promhttp.Handler())
	}

	if cfg.DebugRateLimit {
		mux.HandleFunc("/debug/rate_limit", func(w http.ResponseWriter, r *http.Request) {
			clientID := r.Header.Get("X-API-Key")
			if clientID == "" {
				clientID = r.RemoteAddr
			}

			tokens, err := rateLimiter.Tokens(r.Context(), clientID)
			if err != nil {
				writeJSONResponse(w, http.StatusInternalServerError, map[string]string{
					"error": err.Error(),
				})
				return
			}

			writeJSONResponse(w, http.StatusOK, map[string]string{
				"client_id": clientID,
				"tokens":    fmt.Sprintf("%d", tokens),
				"limit":     fmt.Sprintf("%d", cfg.RateLimitBurst),
			})
		})
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	errChan := make(chan error, 1)

	go func() {
		slog.Info("server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("listen: %w", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case startupErr := <-errChan:
		stop()
		return startupErr
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	slog.Info("server stopped cleanly")
	return nil
}

func runHealthcheck() int {
	port := 8080
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
			port = p
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	urlHealth := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlHealth, nil)
	if err != nil {
		return 1
	}

	client := &http.Client{}
	// #nosec G704 -- Target URL is strictly constrained to localhost loopback with a validated integer port
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return 1
	}
	_ = resp.Body.Close()
	return 0
}
func createRateLimiter(cfg *config.Config, logger *slog.Logger) (ratelimit.Limiter, error) {
	if strings.EqualFold(cfg.RateLimitBackend, "redis") {
		logger.Info("using Redis rate limiter",
			"host", cfg.RedisHost,
			"port", cfg.RedisPort,
		)

		redisConfig := ratelimit.RedisConfig{
			Host:         cfg.RedisHost,
			Port:         cfg.RedisPort,
			Password:     cfg.RedisPassword,
			DB:           cfg.RedisDB,
			PoolSize:     cfg.RedisPoolSize,
			MinIdleConns: cfg.RedisMinIdle,
			DialTimeout:  cfg.RedisDialTimeout,
			ReadTimeout:  cfg.RedisReadTimeout,
			WriteTimeout: cfg.RedisWriteTimeout,
			KeyPrefix:    cfg.RedisKeyPrefix,
		}

		limitConfig := ratelimit.Config{
			TokensPerSecond: float64(cfg.RateLimitRPS),
			BurstSize:       cfg.RateLimitBurst,
		}

		redisLimiter, err := ratelimit.NewRedisLimiter(redisConfig, limitConfig)
		if err != nil {
			logger.Warn("failed to create Redis rate limiter, falling back to in-memory",
				"error", err,
			)
		} else {
			return redisLimiter, nil
		}
	}

	logger.Info("using in-memory rate limiter")
	return ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: float64(cfg.RateLimitRPS),
		BurstSize:       cfg.RateLimitBurst,
		CleanupInterval: cfg.RateLimitCleanupInterval,
	}), nil
}

func writeJSONResponse(w http.ResponseWriter, status int, payload map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
