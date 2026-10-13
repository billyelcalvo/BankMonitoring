package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds the connection pool and pings PostgreSQL once so a bad
// DATABASE_URL fails at startup instead of on the first request.
func NewPool(ctx context.Context, connectionString string, maxConns int32) (*pgxpool.Pool, error) {
	if strings.TrimSpace(connectionString) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		// Parsing errors can contain credentials from the connection string.
		return nil, errors.New("DATABASE_URL is invalid")
	}
	if maxConns > 0 {
		config.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
