package transfer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"
)

var (
	ErrForbidden           = errors.New("access to source account denied")
	ErrDestinationNotFound = errors.New("destination account not found")
	ErrCurrencyMismatch    = errors.New("transfer currency does not match the accounts")
	ErrNotFound            = errors.New("transfer not found")
)

type Service struct {
	store repository.TxManager
	reads repository.TransferRepository
}

// NewService takes the transaction manager for writes and a plain repository
// for reads, which don't need a transaction.
func NewService(store repository.TxManager, reads repository.TransferRepository) *Service {
	return &Service{store: store, reads: reads}
}

// Create moves money between two accounts exactly once per idempotency key.
//
// Everything happens in a single transaction:
//  1. lock both accounts (SELECT ... FOR UPDATE, ordered by id),
//  2. check ownership, currency and balance against the locked rows,
//  3. insert the transfer; the unique (user_id, key) constraint decides
//     whether this request is new or a retry,
//  4. only if it's new and accepted, write the balances and the ledger.
//
// Because the balance check runs on locked rows, two concurrent requests
// can't both see the same funds. A request with insufficient funds is still
// stored as failed, so retrying it returns the same answer.
func (s *Service) Create(ctx context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	if strings.TrimSpace(userID) == "" {
		return entities.Transfer{}, false, ErrForbidden
	}
	if err := request.Validate(); err != nil {
		return entities.Transfer{}, false, err
	}

	var result entities.Transfer
	var created bool
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		accounts, err := tx.Accounts().LockForTransfer(ctx, request.FromAccountID, request.ToAccountID)
		if err != nil {
			return err
		}
		// A missing source and someone else's source look the same from the
		// outside, so callers can't probe which account IDs exist.
		source, ok := accounts[request.FromAccountID]
		if !ok || source.UserID != userID {
			return ErrForbidden
		}
		destination, ok := accounts[request.ToAccountID]
		if !ok {
			return ErrDestinationNotFound
		}
		if source.Currency != request.Currency || destination.Currency != request.Currency {
			return ErrCurrencyMismatch
		}

		draft := repository.NewTransfer{
			UserID: userID, IdempotencyKey: key, Request: request,
			Status: valueobjects.TransferStatusCompleted,
		}
		switch err := source.Debit(request.Amount); {
		case errors.Is(err, entities.ErrInsufficientFunds):
			draft.Status, draft.FailureReason = valueobjects.TransferStatusFailed, valueobjects.FailureReasonInsufficientFunds
		case err != nil:
			return err
		default:
			if err := destination.Credit(request.Amount); err != nil {
				return err
			}
		}

		result, created, err = tx.Transfers().CreateIdempotent(ctx, draft)
		if err != nil {
			return err
		}
		if !created || result.Status != valueobjects.TransferStatusCompleted {
			// A retry or a rejected transfer: nothing to move. The debit above
			// only touched the in-memory copy, so we simply don't save it.
			return nil
		}
		if err := tx.Accounts().SaveBalances(ctx, source, destination); err != nil {
			return err
		}
		return tx.Ledger().Append(ctx,
			entities.LedgerEntry{TransferID: result.ID, AccountID: source.ID, Amount: -request.Amount, BalanceAfter: source.Balance},
			entities.LedgerEntry{TransferID: result.ID, AccountID: destination.ID, Amount: request.Amount, BalanceAfter: destination.Balance},
		)
	})
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("create transfer: %w", err)
	}
	return result, created, nil
}

// Get returns a transfer only to the user who created it.
func (s *Service) Get(ctx context.Context, userID, transferID string) (entities.Transfer, error) {
	transfer, err := s.reads.FindByID(ctx, userID, transferID)
	if errors.Is(err, repository.ErrNotFound) {
		return entities.Transfer{}, ErrNotFound
	}
	return transfer, err
}
