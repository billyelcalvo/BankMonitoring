package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountRepository struct {
	db querier
}

var _ domainrepository.AccountRepository = (*AccountRepository)(nil)

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{db: pool}
}

const accountColumns = `id, user_id, currency, balance, created_at, updated_at`

func (r *AccountRepository) LockForTransfer(ctx context.Context, accountIDs ...string) (map[string]*entities.Account, error) {
	ids := slices.Clone(accountIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)

	// ORDER BY id makes PostgreSQL acquire the row locks in the same order for
	// every transaction, which is what keeps A->B and B->A from deadlocking.
	rows, err := r.db.Query(ctx, `SELECT `+accountColumns+`
		FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("lock accounts: %w", err)
	}
	accounts, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("lock accounts: %w", err)
	}
	locked := make(map[string]*entities.Account, len(accounts))
	for i := range accounts {
		locked[accounts[i].ID] = &accounts[i]
	}
	return locked, nil
}

func (r *AccountRepository) SaveBalances(ctx context.Context, accounts ...*entities.Account) error {
	ids := make([]string, len(accounts))
	balances := make([]int64, len(accounts))
	for i, account := range accounts {
		ids[i], balances[i] = account.ID, account.Balance
	}
	tag, err := r.db.Exec(ctx, `UPDATE accounts AS a
		SET balance = v.balance, updated_at = now()
		FROM unnest($1::text[], $2::bigint[]) AS v(id, balance)
		WHERE a.id = v.id`, ids, balances)
	if err != nil {
		return fmt.Errorf("save balances: %w", err)
	}
	if tag.RowsAffected() != int64(len(accounts)) {
		return fmt.Errorf("save balances: updated %d of %d accounts", tag.RowsAffected(), len(accounts))
	}
	return nil
}

func (r *AccountRepository) FindOwned(ctx context.Context, userID, accountID string) (entities.Account, error) {
	rows, err := r.db.Query(ctx, `SELECT `+accountColumns+`
		FROM accounts WHERE id = $1 AND user_id = $2`, accountID, userID)
	if err != nil {
		return entities.Account{}, fmt.Errorf("find account: %w", err)
	}
	account, err := pgx.CollectExactlyOneRow(rows, scanAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return entities.Account{}, domainrepository.ErrNotFound
	}
	if err != nil {
		return entities.Account{}, fmt.Errorf("find account: %w", err)
	}
	return account, nil
}

func (r *AccountRepository) ListByUser(ctx context.Context, userID string) ([]entities.Account, error) {
	rows, err := r.db.Query(ctx, `SELECT `+accountColumns+`
		FROM accounts WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accounts, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

func scanAccount(row pgx.CollectableRow) (entities.Account, error) {
	var a entities.Account
	err := row.Scan(&a.ID, &a.UserID, &a.Currency, &a.Balance, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}
