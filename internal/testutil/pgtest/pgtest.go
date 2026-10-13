//go:build integration

// Package pgtest starts a throwaway PostgreSQL in Docker for integration
// tests. Each test package gets its own container and a fully migrated schema.
package pgtest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"bankmonitoring/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:17-alpine"

// Start launches the container, applies the migrations and returns the URL.
// Call it from TestMain; stop() must run before os.Exit.
func Start() (url string, stop func(), err error) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("bank"),
		postgres.WithUsername("bank"),
		postgres.WithPassword("bank"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", nil, fmt.Errorf("start postgres: %w", err)
	}
	stop = func() { _ = testcontainers.TerminateContainer(container) }

	url, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		stop()
		return "", nil, err
	}
	if _, err := repository.Migrate(url); err != nil {
		stop()
		return "", nil, err
	}
	return url, stop, nil
}

// Main is a ready-made TestMain body: it starts PostgreSQL, stores the URL in
// *url and runs the tests.
func Main(m *testing.M, url *string) {
	u, stop, err := Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	*url = u
	code := m.Run()
	stop()
	os.Exit(code)
}

// Pool opens a pool and wipes every table so each test starts from scratch.
func Pool(t testing.TB, url string, maxConns int32) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := repository.NewPool(ctx, url, maxConns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE ledger_entries, transfers, accounts, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// CreateUser inserts a user with the given accounts and opening balances.
func CreateUser(t testing.TB, pool *pgxpool.Pool, userID string, balances map[string]int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, permissions)
		VALUES ($1, $1 || '@example.com', 'unused', '{}')`, userID); err != nil {
		t.Fatal(err)
	}
	for id, balance := range balances {
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, user_id, currency, balance)
			VALUES ($1, $2, 'PEN', $3)`, id, userID, balance); err != nil {
			t.Fatal(err)
		}
	}
}

// Balance reads an account balance straight from the table.
func Balance(t testing.TB, pool *pgxpool.Pool, accountID string) int64 {
	t.Helper()
	var balance int64
	if err := pool.QueryRow(context.Background(), `SELECT balance FROM accounts WHERE id = $1`, accountID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	return balance
}
