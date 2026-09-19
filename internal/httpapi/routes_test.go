package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bankmonitoring/internal/service/auth"
)

func TestRoutes(t *testing.T) {
	tokens, err := auth.NewTokenService([]byte(strings.Repeat("k", 32)), "bankmonitoring", "bankmonitoring-api")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tokens.Issue("user-123", []auth.Permission{auth.PermissionTransfersRead})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(tokens)
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/health", "", 200}, {"GET", "/me", "", 401},
		{"GET", "/me", raw, 200}, {"POST", "/me", raw, 405}, {"GET", "/unknown", "", 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.status, w.Code)
		}
		if tc.path == "/me" && w.Code == http.StatusOK {
			var body struct {
				UserID      string            `json:"user_id"`
				Permissions []auth.Permission `json:"permissions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.UserID != "user-123" || len(body.Permissions) != 1 || body.Permissions[0] != auth.PermissionTransfersRead || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected profile response: %s", w.Body.String())
			}
		}
	}
}
