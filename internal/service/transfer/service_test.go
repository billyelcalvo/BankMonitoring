package transfer

import (
	"context"
	"errors"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
)

type accountStub struct {
	userID, accountID string
	owned             bool
	err               error
}

func (s *accountStub) IsOwner(_ context.Context, userID, accountID string) (bool, error) {
	s.userID, s.accountID = userID, accountID
	return s.owned, s.err
}

type transferStub struct {
	called      bool
	userID, key string
	request     entities.CreateTransfer
	created     bool
	err         error
}

func (s *transferStub) CreateIdempotent(_ context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	s.called, s.userID, s.key, s.request = true, userID, key, request
	return entities.Transfer{ID: "transfer-1", IdempotencyKey: key}, s.created, s.err
}

func TestCreateRequiresOwnership(t *testing.T) {
	dbError := errors.New("database unavailable")
	request := entities.CreateTransfer{FromAccountID: "account-1", ToAccountID: "account-2", Amount: 100, Currency: "PEN"}
	for _, tc := range []struct {
		name                         string
		owned, created               bool
		accountErr, repoErr, wantErr error
	}{
		{"owner", true, true, nil, nil, nil},
		{"owner replay", true, false, nil, nil, nil},
		{"not owner", false, false, nil, nil, ErrForbidden},
		{"ownership unavailable", false, false, dbError, nil, dbError},
		{"conflicting retry", true, false, nil, repository.ErrIdempotencyConflict, repository.ErrIdempotencyConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &accountStub{owned: tc.owned, err: tc.accountErr}
			transfers := &transferStub{created: tc.created, err: tc.repoErr}
			s := NewService(transfers, accounts)
			result, created, err := s.Create(context.Background(), "verified-user", "key", request)
			if !errors.Is(err, tc.wantErr) || created != tc.created {
				t.Fatalf("created=%v err=%v", created, err)
			}
			if accounts.userID != "verified-user" || accounts.accountID != request.FromAccountID {
				t.Fatal("ownership checked against wrong identity or account")
			}
			if transfers.called != (tc.owned && tc.accountErr == nil) {
				t.Fatal("unauthorized request reached transfer repository")
			}
			if transfers.called && (transfers.userID != "verified-user" || transfers.key != "key" || transfers.request != request) {
				t.Fatal("incorrect repository arguments")
			}
			if err == nil && result.IdempotencyKey != "key" {
				t.Fatal("missing idempotency key")
			}
		})
	}
}

func TestCreateRejectsEmptyIdentity(t *testing.T) {
	s := NewService(nil, nil)
	if _, _, err := s.Create(context.Background(), "", "key", entities.CreateTransfer{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}
