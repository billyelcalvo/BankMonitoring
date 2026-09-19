package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"
)

const testTransferKey = "e764bdae-5f99-44a2-8344-9c41c8d48449"
const transferBody = `{"from_account_id":"account-1","to_account_id":"account-2","amount":1000,"currency":"PEN"}`

type creatorStub struct {
	called      bool
	userID, key string
	request     entities.CreateTransfer
	created     bool
	err         error
}

func (s *creatorStub) Create(_ context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	s.called, s.userID, s.key, s.request = true, userID, key, request
	return entities.Transfer{ID: "transfer-1", IdempotencyKey: key, Amount: request.Amount}, s.created, s.err
}

func TestCreateTransferRoute(t *testing.T) {
	tokens, err := auth.NewTokenService([]byte(strings.Repeat("k", 32)), "bankmonitoring", "bankmonitoring-api")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := tokens.Issue("verified-user", []auth.Permission{auth.PermissionTransfersCreate})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := tokens.Issue("verified-user", []auth.Permission{auth.PermissionTransfersRead})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		token             string
		keys              []string
		body, contentType string
		created           bool
		serviceErr        error
		status            int
		called            bool
	}{
		{"created", allowed, []string{testTransferKey}, transferBody, "application/json", true, nil, 201, true},
		{"replay", allowed, []string{testTransferKey}, transferBody, "application/json", false, nil, 200, true},
		{"conflict", allowed, []string{testTransferKey}, transferBody, "application/json", false, repository.ErrIdempotencyConflict, 409, true},
		{"foreign account", allowed, []string{testTransferKey}, transferBody, "application/json", false, transfer.ErrForbidden, 403, true},
		{"invalid transfer", allowed, []string{testTransferKey}, transferBody, "application/json", false, repository.ErrInvalidTransfer, 400, true},
		{"database error", allowed, []string{testTransferKey}, transferBody, "application/json", false, errors.New("private database details"), 500, true},
		{"missing token", "", []string{testTransferKey}, transferBody, "application/json", false, nil, 401, false},
		{"invalid token", "invalid", []string{testTransferKey}, transferBody, "application/json", false, nil, 401, false},
		{"missing permission", denied, []string{testTransferKey}, transferBody, "application/json", false, nil, 403, false},
		{"missing key", allowed, nil, transferBody, "application/json", false, nil, 400, false},
		{"duplicate key", allowed, []string{testTransferKey, testTransferKey}, transferBody, "application/json", false, nil, 400, false},
		{"invalid key", allowed, []string{"invalid"}, transferBody, "application/json", false, nil, 400, false},
		{"zero key", allowed, []string{"00000000-0000-0000-0000-000000000000"}, transferBody, "application/json", false, nil, 400, false},
		{"user spoofing", allowed, []string{testTransferKey}, `{"user_id":"victim"}`, "application/json", false, nil, 400, false},
		{"key in body", allowed, []string{testTransferKey}, `{"idempotency_key":"body-key"}`, "application/json", false, nil, 400, false},
		{"invalid JSON", allowed, []string{testTransferKey}, `{`, "application/json", false, nil, 400, false},
		{"null", allowed, []string{testTransferKey}, `null`, "application/json", false, nil, 400, false},
		{"multiple objects", allowed, []string{testTransferKey}, transferBody + `{}`, "application/json", false, nil, 400, false},
		{"too large", allowed, []string{testTransferKey}, `{"description":"` + strings.Repeat("a", 65<<10) + `"}`, "application/json", false, nil, 413, false},
		{"wrong content type", allowed, []string{testTransferKey}, transferBody, "text/plain", false, nil, 415, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &creatorStub{created: tc.created, err: tc.serviceErr}
			h := NewHandler(tokens, service)
			r := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			for _, key := range tc.keys {
				r.Header.Add("Idempotency-Key", key)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || service.called != tc.called {
				t.Fatalf("status=%d called=%v body=%s", w.Code, service.called, w.Body.String())
			}
			if service.called && (service.userID != "verified-user" || service.key != testTransferKey || service.request.FromAccountID != "account-1") {
				t.Fatal("service did not receive the verified JWT identity, header key and request")
			}
			if strings.Contains(w.Body.String(), "private database details") {
				t.Fatal("leaked internal error")
			}
			if w.Code == 200 || w.Code == 201 {
				var result entities.Transfer
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.IdempotencyKey != testTransferKey {
					t.Fatal("missing response idempotency key")
				}
			}
		})
	}
}
