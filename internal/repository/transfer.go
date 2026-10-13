package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TransferRepository struct {
	db querier
}

var _ domainrepository.TransferRepository = (*TransferRepository)(nil)

func NewTransferRepository(pool *pgxpool.Pool) *TransferRepository {
	return &TransferRepository{db: pool}
}

const transferColumns = `id::text, from_account_id, to_account_id, amount, currency,
	description, status, COALESCE(failure_reason, ''), created_at, updated_at,
	original_request, idempotency_key::text`

// CreateIdempotent stores the transfer with the outcome the service decided.
// The unique constraint on (user_id, idempotency_key) is the real arbiter:
// if two requests race with the same key, the second INSERT waits for the
// first transaction and then does nothing, so only one of them moves money.
func (r *TransferRepository) CreateIdempotent(ctx context.Context, t domainrepository.NewTransfer) (entities.Transfer, bool, error) {
	key, err := parseKey(t.IdempotencyKey)
	if err != nil {
		return entities.Transfer{}, false, err
	}
	payload, err := json.Marshal(t.Request)
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("encode transfer request: %w", err)
	}

	transfer, _, err := scanTransfer(r.db.QueryRow(ctx, `
		INSERT INTO transfers (user_id, idempotency_key, from_account_id, to_account_id,
			amount, currency, description, status, failure_reason, original_request)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING `+transferColumns,
		t.UserID, key, t.Request.FromAccountID, t.Request.ToAccountID, t.Request.Amount,
		t.Request.Currency, t.Request.Description, t.Status, t.FailureReason, payload))
	if err == nil {
		return transfer, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return entities.Transfer{}, false, fmt.Errorf("insert transfer: %w", err)
	}

	// The key is taken. A new statement gets a fresh snapshot under READ
	// COMMITTED, so it sees the row the other transaction just committed.
	transfer, original, err := scanTransfer(r.db.QueryRow(ctx, `SELECT `+transferColumns+`
		FROM transfers WHERE user_id = $1 AND idempotency_key = $2`, t.UserID, key))
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("find existing transfer: %w", err)
	}
	return replay(transfer, original, t.Request)
}

func (r *TransferRepository) FindByID(ctx context.Context, userID, transferID string) (entities.Transfer, error) {
	var id pgtype.UUID
	if err := id.Scan(transferID); err != nil || !id.Valid {
		return entities.Transfer{}, domainrepository.ErrNotFound
	}
	transfer, _, err := scanTransfer(r.db.QueryRow(ctx, `SELECT `+transferColumns+`
		FROM transfers WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entities.Transfer{}, domainrepository.ErrNotFound
	}
	if err != nil {
		return entities.Transfer{}, fmt.Errorf("find transfer: %w", err)
	}
	return transfer, nil
}

func parseKey(raw string) (pgtype.UUID, error) {
	var key pgtype.UUID
	if err := key.Scan(raw); err != nil || !key.Valid || key.Bytes == [16]byte{} {
		return key, fmt.Errorf("%w: idempotency key must be a nonzero UUID", entities.ErrInvalidTransfer)
	}
	return key, nil
}

func scanTransfer(row pgx.Row) (entities.Transfer, entities.CreateTransfer, error) {
	var transfer entities.Transfer
	var original entities.CreateTransfer
	var payload []byte
	err := row.Scan(&transfer.ID, &transfer.FromAccountID, &transfer.ToAccountID,
		&transfer.Amount, &transfer.Currency, &transfer.Description, &transfer.Status,
		&transfer.FailureReason, &transfer.CreatedAt, &transfer.UpdatedAt, &payload,
		&transfer.IdempotencyKey)
	if err != nil {
		return transfer, original, err
	}
	if err := json.Unmarshal(payload, &original); err != nil {
		return transfer, original, fmt.Errorf("decode original request: %w", err)
	}
	return transfer, original, nil
}

// replay compares field values, not raw JSON, so key order or whitespace in
// the retried body doesn't matter.
func replay(transfer entities.Transfer, original, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	if original != request {
		return entities.Transfer{}, false, domainrepository.ErrIdempotencyConflict
	}
	return transfer, false, nil
}
