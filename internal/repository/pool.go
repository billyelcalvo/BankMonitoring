package repository

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool reads the connection string from the process environment.
// It does not ping PostgreSQL; connections are acquired when queries run.
func NewPool(ctx context.Context) (*pgxpool.Pool, error) {
	connectionString := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(connectionString) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		// Parsing errors can contain credentials from the connection string.
		return nil, errors.New("DATABASE_URL is invalid")
	}
	return pgxpool.NewWithConfig(ctx, config)
}
