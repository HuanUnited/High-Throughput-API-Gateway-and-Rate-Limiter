package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	clearAllEnv(t)

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "http://localhost:3000", cfg.UpstreamURL)
	assert.Equal(t, 10, cfg.RateLimitRPS)
	assert.Equal(t, 20, cfg.RateLimitBurst)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 300*time.Second, cfg.RateLimitCleanupInterval)
}

func TestLoad_DatabaseURLParsing(t *testing.T) {
	clearAllEnv(t)
	t.Setenv("DATABASE_URL", "postgres://testuser:testpass@dbhost:5433/customdb")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "testuser", cfg.DBUser)
	assert.Equal(t, "testpass", cfg.DBPassword)
	assert.Equal(t, "dbhost", cfg.DBHost)
	assert.Equal(t, 5433, cfg.DBPort)
	assert.Equal(t, "customdb", cfg.DBName)
}

func TestLoad_DatabaseURLInvalid(t *testing.T) {
	clearAllEnv(t)
	t.Setenv("DATABASE_URL", "invalid-no-at-symbol")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DATABASE_URL")
}

func TestLoad_DatabaseValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		expectedErr string
	}{
		{
			name: "missing host via database url",
			env: map[string]string{
				"DATABASE_URL": "postgres://user:password@/dbname",
			},
			expectedErr: "DB_HOST cannot be empty",
		},
		{
			name: "invalid port",
			env: map[string]string{
				"DB_HOST": "localhost",
				"DB_PORT": "70000",
				"DB_USER": "u",
				"DB_NAME": "n",
			},
			expectedErr: "DB_PORT must be between 1 and 65535",
		},
		{
			name: "missing user",
			env: map[string]string{
				"DB_HOST": "localhost",
				"DB_USER": "",
				"DB_NAME": "n",
			},
			expectedErr: "DB_USER cannot be empty",
		},
		{
			name: "missing name",
			env: map[string]string{
				"DB_HOST": "localhost",
				"DB_USER": "u",
				"DB_NAME": "",
			},
			expectedErr: "DB_NAME cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAllEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

func TestLoad_RedisBackendValidation(t *testing.T) {
	clearAllEnv(t)
	t.Setenv("RATE_LIMIT_BACKEND", "redis")
	t.Setenv("REDIS_HOST", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REDIS_HOST cannot be empty")
}

func TestLoad_DurationFormats(t *testing.T) {
	clearAllEnv(t)
	t.Setenv("RATE_LIMIT_CLEANUP_INTERVAL", "15m")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, cfg.RateLimitCleanupInterval)
}

func TestConfig_LoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("UPSTREAM_URL", "http://backend.internal:8080")
	t.Setenv("RATE_LIMIT_RPS", "50")
	t.Setenv("RATE_LIMIT_BURST", "100")
	t.Setenv("RATE_LIMIT_BACKEND", "redis")
	t.Setenv("REDIS_HOST", "127.0.0.1")
	t.Setenv("REDIS_PORT", "6379")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("METRICS_ENABLED", "true")
	t.Setenv("DATABASE_URL", "postgres://user:secret@localhost:5432/gateway_db?sslmode=disable")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "http://backend.internal:8080", cfg.UpstreamURL)
	assert.Equal(t, 50, cfg.RateLimitRPS)
	assert.Equal(t, 100, cfg.RateLimitBurst)
	assert.Equal(t, "redis", cfg.RateLimitBackend)
	assert.Equal(t, "127.0.0.1", cfg.RedisHost)
	assert.Equal(t, "user", cfg.DBUser)
	assert.Equal(t, "secret", cfg.DBPassword)
	assert.Equal(t, "gateway_db", cfg.DBName)
}

func TestConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"invalid port", map[string]string{"PORT": "70000"}},
		{"invalid upstream", map[string]string{"UPSTREAM_URL": "invalid-url"}},
		{"invalid rps", map[string]string{"RATE_LIMIT_RPS": "0"}},
		{"burst smaller than rps", map[string]string{"RATE_LIMIT_RPS": "50", "RATE_LIMIT_BURST": "20"}},
		{"invalid backend", map[string]string{"RATE_LIMIT_BACKEND": "consul"}},
		{"redis missing host", map[string]string{"RATE_LIMIT_BACKEND": "redis", "REDIS_HOST": ""}},
		{"invalid log level", map[string]string{"LOG_LEVEL": "trace"}},
		{"invalid database url", map[string]string{"DATABASE_URL": "invalid-db-format"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			assert.Error(t, err)
		})
	}
}

func TestConfig_EnvHelpers(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	t.Setenv("TEST_BOOL", "true")
	t.Setenv("TEST_DUR_STR", "10s")
	t.Setenv("TEST_DUR_INT", "15")

	assert.Equal(t, 42, getEnvInt("TEST_INT", 10))
	assert.Equal(t, 10, getEnvInt("TEST_INT_MISSING", 10))
	assert.True(t, getEnvBool("TEST_BOOL", false))
	assert.False(t, getEnvBool("TEST_BOOL_MISSING", false))
	assert.Equal(t, 10*time.Second, getEnvDuration("TEST_DUR_STR", time.Second))
	assert.Equal(t, 15*time.Second, getEnvDuration("TEST_DUR_INT", time.Second))
}

func clearAllEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PORT", "UPSTREAM_URL", "RATE_LIMIT_RPS", "RATE_LIMIT_BURST",
		"RATE_LIMIT_BACKEND", "RATE_LIMIT_CLEANUP_INTERVAL", "LOG_LEVEL",
		"DATABASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD",
		"DB_NAME", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}
