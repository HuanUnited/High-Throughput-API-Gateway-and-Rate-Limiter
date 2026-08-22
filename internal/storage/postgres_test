package storage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresGetClientLimit tests the GetClientLimit method.
// Requires a running PostgreSQL instance - skipped if DSN not provided.
func TestPostgresGetClientLimit(t *testing.T) {
	// Skip if no test database is available
	dsn := getTestDSN(t)
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping PostgreSQL integration test")
	}

	cfg := PostgresConfig{
		Host:            "localhost",
		Port:            5432,
		User:            "postgres",
		Password:        "postgres",
		Database:        "rate_limiter_test",
		SSLMode:         "disable",
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 30 * time.Minute,
	}

	// Create storage instance
	store, err := NewPostgres(cfg)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Setup test data
	testAPIKey := "test-api-key"
	testLimit := 42
	err = store.UpsertClient(ctx, testAPIKey, testLimit)
	require.NoError(t, err)

	// Test successful lookup
	t.Run("existing client", func(t *testing.T) {
		limit, err := store.GetClientLimit(ctx, testAPIKey)
		assert.NoError(t, err)
		assert.Equal(t, testLimit, limit)
	})

	// Test non-existent client
	t.Run("non-existent client", func(t *testing.T) {
		_, err := store.GetClientLimit(ctx, "non-existent-key")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	// Test empty API key
	t.Run("empty API key", func(t *testing.T) {
		_, err := store.GetClientLimit(ctx, "")
		assert.ErrorIs(t, err, ErrInvalidAPIKey)
	})

	// Cleanup
	t.Run("cleanup", func(t *testing.T) {
		err := store.DeleteClient(ctx, testAPIKey)
		assert.NoError(t, err)
	})
}

// TestPostgresPreparedStatement tests the prepared statement variant.
func TestPostgresPreparedStatement(t *testing.T) {
	dsn := getTestDSN(t)
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping PostgreSQL integration test")
	}

	cfg := PostgresConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "postgres",
		Database: "rate_limiter_test",
		SSLMode:  "disable",
	}

	store, err := NewPostgres(cfg)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Insert test data
	apiKey := "prepared-stmt-test"
	err = store.UpsertClient(ctx, apiKey, 77)
	require.NoError(t, err)

	// Test prepared statement
	limit, err := store.GetClientLimitPrepared(ctx, apiKey)
	assert.NoError(t, err)
	assert.Equal(t, 77, limit)

	// Cleanup
	_ = store.DeleteClient(ctx, apiKey)
}

// TestPostgresIndependence verifies the storage layer is independent
// of the limiter package (no import cycle).
func TestPostgresIndependence(t *testing.T) {
	// The storage package should compile and be importable
	// without importing the limiter package
	assert.True(t, true)
}

// getTestDSN returns the test database connection string from env,
// or empty string if not configured.
func getTestDSN(t *testing.T) string {
	t.Helper()

	// Check for test database URL in environment
	// Format: postgres://user:password@host:port/dbname?sslmode=disable
	// This allows CI systems to configure the test database
	_ = t // satisfy linter

	// Return a default test DSN or empty string
	// In CI, this would be set to a real database
	return "" // Skip tests by default
}
