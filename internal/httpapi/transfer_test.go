package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"
)

const testTransferKey = "e764bdae-5f99-44a2-8344-9c41c8d48449"
const transferBody = `{"from_account_id":"account-1","to_account_id":"account-2","amount":1000,"currency":"PEN"}`

type transferStub struct {
	called      bool
	userID, key string
	request     entities.CreateTransfer
	status      valueobjects.TransferStatus
	created     bool
	err         error
}

func (s *transferStub) Create(_ context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error) {
	s.called, s.userID, s.key, s.request = true, userID, key, request
	status := s.status
	if status == "" {
		status = valueobjects.TransferStatusCompleted
	}
	return entities.Transfer{ID: "transfer-1", IdempotencyKey: key, Amount: request.Amount, Status: status}, s.created, s.err
}

func (s *transferStub) Get(_ context.Context, userID, id string) (entities.Transfer, error) {
	s.userID = userID
	if s.err != nil {
		return entities.Transfer{}, s.err
	}
	return entities.Transfer{ID: id}, nil
}

func TestCreateTransferRoute(t *testing.T) {
	tokens := testTokens(t)
	allowed := issue(t, tokens, auth.PermissionTransfersCreate)
	denied := issue(t, tokens, auth.PermissionTransfersRead)
	jsonType := map[string][]string{"Content-Type": {"application/json"}}
	for _, tc := range []struct {
		name     string
		token    string
		keys     []string
		body     string
		headers  map[string][]string
		stub     transferStub
		status   int
		called   bool
		replayed bool
	}{
		{"created", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{created: true}, 201, true, false},
		{"replay", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{}, 200, true, true},
		{"insufficient funds", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{created: true, status: valueobjects.TransferStatusFailed}, 422, true, false},
		{"insufficient funds replay", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{status: valueobjects.TransferStatusFailed}, 422, true, true},
		{"conflict", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: repository.ErrIdempotencyConflict}, 409, true, false},
		{"foreign account", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: transfer.ErrForbidden}, 403, true, false},
		{"unknown destination", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: transfer.ErrDestinationNotFound}, 422, true, false},
		{"currency mismatch", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: transfer.ErrCurrencyMismatch}, 422, true, false},
		{"overflow", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: entities.ErrBalanceOverflow}, 422, true, false},
		{"invalid transfer", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: entities.ErrInvalidTransfer}, 400, true, false},
		{"database error", allowed, []string{testTransferKey}, transferBody, jsonType, transferStub{err: errors.New("private database details")}, 500, true, false},
		{"missing token", "", []string{testTransferKey}, transferBody, jsonType, transferStub{}, 401, false, false},
		{"invalid token", "invalid", []string{testTransferKey}, transferBody, jsonType, transferStub{}, 401, false, false},
		{"missing permission", denied, []string{testTransferKey}, transferBody, jsonType, transferStub{}, 403, false, false},
		{"missing key", allowed, nil, transferBody, jsonType, transferStub{}, 400, false, false},
		{"duplicate key", allowed, []string{testTransferKey, testTransferKey}, transferBody, jsonType, transferStub{}, 400, false, false},
		{"invalid key", allowed, []string{"invalid"}, transferBody, jsonType, transferStub{}, 400, false, false},
		{"zero key", allowed, []string{"00000000-0000-0000-0000-000000000000"}, transferBody, jsonType, transferStub{}, 400, false, false},
		{"user spoofing", allowed, []string{testTransferKey}, `{"user_id":"victim"}`, jsonType, transferStub{}, 400, false, false},
		{"key in body", allowed, []string{testTransferKey}, `{"idempotency_key":"body-key"}`, jsonType, transferStub{}, 400, false, false},
		{"invalid JSON", allowed, []string{testTransferKey}, `{`, jsonType, transferStub{}, 400, false, false},
		{"null", allowed, []string{testTransferKey}, `null`, jsonType, transferStub{}, 400, false, false},
		{"multiple objects", allowed, []string{testTransferKey}, transferBody + `{}`, jsonType, transferStub{}, 400, false, false},
		{"too large", allowed, []string{testTransferKey}, `{"description":"` + strings.Repeat("a", 65<<10) + `"}`, jsonType, transferStub{}, 413, false, false},
		{"wrong content type", allowed, []string{testTransferKey}, transferBody, map[string][]string{"Content-Type": {"text/plain"}}, transferStub{}, 415, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := tc.stub
			h := NewHandler(Dependencies{Tokens: tokens, Transfers: &stub})
			headers := map[string][]string{"Idempotency-Key": tc.keys}
			for k, v := range tc.headers {
				headers[k] = v
			}
			w := serve(h, request{method: "POST", path: "/transfers", token: tc.token, body: tc.body, headers: headers})
			if w.Code != tc.status || stub.called != tc.called {
				t.Fatalf("status=%d called=%v body=%s", w.Code, stub.called, w.Body.String())
			}
			if (w.Header().Get("Idempotent-Replayed") == "true") != tc.replayed {
				t.Fatal("Idempotent-Replayed header mismatch")
			}
			if stub.called && (stub.userID != "verified-user" || stub.key != testTransferKey || stub.request.FromAccountID != "account-1") {
				t.Fatal("service did not receive the verified JWT identity, header key and request")
			}
			if strings.Contains(w.Body.String(), "private database details") {
				t.Fatal("leaked internal error")
			}
			if w.Code == 200 || w.Code == 201 {
				var result entities.Transfer
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.IdempotencyKey != testTransferKey {
					t.Fatalf("missing response idempotency key: %s", w.Body.String())
				}
			}
		})
	}
}

func TestGetTransferRoute(t *testing.T) {
	tokens := testTokens(t)
	raw := issue(t, tokens, auth.PermissionTransfersRead)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"found", nil, 200},
		{"missing", transfer.ErrNotFound, 404},
		{"database error", errors.New("private"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &transferStub{err: tc.err}
			w := serve(NewHandler(Dependencies{Tokens: tokens, Transfers: stub}),
				request{method: "GET", path: "/transfers/abc", token: raw})
			if w.Code != tc.status || stub.userID != "verified-user" {
				t.Fatalf("status=%d user=%s", w.Code, stub.userID)
			}
		})
	}
}
