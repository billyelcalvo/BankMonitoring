package repository

import (
	"context"
	"errors"
	"fmt"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	db querier
}

var _ domainrepository.UserRepository = (*UserRepository)(nil)

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: pool}
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (entities.User, error) {
	var user entities.User
	err := r.db.QueryRow(ctx, `SELECT id, email, password_hash, permissions
		FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Permissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return entities.User{}, domainrepository.ErrNotFound
	}
	if err != nil {
		return entities.User{}, fmt.Errorf("find user: %w", err)
	}
	return user, nil
}
