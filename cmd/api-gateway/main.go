package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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
	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
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

	// Parse upstream URL
	targetURL, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return fmt.Errorf("parse upstream url: %w", err)
	}

	// Initialize metrics
	metricsInstance := metrics.New(metrics.Config{
		Enabled:   cfg.MetricsEnabled,
		Namespace: "api_gateway",
	})

	// Initialize storage (if configured)
	var clientStore handler.ClientStore
	if cfg.DatabaseURL != "" {
		pgCfg := storage.PostgresConfig{
			Host:            cfg.DBHost,
			Port:            cfg.DBPort,
			User:            cfg.DBUser,
			Password:        cfg.DBPassword,
			Database:        cfg.DBName,
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
			ConnMaxIdleTime: 30 * time.Minute,
		}

		pgStore, dbErr := storage.NewPostgres(pgCfg)
		if dbErr != nil {
			logger.Warn("failed to connect to postgres, using default limits",
				"error", err,
			)
		} else {
			clientStore = pgStore
			defer func() { _ = pgStore.Close() }()
			logger.Info("postgres storage connected")
		}
	}

	// Initialize rate limiter based on configuration
	rateLimiter, err := createRateLimiter(cfg, logger)
	if err != nil {
		return fmt.Errorf("initialize rate limiter: %w", err)
	}
	defer func() { _ = rateLimiter.Close() }()

	// Build the handler chain:
	// 1. Metrics middleware (outermost)
	// 2. Recovery middleware
	// 3. Logging middleware
	// 4. Rate limiting middleware
	// 5. Reverse proxy handler (innermost)
	proxyHandler := handler.NewProxyHandler(handler.ProxyConfig{
		Target:  targetURL,
		Timeout: 30 * time.Second,
	})

	// Wrap proxy with rate limiting
	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: cfg.RateLimitRPS,
		Storage:      clientStore,
		// Use the new interface-based limiter
		Limiter: rateLimiter,
	}

	// Build the main handler chain
	mainHandler := metricsInstance.Middleware(
		handler.RecoveryMiddleware(logger)(
			handler.LoggingMiddleware(logger)(
				handler.RateLimitMiddleware(rateLimitConfig)(proxyHandler),
			),
		),
	)

	// Create the mux and register routes
	mux := http.NewServeMux()

	// Main proxied routes (with rate limiting)
	mux.Handle("/", mainHandler)

	// Health endpoints (exempt from rate limiting for liveness probes)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version,
		})
	})

	// Readiness endpoint with database check
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Check database connectivity if configured
		if clientStore != nil {
			if pgStore, ok := clientStore.(*storage.Postgres); ok {
				ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				defer cancel()

				if err := pgStore.HealthCheck(ctx); err != nil {
					writeJSONResponse(w, http.StatusServiceUnavailable, map[string]string{
						"status": "not_ready",
						"reason": "database_unavailable",
					})
					return
				}
			}
		}

		writeJSONResponse(w, http.StatusOK, map[string]string{
			"status": "ready",
		})
	})

	// Metrics endpoint for Prometheus scraping
	if cfg.MetricsEnabled {
		mux.Handle("/metrics", promhttp.Handler())
	}

	// Rate limit debug endpoint (optional)
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

	// Build the HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// Channel for startup errors
	errChan := make(chan error, 1)

	// Start the server
	go func() {
		slog.Info("server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("listen: %w", err)
		}
	}()

	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case startupErr := <-errChan:
		stop()
		return startupErr
	}

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	slog.Info("server stopped cleanly")
	return nil
}

// createRateLimiter creates the appropriate rate limiter based on configuration
func createRateLimiter(cfg *config.Config, logger *slog.Logger) (ratelimit.Limiter, error) {
	// Check if Redis is configured and available
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

	// Fall back to in-memory rate limiter
	logger.Info("using in-memory rate limiter")
	return ratelimit.NewMemoryLimiter(ratelimit.Config{
		TokensPerSecond: float64(cfg.RateLimitRPS),
		BurstSize:       cfg.RateLimitBurst,
		CleanupInterval: cfg.RateLimitCleanupInterval,
	}), nil
}

// writeJSONResponse writes a JSON response with the given status code.
func writeJSONResponse(w http.ResponseWriter, status int, payload map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// parseLogLevel converts a string to slog.Level.
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
