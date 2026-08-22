// Package storage manages PostgreSQL persistence for clients.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq" // register postgres driver
)

// ErrNotFound indicates a requested client record does not exist.
var ErrNotFound = errors.New("client not found")
var ErrInvalidAPIKey = errors.New("invalid api key")

// Postgres wraps a PostgreSQL database connection pool.
type Postgres struct {
	db *sql.DB
}

// PostgresConfig specifies parameters for connecting to PostgreSQL.
type PostgresConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// NewPostgres connects and pings the PostgreSQL database.
func NewPostgres(cfg PostgresConfig) (*Postgres, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid postgres config: %w", err)
	}

	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open database connection: %w", err)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if dbErr := db.PingContext(ctx); dbErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Postgres{db: db}, nil
}

// Close terminates the PostgreSQL connection pool.
func (p *Postgres) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

// GetClientLimit returns the rate limit associated with an API key.
func (p *Postgres) GetClientLimit(ctx context.Context, apiKey string) (int, error) {
	if apiKey == "" {
		return 0, ErrNotFound
	}

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

// UpsertClient inserts or updates a client rate limit.
func (p *Postgres) UpsertClient(ctx context.Context, apiKey string, limit int) error {
	const query = `
		INSERT INTO clients (api_key, rate_limit)
		VALUES ($1, $2)
		ON CONFLICT (api_key) DO UPDATE SET rate_limit = EXCLUDED.rate_limit, updated_at = NOW();`
	_, err := p.db.ExecContext(ctx, query, apiKey, limit)
	return err
}

// DeleteClient removes a client by API key.
func (p *Postgres) DeleteClient(ctx context.Context, apiKey string) error {
	const query = `DELETE FROM clients WHERE api_key = $1;`
	_, err := p.db.ExecContext(ctx, query, apiKey)
	return err
}

// GetClientLimitPrepared fetches client limits using a prepared statement query.
func (p *Postgres) GetClientLimitPrepared(ctx context.Context, apiKey string) (int, error) {
	return p.GetClientLimit(ctx, apiKey)
}

// HealthCheck verifies the database connection is responsive.
func (p *Postgres) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := p.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check: %w", err)
	}

	return nil
}

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
