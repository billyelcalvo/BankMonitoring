package repository

import (
	"context"

	"bankmonitoring/internal/domain/entities"
)

type AccountRepository interface {
	// LockForTransfer reads the accounts with SELECT ... FOR UPDATE, always in
	// ascending ID order so two opposite transfers can't deadlock each other.
	// Missing accounts are simply absent from the map.
	LockForTransfer(ctx context.Context, accountIDs ...string) (map[string]*entities.Account, error)
	// SaveBalances writes balances of accounts previously locked in the same transaction.
	SaveBalances(ctx context.Context, accounts ...*entities.Account) error
	// FindOwned returns ErrNotFound when the account doesn't exist or belongs to someone else.
	FindOwned(ctx context.Context, userID, accountID string) (entities.Account, error)
	ListByUser(ctx context.Context, userID string) ([]entities.Account, error)
}

type LedgerRepository interface {
	Append(ctx context.Context, entries ...entities.LedgerEntry) error
}
