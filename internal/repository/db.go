package repository

import (
	"context"

	domainrepository "bankmonitoring/internal/domain/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is what both *pgxpool.Pool and pgx.Tx offer, so every repository
// works the same inside or outside a transaction.
type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Store is the transaction manager backed by a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

var _ domainrepository.TxManager = (*Store)(nil)

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// WithinTx uses READ COMMITTED on purpose: correctness comes from the row
// locks taken by LockForTransfer, not from the isolation level, and it
// avoids the serialization-failure retries SERIALIZABLE would force on us.
func (s *Store) WithinTx(ctx context.Context, fn func(domainrepository.Tx) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(txRepositories{db: tx})
	})
}

type txRepositories struct {
	db querier
}

func (r txRepositories) Accounts() domainrepository.AccountRepository {
	return &AccountRepository{db: r.db}
}

func (r txRepositories) Transfers() domainrepository.TransferRepository {
	return &TransferRepository{db: r.db}
}

func (r txRepositories) Ledger() domainrepository.LedgerRepository {
	return &LedgerRepository{db: r.db}
}
