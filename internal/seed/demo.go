// Package seed provisions demo users and accounts so a fresh deployment can
// be tried out right away. It only runs when SEED_DEMO_DATA=true.
package seed

import (
	"context"
	"fmt"

	"bankmonitoring/internal/service/auth"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type demoAccount struct {
	id, currency string
	balance      int64
}

type demoUser struct {
	id, email string
	accounts  []demoAccount
}

// Opening balances in cents: 1,000,000.00 each, enough for long load tests.
var demoUsers = []demoUser{
	{"user-alice", "alice@example.com", []demoAccount{{"acc-alice-pen", "PEN", 100_000_000}, {"acc-alice-usd", "USD", 100_000_000}}},
	{"user-bob", "bob@example.com", []demoAccount{{"acc-bob-pen", "PEN", 100_000_000}, {"acc-bob-usd", "USD", 100_000_000}}},
	{"user-carol", "carol@example.com", []demoAccount{{"acc-carol-pen", "PEN", 0}}},
}

var demoPermissions = []string{
	string(auth.PermissionAccountsRead),
	string(auth.PermissionTransfersRead),
	string(auth.PermissionTransfersCreate),
}

// Demo is safe to run on every start: users get their password refreshed
// from DEMO_PASSWORD, but existing accounts keep their current balance.
func Demo(ctx context.Context, pool *pgxpool.Pool, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash demo password: %w", err)
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, u := range demoUsers {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, password_hash, permissions)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (id) DO UPDATE SET password_hash = EXCLUDED.password_hash,
					permissions = EXCLUDED.permissions`,
				u.id, u.email, hash, demoPermissions); err != nil {
				return fmt.Errorf("seed user %s: %w", u.id, err)
			}
			for _, a := range u.accounts {
				if _, err := tx.Exec(ctx, `INSERT INTO accounts (id, user_id, currency, balance)
					VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING`,
					a.id, u.id, a.currency, a.balance); err != nil {
					return fmt.Errorf("seed account %s: %w", a.id, err)
				}
			}
		}
		return nil
	})
}
