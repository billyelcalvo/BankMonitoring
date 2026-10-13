package account

import (
	"context"
	"errors"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
)

var ErrNotFound = errors.New("account not found")

type Service struct {
	accounts repository.AccountRepository
}

func NewService(accounts repository.AccountRepository) *Service {
	return &Service{accounts: accounts}
}

func (s *Service) List(ctx context.Context, userID string) ([]entities.Account, error) {
	return s.accounts.ListByUser(ctx, userID)
}

// Get answers "not found" for accounts owned by someone else as well, so the
// endpoint can't be used to discover which account IDs exist.
func (s *Service) Get(ctx context.Context, userID, accountID string) (entities.Account, error) {
	account, err := s.accounts.FindOwned(ctx, userID, accountID)
	if errors.Is(err, repository.ErrNotFound) {
		return entities.Account{}, ErrNotFound
	}
	return account, err
}
