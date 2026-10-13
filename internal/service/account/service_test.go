package account

import (
	"context"
	"errors"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
)

type accountsStub struct {
	repository.AccountRepository
	accounts []entities.Account
	err      error
}

func (s accountsStub) FindOwned(_ context.Context, userID, accountID string) (entities.Account, error) {
	for _, a := range s.accounts {
		if a.ID == accountID && a.UserID == userID {
			return a, nil
		}
	}
	if s.err != nil {
		return entities.Account{}, s.err
	}
	return entities.Account{}, repository.ErrNotFound
}

func (s accountsStub) ListByUser(_ context.Context, userID string) ([]entities.Account, error) {
	var owned []entities.Account
	for _, a := range s.accounts {
		if a.UserID == userID {
			owned = append(owned, a)
		}
	}
	return owned, s.err
}

func TestAccountService(t *testing.T) {
	s := NewService(accountsStub{accounts: []entities.Account{
		{ID: "a-1", UserID: "alice", Balance: 10},
		{ID: "b-1", UserID: "bob", Balance: 20},
	}})

	list, err := s.List(context.Background(), "alice")
	if err != nil || len(list) != 1 || list[0].ID != "a-1" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if got, err := s.Get(context.Background(), "alice", "a-1"); err != nil || got.Balance != 10 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := s.Get(context.Background(), "alice", "b-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's account must look missing, got %v", err)
	}

	dbError := errors.New("boom")
	if _, err := NewService(accountsStub{err: dbError}).Get(context.Background(), "alice", "x"); !errors.Is(err, dbError) {
		t.Fatalf("expected database error, got %v", err)
	}
}
