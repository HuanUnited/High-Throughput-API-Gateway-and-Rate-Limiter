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
	"syscall"
	"time"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/config"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/handler"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/limiter"
	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/metrics"
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
			// Parse from DatabaseURL or use env vars
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

		pgStore, err := storage.NewPostgres(pgCfg)
		if err != nil {
			logger.Warn("failed to connect to postgres, using default limits",
				"error", err,
			)
		} else {
			clientStore = pgStore
			defer pgStore.Close()
			logger.Info("postgres storage connected")
		}
	}

	// Initialize rate limiter
	bucket := limiter.NewTokenBucket(cfg.RateLimitBurst, float64(cfg.RateLimitRPS))

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

	// Wrap proxy with metrics-aware middleware
	rateLimitConfig := handler.RateLimitConfig{
		DefaultLimit: cfg.RateLimitRPS,
		Storage:      clientStore,
	}

	// Build the main handler chain
	mainHandler := metricsInstance.Middleware(
		handler.RecoveryMiddleware(logger)(
			handler.LoggingMiddleware(logger)(
				handler.RateLimitMiddleware(bucket, rateLimitConfig)(proxyHandler),
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

// writeJSONResponse writes a JSON response with the given status code.
func writeJSONResponse(w http.ResponseWriter, status int, payload map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// parseLogLevel converts a string to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch level {
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
