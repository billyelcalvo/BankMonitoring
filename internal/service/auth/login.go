package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bankmonitoring/internal/domain/repository"

	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

type LoginService struct {
	users  repository.UserRepository
	tokens *TokenService
	// dummyHash is compared when the email doesn't exist, so a wrong email
	// takes about as long as a wrong password and can't be told apart.
	dummyHash []byte
}

func NewLoginService(users repository.UserRepository, tokens *TokenService) *LoginService {
	hash, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	return &LoginService{users: users, tokens: tokens, dummyHash: hash}
}

// Login checks the credentials and issues an access token carrying the
// permissions stored for the user, never anything taken from the request.
func (s *LoginService) Login(ctx context.Context, email, password string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return "", ErrInvalidCredentials
	}
	user, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", ErrInvalidCredentials
	}
	permissions := make([]Permission, len(user.Permissions))
	for i, p := range user.Permissions {
		permissions[i] = Permission(p)
	}
	return s.tokens.Issue(user.ID, permissions)
}

// HashPassword is used when provisioning users (see the demo seed).
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
