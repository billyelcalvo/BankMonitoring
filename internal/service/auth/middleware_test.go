package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticationAndAuthorization(t *testing.T) {
	s := testService(t)
	allowed, err := s.Issue("user-123", []Permission{PermissionTransfersCreate})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := s.Issue("user-123", []Permission{PermissionTransfersRead})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		headers []string
		status  int
	}{
		{"missing", nil, 401},
		{"wrong scheme", []string{"Basic " + allowed}, 401},
		{"empty bearer", []string{"Bearer"}, 401},
		{"invalid", []string{"Bearer invalid"}, 401},
		{"extra fields", []string{"Bearer " + allowed + " extra"}, 401},
		{"duplicate", []string{"Bearer " + allowed, "Bearer " + allowed}, 401},
		{"permission denied", []string{"Bearer " + denied}, 403},
		{"allowed", []string{"Bearer " + allowed}, 204},
		{"case insensitive scheme", []string{"bearer " + allowed}, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := s.Authenticate(RequirePermission(PermissionTransfersCreate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				claims, ok := ClaimsFromContext(r.Context())
				if !ok || claims.Subject != "user-123" {
					t.Fatal("missing authenticated identity")
				}
				w.WriteHeader(http.StatusNoContent)
			})))
			r := httptest.NewRequest(http.MethodPost, "/transfers", nil)
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || called != (tc.status == 204) {
				t.Fatalf("status=%d handler called=%v", w.Code, called)
			}
			if w.Code == 401 && w.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("missing authentication challenge")
			}
		})
	}
}

func TestPermissionWithoutAuthentication(t *testing.T) {
	h := RequirePermission(PermissionTransfersCreate, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated request reached handler")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/transfers", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
