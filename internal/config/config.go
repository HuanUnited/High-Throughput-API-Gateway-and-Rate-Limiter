package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration values.
// All fields are immutable after loading to prevent data races.
type Config struct {
	Port int

	UpstreamURL string

	RateLimitRPS   int
	RateLimitBurst int

	LogLevel string

	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
// It returns an error if any required values are invalid or missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:           getEnvInt("PORT", 8080),
		UpstreamURL:    getEnv("UPSTREAM_URL", "http://localhost:3000"),
		RateLimitRPS:   getEnvInt("RATE_LIMIT_RPS", 10),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 20),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		ReadTimeout:    time.Duration(getEnvInt("READ_TIMEOUT_SECONDS", 15)) * time.Second,
		WriteTimeout:   time.Duration(getEnvInt("WRITE_TIMEOUT_SECONDS", 15)) * time.Second,
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// validate performs semantic validation on loaded configuration values.
func (c *Config) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}

	if !strings.HasPrefix(c.UpstreamURL, "http://") && !strings.HasPrefix(c.UpstreamURL, "https://") {
		return fmt.Errorf("UPSTREAM_URL must start with http:// or https://, got %s", c.UpstreamURL)
	}

	if c.RateLimitRPS <= 0 {
		return fmt.Errorf("RATE_LIMIT_RPS must be greater than 0, got %d", c.RateLimitRPS)
	}

	if c.RateLimitBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_BURST must be greater than 0, got %d", c.RateLimitBurst)
	}

	if c.RateLimitBurst < c.RateLimitRPS {
		return fmt.Errorf(
			"RATE_LIMIT_BURST (%d) must be greater than or equal to RATE_LIMIT_RPS (%d)",
			c.RateLimitBurst, c.RateLimitRPS,
		)
	}

	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
		// valid
	default:
		return fmt.Errorf("LOG_LEVEL must be one of [debug, info, warn, error], got %s", c.LogLevel)
	}

	return nil
}

// getEnv returns the value of the environment variable key,
// or fallback if the variable is not set or is empty.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return fallback
}

// getEnvInt returns the integer value of the environment variable key,
// or fallback if not set, empty, or not a valid integer.
func getEnvInt(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}
