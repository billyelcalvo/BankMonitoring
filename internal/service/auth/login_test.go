package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
)

type usersStub map[string]entities.User

func (u usersStub) FindByEmail(_ context.Context, email string) (entities.User, error) {
	if email == "broken@example.com" {
		return entities.User{}, errors.New("database unavailable")
	}
	user, ok := u[strings.ToLower(email)]
	if !ok {
		return entities.User{}, repository.ErrNotFound
	}
	return user, nil
}

func TestLogin(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	tokens := testService(t)
	s := NewLoginService(usersStub{"alice@example.com": {
		ID: "alice", Email: "alice@example.com", PasswordHash: hash,
		Permissions: []string{string(PermissionTransfersCreate)},
	}}, tokens)

	raw, err := s.Login(context.Background(), " Alice@Example.com ", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.Verify(raw)
	if err != nil || claims.Subject != "alice" || !claims.HasPermission(PermissionTransfersCreate) {
		t.Fatalf("unexpected claims %+v err=%v", claims, err)
	}

	for name, creds := range map[string][2]string{
		"wrong password": {"alice@example.com", "wrong"},
		"unknown email":  {"nobody@example.com", "correct horse"},
		"empty":          {"", ""},
	} {
		if _, err := s.Login(context.Background(), creds[0], creds[1]); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("%s: expected invalid credentials, got %v", name, err)
		}
	}
	if _, err := s.Login(context.Background(), "broken@example.com", "x"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("database errors must not look like bad credentials, got %v", err)
	}
}
