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
