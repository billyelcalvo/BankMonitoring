package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// These tests script the database answers to cover the idempotency branches
// in isolation. The real PostgreSQL behaviour (locks, constraints, races) is
// covered by the integration tests in this package.

const testKey = "e764bdae-5f99-44a2-8344-9c41c8d48449"

var testRequest = entities.CreateTransfer{
	FromAccountID: "account-1", ToAccountID: "account-2", Amount: 1000,
	Currency: "PEN", Description: "Payment",
}

func newTestTransfer() domainrepository.NewTransfer {
	return domainrepository.NewTransfer{
		UserID: "user-1", IdempotencyKey: testKey, Request: testRequest,
		Status: valueobjects.TransferStatusCompleted,
	}
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
		*dest[6].(*valueobjects.TransferStatus) = valueobjects.TransferStatusCompleted
		*dest[7].(*valueobjects.FailureReason) = valueobjects.FailureReasonNone
		*dest[8].(*time.Time) = time.Unix(100, 0).UTC()
		*dest[9].(*time.Time) = time.Unix(100, 0).UTC()
		payload, err := json.Marshal(request)
		*dest[10].(*[]byte) = payload
		*dest[11].(*string) = testKey
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

func (db *scriptedDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	db.t.Fatal("unexpected Query call")
	return nil, nil
}

func (db *scriptedDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	db.t.Fatal("unexpected Exec call")
	return pgconn.CommandTag{}, nil
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
		{"new", []queryStep{{"INSERT", transferRow(testRequest)}}, true, nil},
		{"replay", []queryStep{{"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", transferRow(testRequest)}}, false, nil},
		{"conflict", []queryStep{{"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", transferRow(changed)}}, false, domainrepository.ErrIdempotencyConflict},
		{"insert error", []queryStep{{"INSERT", errorRow(dbError)}}, false, dbError},
		{"lookup error", []queryStep{{"INSERT", errorRow(pgx.ErrNoRows)}, {"SELECT", errorRow(dbError)}}, false, dbError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &scriptedDB{t: t, steps: tc.steps}
			repo := &TransferRepository{db: db}
			transfer, created, err := repo.CreateIdempotent(context.Background(), newTestTransfer())
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
			if !errors.Is(err, domainrepository.ErrIdempotencyConflict) {
				t.Fatal("changed request was accepted")
			}
		})
	}
	transfer := entities.Transfer{ID: "existing", Status: valueobjects.TransferStatusFailed}
	got, created, err := replay(transfer, testRequest, testRequest)
	if err != nil || created || got != transfer {
		t.Fatal("replay must preserve the stored transfer")
	}
}

func TestInvalidKeysNeverQueryDatabase(t *testing.T) {
	for _, key := range []string{"invalid", "00000000-0000-0000-0000-000000000000", ""} {
		t.Run(key, func(t *testing.T) {
			draft := newTestTransfer()
			draft.IdempotencyKey = key
			repo := &TransferRepository{db: &scriptedDB{t: t}}
			_, _, err := repo.CreateIdempotent(context.Background(), draft)
			if !errors.Is(err, entities.ErrInvalidTransfer) {
				t.Fatalf("expected invalid request, got %v", err)
			}
		})
	}
}

func TestFindByIDRejectsMalformedIDs(t *testing.T) {
	repo := &TransferRepository{db: &scriptedDB{t: t}}
	if _, err := repo.FindByID(context.Background(), "user-1", "not-a-uuid"); !errors.Is(err, domainrepository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestPoolRequiresConnectionString(t *testing.T) {
	if _, err := NewPool(context.Background(), " ", 10); err == nil {
		t.Fatal("accepted missing DATABASE_URL")
	}
	if _, err := NewPool(context.Background(), "postgres://%zz", 10); err == nil || strings.Contains(err.Error(), "%zz") {
		t.Fatalf("invalid URL must fail without echoing it: %v", err)
	}
}
