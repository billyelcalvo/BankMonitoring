package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/service/account"
	"bankmonitoring/internal/service/auth"
)

func testTokens(t *testing.T) *auth.TokenService {
	t.Helper()
	tokens, err := auth.NewTokenService([]byte(strings.Repeat("k", 32)), "bankmonitoring", "bankmonitoring-api")
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func issue(t *testing.T, tokens *auth.TokenService, permissions ...auth.Permission) string {
	t.Helper()
	raw, err := tokens.Issue("verified-user", permissions)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type request struct {
	method, path, token, body string
	headers                   map[string][]string
}

func serve(h http.Handler, req request) *httptest.ResponseRecorder {
	var body io.Reader
	if req.body != "" {
		body = strings.NewReader(req.body)
	}
	r := httptest.NewRequest(req.method, req.path, body)
	if req.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if req.token != "" {
		r.Header.Set("Authorization", "Bearer "+req.token)
	}
	for name, values := range req.headers {
		r.Header.Del(name)
		for _, v := range values {
			r.Header.Add(name, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type accountsStub struct {
	accounts []entities.Account
	err      error
	userID   string
}

func (s *accountsStub) List(_ context.Context, userID string) ([]entities.Account, error) {
	s.userID = userID
	return s.accounts, s.err
}

func (s *accountsStub) Get(_ context.Context, userID, id string) (entities.Account, error) {
	s.userID = userID
	if s.err != nil {
		return entities.Account{}, s.err
	}
	for _, a := range s.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return entities.Account{}, account.ErrNotFound
}

type loginStub struct {
	token string
	err   error
}

func (s loginStub) Login(context.Context, string, string) (string, error) {
	return s.token, s.err
}
