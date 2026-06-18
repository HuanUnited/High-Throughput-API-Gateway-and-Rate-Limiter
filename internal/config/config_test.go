package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear env variables to test defaults
	t.Setenv("PORT", "")
	t.Setenv("UPSTREAM_URL", "")
	t.Setenv("RATE_LIMIT_RPS", "")
	t.Setenv("RATE_LIMIT_BURST", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "http://localhost:3000", cfg.UpstreamURL)
	assert.Equal(t, 10, cfg.RateLimitRPS)
	assert.Equal(t, 20, cfg.RateLimitBurst)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_WithEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("UPSTREAM_URL", "https://api.example.com")
	t.Setenv("RATE_LIMIT_RPS", "50")
	t.Setenv("RATE_LIMIT_BURST", "100")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "https://api.example.com", cfg.UpstreamURL)
	assert.Equal(t, 50, cfg.RateLimitRPS)
	assert.Equal(t, 100, cfg.RateLimitBurst)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_InvalidConfig(t *testing.T) {
	testCases := []struct {
		name        string
		env         map[string]string
		expectedErr string
	}{
		{
			name: "invalid port",
			env: map[string]string{
				"PORT": "70000",
			},
			expectedErr: "PORT must be between 1 and 65535",
		},
		{
			name: "invalid upstream URL",
			env: map[string]string{
				"UPSTREAM_URL": "invalid-url",
			},
			expectedErr: "UPSTREAM_URL must start with http:// or https://",
		},
		{
			name: "zero RPS",
			env: map[string]string{
				"RATE_LIMIT_RPS": "0",
			},
			expectedErr: "RATE_LIMIT_RPS must be greater than 0",
		},
		{
			name: "zero burst",
			env: map[string]string{
				"RATE_LIMIT_BURST": "0",
			},
			expectedErr: "RATE_LIMIT_BURST must be greater than 0",
		},
		{
			name: "burst less than RPS",
			env: map[string]string{
				"RATE_LIMIT_RPS":   "20",
				"RATE_LIMIT_BURST": "10",
			},
			expectedErr: "must be greater than or equal to",
		},
		{
			name: "invalid log level",
			env: map[string]string{
				"LOG_LEVEL": "verbose",
			},
			expectedErr: "LOG_LEVEL must be one of",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear all config env vars first
			for _, key := range []string{
				"PORT", "UPSTREAM_URL", "RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "LOG_LEVEL",
			} {
				t.Setenv(key, "")
			}

			// Set test case env
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestLoad_InvalidIntEnvFallsBack(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	t.Setenv("RATE_LIMIT_RPS", "abc")

	cfg, err := Load()
	require.NoError(t, err)

	// Should fall back to defaults
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, 10, cfg.RateLimitRPS)
}
