package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/service/auth"
)

func TestRoutes(t *testing.T) {
	tokens := testTokens(t)
	raw := issue(t, tokens, auth.PermissionTransfersRead)
	h := NewHandler(Dependencies{Tokens: tokens})
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/health", "", 200}, {"GET", "/ready", "", 200}, {"GET", "/me", "", 401},
		{"GET", "/me", raw, 200}, {"POST", "/me", raw, 405}, {"GET", "/unknown", "", 404},
		{"GET", "/accounts", raw, 403}, {"GET", "/openapi.yaml", "", 200}, {"GET", "/docs", "", 200},
	} {
		w := serve(h, request{method: tc.method, path: tc.path, token: tc.token})
		if w.Code != tc.status {
			t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.status, w.Code)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Fatal("every response should carry a request ID")
		}
		if tc.path == "/me" && w.Code == http.StatusOK {
			var body struct {
				UserID      string            `json:"user_id"`
				Permissions []auth.Permission `json:"permissions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.UserID != "verified-user" || len(body.Permissions) != 1 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected profile response: %s", w.Body.String())
			}
		}
	}
}

func TestReadyReportsDatabaseOutage(t *testing.T) {
	h := NewHandler(Dependencies{Tokens: testTokens(t), Ready: func(context.Context) error { return errors.New("down") }})
	if w := serve(h, request{method: "GET", path: "/ready"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestAccountRoutes(t *testing.T) {
	tokens := testTokens(t)
	raw := issue(t, tokens, auth.PermissionAccountsRead)
	stub := &accountsStub{accounts: []entities.Account{{ID: "acc-1", Currency: "PEN", Balance: 500}}}
	h := NewHandler(Dependencies{Tokens: tokens, Accounts: stub})

	w := serve(h, request{method: "GET", path: "/accounts", token: raw})
	var list struct{ Accounts []entities.Account }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Accounts) != 1 || stub.userID != "verified-user" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	w = serve(h, request{method: "GET", path: "/accounts/acc-1", token: raw})
	var got entities.Account
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Balance != 500 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w := serve(h, request{method: "GET", path: "/accounts/other", token: raw}); w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	// An empty list is still a JSON array, never null.
	empty := NewHandler(Dependencies{Tokens: tokens, Accounts: &accountsStub{}})
	if w := serve(empty, request{method: "GET", path: "/accounts", token: raw}); w.Body.String() != "{\"accounts\":[]}\n" {
		t.Fatalf("unexpected body %q", w.Body.String())
	}

	failing := NewHandler(Dependencies{Tokens: tokens, Accounts: &accountsStub{err: errors.New("private details")}})
	for _, path := range []string{"/accounts", "/accounts/acc-1"} {
		w := serve(failing, request{method: "GET", path: path, token: raw})
		if w.Code != 500 || strings.Contains(w.Body.String(), "private details") {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestLoginRoute(t *testing.T) {
	tokens := testTokens(t)
	for _, tc := range []struct {
		name, body string
		stub       loginStub
		status     int
	}{
		{"ok", `{"email":"a@b.c","password":"secret"}`, loginStub{token: "jwt"}, 200},
		{"bad credentials", `{"email":"a@b.c","password":"nope"}`, loginStub{err: auth.ErrInvalidCredentials}, 401},
		{"database error", `{"email":"a@b.c","password":"x"}`, loginStub{err: errors.New("boom")}, 500},
		{"unknown field", `{"email":"a@b.c","password":"x","admin":true}`, loginStub{}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(Dependencies{Tokens: tokens, Login: tc.stub})
			w := serve(h, request{method: "POST", path: "/auth/login", body: tc.body})
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 200 {
				var body loginResponse
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.AccessToken != "jwt" || body.TokenType != "Bearer" || body.ExpiresIn != 900 {
					t.Fatalf("unexpected body %s", w.Body.String())
				}
			}
		})
	}
}

func TestAccessLogRecordsMatchedRoute(t *testing.T) {
	var buf bytes.Buffer
	tokens := testTokens(t)
	h := NewHandler(Dependencies{Tokens: tokens, Transfers: &transferStub{}, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	serve(h, request{method: "GET", path: "/transfers/abc-123", token: issue(t, tokens, auth.PermissionTransfersRead)})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %s", buf.String())
	}
	if entry["route"] != "GET /transfers/{id}" {
		t.Fatalf("expected the route pattern, got %v", entry["route"])
	}
}
