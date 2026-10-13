//go:build integration

package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"
	"bankmonitoring/internal/repository"
	"bankmonitoring/internal/seed"
	"bankmonitoring/internal/testutil/pgtest"
)

var databaseURL string

func TestMain(m *testing.M) { pgtest.Main(m, &databaseURL) }

const key = "8a1d6f0e-3c43-4a51-9d0b-6f3a3b8f2c11"

func draft(from, to string, amount int64) domainrepository.NewTransfer {
	return domainrepository.NewTransfer{
		UserID: "alice", IdempotencyKey: key,
		Request: entities.CreateTransfer{FromAccountID: from, ToAccountID: to, Amount: amount, Currency: "PEN"},
		Status:  valueobjects.TransferStatusCompleted,
	}
}

func TestMigrationsAreRepeatable(t *testing.T) {
	version, err := repository.Migrate(databaseURL)
	if err != nil || version != 5 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestLockForTransfer(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-1": 100, "a-2": 50})
	store := repository.NewStore(pool)
	ctx := context.Background()

	err := store.WithinTx(ctx, func(tx domainrepository.Tx) error {
		locked, err := tx.Accounts().LockForTransfer(ctx, "a-2", "a-1", "missing", "a-1")
		if err != nil {
			return err
		}
		if len(locked) != 2 || locked["a-1"].Balance != 100 || locked["a-2"].UserID != "alice" {
			t.Fatalf("unexpected locked accounts: %+v", locked)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLockBlocksConcurrentWriters(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-1": 100})
	store := repository.NewStore(pool)
	ctx := context.Background()

	locked := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = store.WithinTx(ctx, func(tx domainrepository.Tx) error {
			if _, err := tx.Accounts().LockForTransfer(ctx, "a-1"); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	// While the first transaction holds the row, a second one must wait.
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	err := store.WithinTx(waitCtx, func(tx domainrepository.Tx) error {
		_, err := tx.Accounts().LockForTransfer(waitCtx, "a-1")
		return err
	})
	if err == nil {
		t.Fatal("second transaction acquired a row that was already locked")
	}
	close(release)
	wg.Wait()
}

func TestSaveBalancesAndLedger(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-1": 100, "a-2": 0})
	store := repository.NewStore(pool)
	ctx := context.Background()

	err := store.WithinTx(ctx, func(tx domainrepository.Tx) error {
		accounts, err := tx.Accounts().LockForTransfer(ctx, "a-1", "a-2")
		if err != nil {
			return err
		}
		created, _, err := tx.Transfers().CreateIdempotent(ctx, draft("a-1", "a-2", 40))
		if err != nil {
			return err
		}
		accounts["a-1"].Balance, accounts["a-2"].Balance = 60, 40
		if err := tx.Accounts().SaveBalances(ctx, accounts["a-1"], accounts["a-2"]); err != nil {
			return err
		}
		return tx.Ledger().Append(ctx,
			entities.LedgerEntry{TransferID: created.ID, AccountID: "a-1", Amount: -40, BalanceAfter: 60},
			entities.LedgerEntry{TransferID: created.ID, AccountID: "a-2", Amount: 40, BalanceAfter: 40})
	})
	if err != nil {
		t.Fatal(err)
	}
	if pgtest.Balance(t, pool, "a-1") != 60 || pgtest.Balance(t, pool, "a-2") != 40 {
		t.Fatal("balances not saved")
	}
	var entries, sum int64
	if err := pool.QueryRow(ctx, `SELECT count(*), sum(amount) FROM ledger_entries`).Scan(&entries, &sum); err != nil || entries != 2 || sum != 0 {
		t.Fatalf("entries=%d sum=%d err=%v", entries, sum, err)
	}

	// Saving an account that doesn't exist must not silently succeed.
	err = store.WithinTx(ctx, func(tx domainrepository.Tx) error {
		return tx.Accounts().SaveBalances(ctx, &entities.Account{ID: "ghost", Balance: 1})
	})
	if err == nil {
		t.Fatal("expected an error for a missing account")
	}
}

func TestDatabaseRefusesNegativeBalances(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-1": 10})
	store := repository.NewStore(pool)
	ctx := context.Background()

	err := store.WithinTx(ctx, func(tx domainrepository.Tx) error {
		return tx.Accounts().SaveBalances(ctx, &entities.Account{ID: "a-1", Balance: -1})
	})
	if err == nil || pgtest.Balance(t, pool, "a-1") != 10 {
		t.Fatal("the CHECK constraint should have rejected the overdraft")
	}
}

func TestCreateIdempotentAgainstPostgres(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-1": 100, "a-2": 0})
	transfers := repository.NewTransferRepository(pool)
	ctx := context.Background()

	first, created, err := transfers.CreateIdempotent(ctx, draft("a-1", "a-2", 10))
	if err != nil || !created || first.Status != valueobjects.TransferStatusCompleted {
		t.Fatalf("created=%v err=%v", created, err)
	}
	again, created, err := transfers.CreateIdempotent(ctx, draft("a-1", "a-2", 10))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("replay failed: created=%v err=%v", created, err)
	}
	if _, _, err := transfers.CreateIdempotent(ctx, draft("a-1", "a-2", 11)); !errors.Is(err, domainrepository.ErrIdempotencyConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	failed := draft("a-1", "a-2", 10)
	failed.IdempotencyKey = "0f6c7c43-9f0e-4e1c-bb7c-2c3b5b0e1d22"
	failed.Status, failed.FailureReason = valueobjects.TransferStatusFailed, valueobjects.FailureReasonInsufficientFunds
	stored, _, err := transfers.CreateIdempotent(ctx, failed)
	if err != nil || stored.FailureReason != valueobjects.FailureReasonInsufficientFunds {
		t.Fatalf("failure reason not stored: %+v %v", stored, err)
	}

	found, err := transfers.FindByID(ctx, "alice", first.ID)
	if err != nil || found.ID != first.ID || found.IdempotencyKey != key {
		t.Fatalf("found=%+v err=%v", found, err)
	}
	if _, err := transfers.FindByID(ctx, "mallory", first.ID); !errors.Is(err, domainrepository.ErrNotFound) {
		t.Fatalf("other users must not see the transfer, got %v", err)
	}
	if _, err := transfers.FindByID(ctx, "alice", "6b4b6f0a-0000-4000-8000-000000000000"); !errors.Is(err, domainrepository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestAccountAndUserQueries(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	pgtest.CreateUser(t, pool, "alice", map[string]int64{"a-2": 5, "a-1": 7})
	pgtest.CreateUser(t, pool, "bob", map[string]int64{"b-1": 1})
	accounts := repository.NewAccountRepository(pool)
	ctx := context.Background()

	list, err := accounts.ListByUser(ctx, "alice")
	if err != nil || len(list) != 2 || list[0].ID != "a-1" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if a, err := accounts.FindOwned(ctx, "alice", "a-1"); err != nil || a.Balance != 7 {
		t.Fatalf("account=%+v err=%v", a, err)
	}
	if _, err := accounts.FindOwned(ctx, "alice", "b-1"); !errors.Is(err, domainrepository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	users := repository.NewUserRepository(pool)
	if u, err := users.FindByEmail(ctx, "ALICE@example.com"); err != nil || u.ID != "alice" {
		t.Fatalf("user=%+v err=%v", u, err)
	}
	if _, err := users.FindByEmail(ctx, "nobody@example.com"); !errors.Is(err, domainrepository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestDemoSeedIsRepeatable(t *testing.T) {
	pool := pgtest.Pool(t, databaseURL, 4)
	ctx := context.Background()
	if err := seed.Demo(ctx, pool, "demo-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET balance = 1 WHERE id = 'acc-alice-pen'`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Demo(ctx, pool, "demo-password"); err != nil {
		t.Fatal(err)
	}
	if pgtest.Balance(t, pool, "acc-alice-pen") != 1 {
		t.Fatal("re-running the seed must not reset balances")
	}
}
