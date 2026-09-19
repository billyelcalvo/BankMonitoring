package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testService(t *testing.T) *TokenService {
	t.Helper()
	s, err := NewTokenService([]byte(strings.Repeat("k", 32)), "bankmonitoring", "bankmonitoring-api")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIssueAndVerify(t *testing.T) {
	s := testService(t)
	raw, err := s.Issue("user-123", []Permission{PermissionTransfersRead})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-123" || !claims.HasPermission(PermissionTransfersRead) || claims.HasPermission(PermissionTransfersCreate) {
		t.Fatalf("unexpected identity or permissions: %+v", claims)
	}
	if claims.ExpiresAt.Sub(claims.IssuedAt.Time) != AccessTokenTTL {
		t.Fatal("unexpected token lifetime")
	}
	if _, err := s.Issue(" ", nil); err == nil {
		t.Fatal("accepted empty user ID")
	}
}

func TestRejectInvalidTokens(t *testing.T) {
	s := testService(t)
	for _, name := range []string{"expired", "issuer", "audience", "missing issuer", "missing audience", "missing expiry", "missing issued at", "missing subject", "future issued at", "future not before", "excessive lifetime", "wrong key", "wrong algorithm", "unsigned", "tampered", "malformed"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			claims := Claims{RegisteredClaims: jwt.RegisteredClaims{
				Subject: "user-123", Issuer: s.issuer, Audience: jwt.ClaimStrings{s.audience},
				IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
			}}
			var method jwt.SigningMethod = jwt.SigningMethodHS256
			var key any = s.key
			switch name {
			case "expired":
				claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second))
			case "issuer":
				claims.Issuer = "another-issuer"
			case "audience":
				claims.Audience = jwt.ClaimStrings{"another-api"}
			case "missing issuer":
				claims.Issuer = ""
			case "missing audience":
				claims.Audience = nil
			case "missing expiry":
				claims.ExpiresAt = nil
			case "missing issued at":
				claims.IssuedAt = nil
			case "missing subject":
				claims.Subject = ""
			case "future issued at":
				claims.IssuedAt = jwt.NewNumericDate(now.Add(30 * time.Second))
			case "future not before":
				claims.NotBefore = jwt.NewNumericDate(now.Add(30 * time.Second))
			case "excessive lifetime":
				claims.ExpiresAt = jwt.NewNumericDate(now.Add(time.Hour))
			case "wrong key":
				key = []byte(strings.Repeat("x", 32))
			case "wrong algorithm":
				method = jwt.SigningMethodHS384
			case "unsigned":
				method, key = jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType
			}
			raw, err := jwt.NewWithClaims(method, claims).SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			if name == "tampered" {
				parts := strings.Split(raw, ".")
				claims.Subject = "another-user"
				modified := jwt.NewWithClaims(method, claims)
				unsigned, err := modified.SigningString()
				if err != nil {
					t.Fatal(err)
				}
				raw = unsigned + "." + parts[2]
			}
			if name == "malformed" {
				raw = "not-a-jwt"
			}
			if _, err := s.Verify(raw); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected invalid token, got %v", err)
			}
		})
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ key, issuer, audience string }{
		{"", "issuer", "audience"}, {"short", "issuer", "audience"},
		{strings.Repeat("k", 32), "", "audience"}, {strings.Repeat("k", 32), "issuer", ""},
	} {
		if _, err := NewTokenService([]byte(tc.key), tc.issuer, tc.audience); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}
