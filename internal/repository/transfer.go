package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repositories"
	"bankmonitoring/internal/domain/valueobjects"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type TransferRepository struct {
	db rowQuerier
}

var _ repositories.TransferRepository = (*TransferRepository)(nil)

func NewTransferRepository(pool *pgxpool.Pool) *TransferRepository {
	return &TransferRepository{db: pool}
}

const transferColumns = `id::text, from_account_id, to_account_id, amount,
	currency, description, status, created_at, updated_at, original_request`

// CreateIdempotent persists a pending transfer, not a movement of funds.
// The unique constraint on (user_id, idempotency_key) arbitrates concurrent
// requests. The original request remains unchanged even if the status changes.
func (r *TransferRepository) CreateIdempotent(ctx context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	var uuid pgtype.UUID
	if err := uuid.Scan(key); err != nil || !uuid.Valid || uuid.Bytes == [16]byte{} {
		return entities.Transfer{}, false, fmt.Errorf("%w: idempotency key must be a nonzero UUID", repositories.ErrInvalidTransfer)
	}
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(request.FromAccountID) == "" ||
		strings.TrimSpace(request.ToAccountID) == "" || request.FromAccountID == request.ToAccountID || request.Amount <= 0 ||
		len(request.Currency) != 3 || strings.Trim(request.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return entities.Transfer{}, false, repositories.ErrInvalidTransfer
	}

	transfer, original, err := r.find(ctx, userID, uuid)
	if err == nil {
		return replay(transfer, original, request)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return entities.Transfer{}, false, fmt.Errorf("find transfer: %w", err)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("encode transfer request: %w", err)
	}
	transfer, _, err = scanTransfer(r.db.QueryRow(ctx, `
		INSERT INTO transfers (user_id, idempotency_key, from_account_id,
			to_account_id, amount, currency, description, status, original_request)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING `+transferColumns,
		userID, uuid, request.FromAccountID, request.ToAccountID, request.Amount,
		request.Currency, request.Description, valueobjects.TransferStatusPending, payload))
	if err == nil {
		return transfer, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return entities.Transfer{}, false, fmt.Errorf("insert transfer: %w", err)
	}

	// A competing insert won. A separate statement sees its committed row,
	// unlike a SELECT sharing the INSERT statement's original snapshot.
	transfer, original, err = r.find(ctx, userID, uuid)
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("find concurrent transfer: %w", err)
	}
	return replay(transfer, original, request)
}

func (r *TransferRepository) find(ctx context.Context, userID string, key pgtype.UUID) (entities.Transfer, entities.CreateTransfer, error) {
	return scanTransfer(r.db.QueryRow(ctx, `SELECT `+transferColumns+`
		FROM transfers WHERE user_id = $1 AND idempotency_key = $2`, userID, key))
}

func scanTransfer(row pgx.Row) (entities.Transfer, entities.CreateTransfer, error) {
	var transfer entities.Transfer
	var original entities.CreateTransfer
	var payload []byte
	err := row.Scan(&transfer.ID, &transfer.FromAccountID, &transfer.ToAccountID,
		&transfer.Amount, &transfer.Currency, &transfer.Description, &transfer.Status,
		&transfer.CreatedAt, &transfer.UpdatedAt, &payload)
	if err != nil {
		return transfer, original, err
	}
	if err := json.Unmarshal(payload, &original); err != nil {
		return transfer, original, fmt.Errorf("decode original request: %w", err)
	}
	return transfer, original, nil
}

func replay(transfer entities.Transfer, original, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	if original != request {
		return entities.Transfer{}, false, repositories.ErrIdempotencyConflict
	}
	return transfer, false, nil
}
