// Package storage provides PostgreSQL-backed persistence for the gateway.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// Errors returned by the storage layer.
var (
	ErrNotFound      = errors.New("client not found")
	ErrInvalidAPIKey = errors.New("invalid API key format")
)

// Postgres handles PostgreSQL database operations.
type Postgres struct {
	db *sql.DB
}

// PostgresConfig holds PostgreSQL connection configuration.
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string

	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// NewPostgres creates a new PostgreSQL storage instance.
// It establishes a connection pool and verifies connectivity.
func NewPostgres(cfg PostgresConfig) (*Postgres, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid postgres config: %w", err)
	}

	// Build connection string
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open database connection: %w", err)
	}

	// Configure connection pool
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	// Verify connection with retry
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Postgres{db: db}, nil
}

// Close closes the database connection pool.
func (p *Postgres) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

// GetClientLimit retrieves the rate limit for a given API key.
// It returns the limit and any error encountered.
func (p *Postgres) GetClientLimit(ctx context.Context, apiKey string) (int, error) {
	if apiKey == "" {
		return 0, ErrInvalidAPIKey
	}

	// Use a prepared statement for efficiency and SQL injection prevention
	const query = `
		SELECT rate_limit
		FROM clients
		WHERE api_key = $1
	`

	var limit int
	err := p.db.QueryRowContext(ctx, query, apiKey).Scan(&limit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("query client limit: %w", err)
	}

	if limit <= 0 {
		return 0, fmt.Errorf("invalid rate limit value %d for client %s", limit, apiKey)
	}

	return limit, nil
}

// GetClientLimitPrepared demonstrates prepared statement usage
// for high-throughput scenarios (useful for performance optimization).
func (p *Postgres) GetClientLimitPrepared(ctx context.Context, apiKey string) (int, error) {
	// Prepare statement (typically done once at startup)
	stmt, err := p.db.PrepareContext(ctx, `
		SELECT rate_limit
		FROM clients
		WHERE api_key = $1
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	var limit int
	err = stmt.QueryRowContext(ctx, apiKey).Scan(&limit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("execute prepared statement: %w", err)
	}

	return limit, nil
}

// UpsertClient creates or updates a client's rate limit.
// This is useful for admin/management endpoints.
func (p *Postgres) UpsertClient(ctx context.Context, apiKey string, rateLimit int) error {
	if apiKey == "" {
		return ErrInvalidAPIKey
	}
	if rateLimit <= 0 {
		return fmt.Errorf("rate limit must be positive, got %d", rateLimit)
	}

	const query = `
		INSERT INTO clients (api_key, rate_limit)
		VALUES ($1, $2)
		ON CONFLICT (api_key)
		DO UPDATE SET rate_limit = EXCLUDED.rate_limit, updated_at = NOW()
	`

	_, err := p.db.ExecContext(ctx, query, apiKey, rateLimit)
	if err != nil {
		return fmt.Errorf("upsert client: %w", err)
	}

	return nil
}

// DeleteClient removes a client by API key.
func (p *Postgres) DeleteClient(ctx context.Context, apiKey string) error {
	const query = `DELETE FROM clients WHERE api_key = $1`

	result, err := p.db.ExecContext(ctx, query, apiKey)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// CountClients returns the total number of clients in the database.
func (p *Postgres) CountClients(ctx context.Context) (int, error) {
	const query = `SELECT COUNT(*) FROM clients`

	var count int
	err := p.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count clients: %w", err)
	}

	return count, nil
}

// HealthCheck verifies database connectivity.
func (p *Postgres) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := p.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check: %w", err)
	}

	return nil
}

// validateConfig ensures required configuration is present.
func validateConfig(cfg PostgresConfig) error {
	if cfg.Host == "" {
		return errors.New("host cannot be empty")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("invalid port %d", cfg.Port)
	}
	if cfg.User == "" {
		return errors.New("user cannot be empty")
	}
	if cfg.Database == "" {
		return errors.New("database cannot be empty")
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}
	return nil
}
