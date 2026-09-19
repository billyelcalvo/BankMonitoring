package repository

import "context"

type AccountRepository interface {
	IsOwner(ctx context.Context, userID, accountID string) (bool, error)
}
