package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repositories"
	"bankmonitoring/internal/domain/valueobjects"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const testKey = "e764bdae-5f99-44a2-8344-9c41c8d48449"

var testRequest = entities.CreateTransfer{
	FromAccountID: "account-1", ToAccountID: "account-2", Amount: 1000,
	Currency: "PEN", Description: "Payment",
}

type stubRow func(...any) error

func (r stubRow) Scan(dest ...any) error { return r(dest...) }

func errorRow(err error) pgx.Row {
	return stubRow(func(...any) error { return err })
}

func transferRow(request entities.CreateTransfer) pgx.Row {
	return stubRow(func(dest ...any) error {
		*dest[0].(*string) = "saved-transfer-id"
		*dest[1].(*string) = request.FromAccountID
		*dest[2].(*string) = request.ToAccountID
		*dest[3].(*int64) = request.Amount
		*dest[4].(*string) = request.Currency
		*dest[5].(*string) = request.Description
		*dest[6].(*valueobjects.TransferStatus) = valueobjects.TransferStatusPending
		*dest[7].(*time.Time) = time.Unix(100, 0).UTC()
		*dest[8].(*time.Time) = time.Unix(100, 0).UTC()
		payload, err := json.Marshal(request)
		*dest[9].(*[]byte) = payload
		*dest[10].(*string) = testKey
		return err
	})
}

type queryStep struct {
	operation string
	row       pgx.Row
}

type scriptedDB struct {
	t     *testing.T
	steps []queryStep
	calls int
}

func (db *scriptedDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	db.t.Helper()
	if db.calls >= len(db.steps) {
		db.t.Fatal("unexpected database operation")
	}
	step := db.steps[db.calls]
	db.calls++
	if !strings.HasPrefix(strings.TrimSpace(sql), step.operation) {
		db.t.Fatalf("expected %s, got %s", step.operation, sql)
	}
	if args[0] != "user-1" || !args[1].(pgtype.UUID).Valid {
		db.t.Fatal("query must use authenticated user ID and UUID key")
	}
	return step.row
}

func TestCreateIdempotent(t *testing.T) {
	changed := testRequest
	changed.Amount++
	dbError := errors.New("database unavailable")
	for _, tc := range []struct {
		name    string
		steps   []queryStep
		created bool
		wantErr error
	}{
		{"new", []queryStep{{"SELECT", errorRow(pgx.ErrNoRows)}, {"INSERT", transferRow(testRequest)}}, true, nil},
		{"replay", []queryStep{{"SELECT", transferRow(testRequest)}}, false, nil},
		{"conflict", []queryStep{{"SELECT", transferRow(changed)}}, false, repositories.ErrIdempotencyConflict},
		{"concurrent replay", []queryStep{{"SELECT", errorRow(pgx.ErrNoRows)}, {"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", transferRow(testRequest)}}, false, nil},
		{"concurrent conflict", []queryStep{{"SELECT", errorRow(pgx.ErrNoRows)}, {"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", transferRow(changed)}}, false, repositories.ErrIdempotencyConflict},
		{"lookup error", []queryStep{{"SELECT", errorRow(dbError)}}, false, dbError},
		{"insert error", []queryStep{{"SELECT", errorRow(pgx.ErrNoRows)}, {"INSERT", errorRow(dbError)}}, false, dbError},
		{"concurrent lookup error", []queryStep{{"SELECT", errorRow(pgx.ErrNoRows)}, {"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", errorRow(dbError)}}, false, dbError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &scriptedDB{t: t, steps: tc.steps}
			repo := &TransferRepository{db: db}
			transfer, created, err := repo.CreateIdempotent(context.Background(), "user-1", testKey, testRequest)
			if !errors.Is(err, tc.wantErr) || created != tc.created {
				t.Fatalf("created=%v error=%v", created, err)
			}
			if err == nil && (transfer.ID != "saved-transfer-id" || transfer.IdempotencyKey != testKey) {
				t.Fatalf("unexpected transfer: %+v", transfer)
			}
			if db.calls != len(tc.steps) {
				t.Fatal("missing database operations")
			}
		})
	}
}

func TestRequestComparison(t *testing.T) {
	for _, field := range []string{"from", "to", "amount", "currency", "description"} {
		t.Run(field, func(t *testing.T) {
			changed := testRequest
			switch field {
			case "from":
				changed.FromAccountID = "other"
			case "to":
				changed.ToAccountID = "other"
			case "amount":
				changed.Amount++
			case "currency":
				changed.Currency = "USD"
			case "description":
				changed.Description = "different"
			}
			_, _, err := replay(entities.Transfer{}, testRequest, changed)
			if !errors.Is(err, repositories.ErrIdempotencyConflict) {
				t.Fatal("changed request was accepted")
			}
		})
	}
	transfer := entities.Transfer{ID: "existing", Status: valueobjects.TransferStatusCompleted}
	got, created, err := replay(transfer, testRequest, testRequest)
	if err != nil || created || got != transfer {
		t.Fatal("replay must preserve the current transfer state")
	}
}

func TestInvalidRequestsNeverQueryDatabase(t *testing.T) {
	for _, name := range []string{"key", "zero key", "user", "amount", "currency", "same account", "empty account"} {
		t.Run(name, func(t *testing.T) {
			request, userID, key := testRequest, "user-1", testKey
			switch name {
			case "key":
				key = "invalid"
			case "zero key":
				key = "00000000-0000-0000-0000-000000000000"
			case "user":
				userID = ""
			case "amount":
				request.Amount = 0
			case "currency":
				request.Currency = "pen"
			case "same account":
				request.ToAccountID = request.FromAccountID
			case "empty account":
				request.FromAccountID = " "
			}
			repo := &TransferRepository{db: &scriptedDB{t: t}}
			_, _, err := repo.CreateIdempotent(context.Background(), userID, key, request)
			if !errors.Is(err, repositories.ErrInvalidTransfer) {
				t.Fatalf("expected invalid request, got %v", err)
			}
		})
	}
}

func TestPoolRequiresConnectionString(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := NewPool(context.Background()); err == nil {
		t.Fatal("accepted missing DATABASE_URL")
	}
}
