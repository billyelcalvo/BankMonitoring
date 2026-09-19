package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Permission string

const (
	PermissionTransfersRead   Permission = "transfers:read"
	PermissionTransfersCreate Permission = "transfers:create"
	AccessTokenTTL                       = 15 * time.Minute
)

var ErrInvalidToken = errors.New("invalid access token")

type Claims struct {
	jwt.RegisteredClaims
	Permissions []Permission `json:"permissions"`
}

func (c Claims) Validate() error {
	if strings.TrimSpace(c.Subject) == "" || c.IssuedAt == nil || c.ExpiresAt == nil {
		return ErrInvalidToken
	}
	if !c.ExpiresAt.After(c.IssuedAt.Time) || c.ExpiresAt.Sub(c.IssuedAt.Time) > AccessTokenTTL {
		return ErrInvalidToken
	}
	return nil
}

func (c Claims) HasPermission(permission Permission) bool {
	for _, granted := range c.Permissions {
		if permission != "" && granted == permission {
			return true
		}
	}
	return false
}

type TokenService struct {
	key      []byte
	issuer   string
	audience string
}

func NewTokenService(key []byte, issuer, audience string) (*TokenService, error) {
	if len(key) < 32 {
		return nil, errors.New("JWT signing key must contain at least 32 random bytes")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, errors.New("JWT issuer and audience are required")
	}
	return &TokenService{key: append([]byte(nil), key...), issuer: issuer, audience: audience}, nil
}

// Issue must only be called after authenticating the user. Permissions must come
// from trusted server-side data, never from the client's login request.
func (s *TokenService) Issue(userID string, permissions []Permission) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("user ID is required")
	}
	now := time.Now().UTC()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID, Issuer: s.issuer, Audience: jwt.ClaimStrings{s.audience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
		},
		Permissions: append([]Permission{}, permissions...),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
}

func (s *TokenService) Verify(raw string) (*Claims, error) {
	claims := new(Claims)
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		return s.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer), jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithStrictDecoding())
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
