package repository

import (
	"context"

	"bankmonitoring/internal/domain/repositories"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountRepository struct {
	db rowQuerier
}

var _ repositories.AccountRepository = (*AccountRepository)(nil)

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{db: pool}
}

func (r *AccountRepository) IsOwner(ctx context.Context, userID, accountID string) (bool, error) {
	var owned bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM accounts WHERE id = $1 AND user_id = $2
	)`, accountID, userID).Scan(&owned)
	return owned, err
}
