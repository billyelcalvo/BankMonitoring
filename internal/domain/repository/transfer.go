package repository

import (
	"context"
	"errors"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/valueobjects"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key already used with a different request")
	ErrNotFound            = errors.New("not found")
	// ErrInvalidTransfer is kept while the HTTP layer moves to the entity error.
	ErrInvalidTransfer = entities.ErrInvalidTransfer
)

// NewTransfer is a transfer whose outcome the service has already decided.
type NewTransfer struct {
	// UserID must come from the authenticated identity, not the request body.
	UserID         string
	IdempotencyKey string
	Request        entities.CreateTransfer
	Status         valueobjects.TransferStatus
	FailureReason  valueobjects.FailureReason
}

type TransferRepository interface {
	// CreateIdempotent returns the transfer and whether it was created now.
	// When the key was already used, it returns the stored transfer untouched,
	// or ErrIdempotencyConflict if the original request was different.
	CreateIdempotent(ctx context.Context, transfer NewTransfer) (entities.Transfer, bool, error)
	// FindByID only returns transfers created by userID.
	FindByID(ctx context.Context, userID, transferID string) (entities.Transfer, error)
}
