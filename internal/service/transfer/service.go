package transfer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
)

var ErrForbidden = errors.New("access to source account denied")

type Service struct {
	transfers repository.TransferRepository
	accounts  repository.AccountRepository
}

func NewService(transfers repository.TransferRepository, accounts repository.AccountRepository) *Service {
	return &Service{transfers: transfers, accounts: accounts}
}

// Create uses the verified JWT subject as userID. It authorizes every request,
// including retries, before reading or creating an idempotent transfer.
func (s *Service) Create(ctx context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	if strings.TrimSpace(userID) == "" {
		return entities.Transfer{}, false, ErrForbidden
	}
	if strings.TrimSpace(request.FromAccountID) == "" || strings.TrimSpace(request.ToAccountID) == "" ||
		request.FromAccountID == request.ToAccountID || request.Amount <= 0 || len(request.Currency) != 3 ||
		strings.Trim(request.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return entities.Transfer{}, false, repository.ErrInvalidTransfer
	}
	owned, err := s.accounts.IsOwner(ctx, userID, request.FromAccountID)
	if err != nil {
		return entities.Transfer{}, false, fmt.Errorf("check source account ownership: %w", err)
	}
	if !owned {
		return entities.Transfer{}, false, ErrForbidden
	}
	return s.transfers.CreateIdempotent(ctx, userID, key, request)
}
