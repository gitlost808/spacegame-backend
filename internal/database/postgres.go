package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a PostgreSQL connection pool and verifies connectivity.
// The caller must close the pool when it is no longer needed.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	if err := ValidateSSLMode(databaseURL); err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return pool, nil
}

// ValidateSSLMode requires production URLs to make the transport policy explicit.
// pgx validates the selected mode and configures TLS for server and migration clients.
func ValidateSSLMode(databaseURL string) error {
	if os.Getenv("APP_ENV") != "production" {
		return nil
	}
	connectionURL, err := url.Parse(databaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	modes := connectionURL.Query()["sslmode"]
	if (connectionURL.Scheme != "postgres" && connectionURL.Scheme != "postgresql") || len(modes) != 1 || strings.TrimSpace(modes[0]) == "" {
		return errors.New("production requires one explicit sslmode in the DATABASE_URL PostgreSQL URI")
	}
	return nil
}
