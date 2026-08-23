// Package config manages configuration parsing for the API gateway.
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

	RateLimitRPS             int
	RateLimitBurst           int
	RateLimitBackend         string
	RateLimitCleanupInterval time.Duration

	LogLevel string

	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// Metrics configuration
	MetricsEnabled bool

	// Database configuration
	DatabaseURL string
	DBHost      string
	DBPort      int
	DBUser      string
	DBPassword  string
	DBName      string

	// Redis configuration
	RedisHost         string
	RedisPort         int
	RedisPassword     string
	RedisDB           int
	RedisPoolSize     int
	RedisMinIdle      int
	RedisDialTimeout  time.Duration
	RedisReadTimeout  time.Duration
	RedisWriteTimeout time.Duration
	RedisKeyPrefix    string

	// Debug configuration
	DebugRateLimit bool
}

// Load reads configuration from environment variables with sensible defaults.
// It returns an error if any required values are invalid or missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:             getEnvInt("PORT", 8080),
		UpstreamURL:      getEnv("UPSTREAM_URL", "http://localhost:3000"),
		RateLimitRPS:     getEnvInt("RATE_LIMIT_RPS", 10),
		RateLimitBurst:   getEnvInt("RATE_LIMIT_BURST", 20),
		RateLimitBackend: getEnv("RATE_LIMIT_BACKEND", "memory"),
		RateLimitCleanupInterval: getEnvDuration(
			"RATE_LIMIT_CLEANUP_INTERVAL",
			getEnvDuration("RATE_LIMIT_CLEANUP_INTERVAL_SECONDS", 300*time.Second),
		),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		ReadTimeout:  time.Duration(getEnvInt("READ_TIMEOUT_SECONDS", 15)) * time.Second,
		WriteTimeout: time.Duration(getEnvInt("WRITE_TIMEOUT_SECONDS", 15)) * time.Second,

		// Metrics configuration
		MetricsEnabled: getEnvBool("METRICS_ENABLED", true),

		// Database configuration
		DatabaseURL: getEnv("DATABASE_URL", ""),
		DBHost:      getEnv("DB_HOST", "localhost"),
		DBPort:      getEnvInt("DB_PORT", 5432),
		DBUser:      getEnv("DB_USER", ""),
		DBPassword:  getEnv("DB_PASSWORD", ""),
		DBName:      getEnv("DB_NAME", ""),

		// Redis configuration
		RedisHost:         getEnv("REDIS_HOST", ""),
		RedisPort:         getEnvInt("REDIS_PORT", 6379),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvInt("REDIS_DB", 0),
		RedisPoolSize:     getEnvInt("REDIS_POOL_SIZE", 20),
		RedisMinIdle:      getEnvInt("REDIS_MIN_IDLE", 5),
		RedisDialTimeout:  time.Duration(getEnvInt("REDIS_DIAL_TIMEOUT_SECONDS", 5)) * time.Second,
		RedisReadTimeout:  time.Duration(getEnvInt("REDIS_READ_TIMEOUT_SECONDS", 3)) * time.Second,
		RedisWriteTimeout: time.Duration(getEnvInt("REDIS_WRITE_TIMEOUT_SECONDS", 3)) * time.Second,
		RedisKeyPrefix:    getEnv("REDIS_KEY_PREFIX", "ratelimit:"),

		// Debug configuration
		DebugRateLimit: getEnvBool("DEBUG_RATE_LIMIT", false),
	}

	// If DATABASE_URL is set, parse it to extract components
	if cfg.DatabaseURL != "" {
		if err := cfg.parseDatabaseURL(); err != nil {
			return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
		}
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// parseDatabaseURL parses a PostgreSQL connection string.
// Format: postgres://user:password@host:port/dbname
func (c *Config) parseDatabaseURL() error {
	// Strip protocol prefix if present
	urlStr := strings.TrimPrefix(c.DatabaseURL, "postgres://")
	urlStr = strings.TrimPrefix(urlStr, "postgresql://")

	// Parse credentials
	credentials, hostPortDB, found := strings.Cut(urlStr, "@")
	if !found {
		return fmt.Errorf("missing @ separator in database URL")
	}

	// Parse user and password
	c.DBUser, c.DBPassword, _ = strings.Cut(credentials, ":")

	// Parse host:port and dbname
	hostPort, dbName, hasDB := strings.Cut(hostPortDB, "/")
	if hasDB {
		// Strip query parameters if present
		c.DBName, _, _ = strings.Cut(dbName, "?")
	}

	// Parse host and optional port
	host, portStr, hasPort := strings.Cut(hostPort, ":")
	c.DBHost = host
	if hasPort {
		if port, err := strconv.Atoi(portStr); err == nil {
			c.DBPort = port
		}
	}

	return nil
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

	// Validate rate limit backend
	switch strings.ToLower(c.RateLimitBackend) {
	case "memory", "redis":
		// valid
	default:
		return fmt.Errorf("RATE_LIMIT_BACKEND must be 'memory' or 'redis', got %s", c.RateLimitBackend)
	}

	// Validate Redis configuration if using Redis backend
	if strings.ToLower(c.RateLimitBackend) == "redis" {
		if c.RedisHost == "" {
			return fmt.Errorf("REDIS_HOST cannot be empty when RATE_LIMIT_BACKEND is 'redis'")
		}
		if c.RedisPort < 1 || c.RedisPort > 65535 {
			return fmt.Errorf("REDIS_PORT must be between 1 and 65535, got %d", c.RedisPort)
		}
		if c.RedisPoolSize <= 0 {
			return fmt.Errorf("REDIS_POOL_SIZE must be greater than 0, got %d", c.RedisPoolSize)
		}
	}

	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
		// valid
	default:
		return fmt.Errorf("LOG_LEVEL must be one of [debug, info, warn, error], got %s", c.LogLevel)
	}

	// Validate database configuration if any database settings are present
	if c.DatabaseURL != "" || c.DBUser != "" || c.DBName != "" {
		if err := c.validateDatabase(); err != nil {
			return err
		}
	}

	return nil
}

// validateDatabase validates database configuration.
func (c *Config) validateDatabase() error {
	if c.DBHost == "" {
		return fmt.Errorf("DB_HOST cannot be empty when database is configured")
	}

	if c.DBPort < 1 || c.DBPort > 65535 {
		return fmt.Errorf("DB_PORT must be between 1 and 65535, got %d", c.DBPort)
	}

	if c.DBUser == "" {
		return fmt.Errorf("DB_USER cannot be empty when database is configured")
	}

	if c.DBName == "" {
		return fmt.Errorf("DB_NAME cannot be empty when database is configured")
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

// getEnvBool returns the boolean value of the environment variable key,
// or fallback if not set, empty, or not a valid boolean.
func getEnvBool(key string, fallback bool) bool {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}

// getEnvDuration returns the parsed time.Duration of the environment variable key,
// accepting duration strings (e.g., '5m', '30s') or integer values as seconds.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
		if seconds, err := strconv.Atoi(value); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}
	return fallback
}
