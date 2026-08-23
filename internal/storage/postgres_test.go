package storage

import (
	"context"
	"os"
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
	defer func(store *Postgres) {
		err := store.Close()
		if err != nil {
			panic(err)
		}
	}(store)

	ctx := context.Background()

	// Setup test data
	testAPIKey := "test-api-key"
	testLimit := 42
	err = store.UpsertClient(ctx, testAPIKey, testLimit)
	require.NoError(t, err)

	// Test successful lookup
	t.Run("existing client", func(t *testing.T) {
		limit, err := store.GetClientLimit(ctx, testAPIKey)
		require.NoError(t, err)
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
	defer func(store *Postgres) {
		err := store.Close()
		if err != nil {
			panic(err)
		}
	}(store)

	ctx := context.Background()

	// Insert test data
	apiKey := "prepared-stmt-test"
	err = store.UpsertClient(ctx, apiKey, 77)
	require.NoError(t, err)

	// Test prepared statement
	limit, err := store.GetClientLimitPrepared(ctx, apiKey)
	require.NoError(t, err)
	assert.Equal(t, 77, limit)

	// Cleanup
	_ = store.DeleteClient(ctx, apiKey)
}

// getTestDSN returns the test database connection string from env
func getTestDSN(t *testing.T) string {
	t.Helper()
	_ = t
	return os.Getenv("TEST_DATABASE_URL")
}
