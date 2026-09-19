package repositories

import (
	"context"
	"errors"

	"bankmonitoring/internal/domain/entities"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key already used with a different request")
	ErrInvalidTransfer     = errors.New("invalid transfer request")
)

type TransferRepository interface {
	// CreateIdempotent returns the transfer and whether it was created now.
	// userID must come from the authenticated identity, not the request body.
	CreateIdempotent(ctx context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error)
}
