package repository

import (
	"context"

	"bankmonitoring/internal/domain/entities"
)

type UserRepository interface {
	// FindByEmail matches case-insensitively and returns ErrNotFound if there's no such user.
	FindByEmail(ctx context.Context, email string) (entities.User, error)
}
