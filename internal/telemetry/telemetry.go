// Package telemetry provides logging, metrics, and tracing abstractions.
package telemetry

import (
	"log/slog"
	"os"

	"github.com/HuanUnited/High-Throughput-API-Gateway-and-Rate-Limiter/internal/config"
)

// Logger is a thin wrapper around slog for future extensibility.
type Logger struct {
	logger *slog.Logger
}

// NewLogger creates a structured logger from configuration.
func NewLogger(cfg *config.Config) (*Logger, error) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	return &Logger{logger: logger}, nil
}

// Slogger returns the underlying slog logger.
func (l *Logger) Slogger() *slog.Logger {
	return l.logger
}

// Metrics registry skeleton — Prometheus client added in later commits.
// type Metrics struct{}
