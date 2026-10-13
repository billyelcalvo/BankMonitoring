package transfer

import (
	"context"
	"errors"
	"maps"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"
)

// fakeStore is an in-memory TxManager. Each WithinTx works on a copy of the
// state and only keeps it when fn returns nil, just like commit/rollback.
type fakeStore struct {
	accounts  map[string]entities.Account
	transfers map[string]storedTransfer
	ledger    []entities.LedgerEntry

	lockErr, saveErr, insertErr, ledgerErr error
}

type storedTransfer struct {
	transfer entities.Transfer
	request  entities.CreateTransfer
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		accounts: map[string]entities.Account{
			"alice-pen": {ID: "alice-pen", UserID: "alice", Currency: "PEN", Balance: 1000},
			"bob-pen":   {ID: "bob-pen", UserID: "bob", Currency: "PEN", Balance: 0},
			"bob-usd":   {ID: "bob-usd", UserID: "bob", Currency: "USD", Balance: 0},
		},
		transfers: map[string]storedTransfer{},
	}
}

func (s *fakeStore) WithinTx(_ context.Context, fn func(repository.Tx) error) error {
	tx := &fakeTx{store: s, accounts: maps.Clone(s.accounts), transfers: maps.Clone(s.transfers)}
	if err := fn(tx); err != nil {
		return err
	}
	s.accounts, s.transfers = tx.accounts, tx.transfers
	s.ledger = append(s.ledger, tx.ledger...)
	return nil
}

type fakeTx struct {
	store     *fakeStore
	accounts  map[string]entities.Account
	transfers map[string]storedTransfer
	ledger    []entities.LedgerEntry
}

func (tx *fakeTx) Accounts() repository.AccountRepository   { return fakeAccounts{tx} }
func (tx *fakeTx) Transfers() repository.TransferRepository { return fakeTransfers{tx} }
func (tx *fakeTx) Ledger() repository.LedgerRepository      { return fakeLedger{tx} }

type fakeAccounts struct{ tx *fakeTx }

func (a fakeAccounts) LockForTransfer(_ context.Context, ids ...string) (map[string]*entities.Account, error) {
	if a.tx.store.lockErr != nil {
		return nil, a.tx.store.lockErr
	}
	locked := map[string]*entities.Account{}
	for _, id := range ids {
		if account, ok := a.tx.accounts[id]; ok {
			locked[id] = &account
		}
	}
	return locked, nil
}

func (a fakeAccounts) SaveBalances(_ context.Context, accounts ...*entities.Account) error {
	if a.tx.store.saveErr != nil {
		return a.tx.store.saveErr
	}
	for _, account := range accounts {
		a.tx.accounts[account.ID] = *account
	}
	return nil
}

func (fakeAccounts) FindOwned(context.Context, string, string) (entities.Account, error) {
	panic("not used by the transfer service")
}

func (fakeAccounts) ListByUser(context.Context, string) ([]entities.Account, error) {
	panic("not used by the transfer service")
}

type fakeTransfers struct{ tx *fakeTx }

func (f fakeTransfers) CreateIdempotent(_ context.Context, t repository.NewTransfer) (entities.Transfer, bool, error) {
	if f.tx.store.insertErr != nil {
		return entities.Transfer{}, false, f.tx.store.insertErr
	}
	id := t.UserID + "/" + t.IdempotencyKey
	if existing, ok := f.tx.transfers[id]; ok {
		if existing.request != t.Request {
			return entities.Transfer{}, false, repository.ErrIdempotencyConflict
		}
		return existing.transfer, false, nil
	}
	saved := entities.Transfer{
		ID: "transfer-" + t.IdempotencyKey, IdempotencyKey: t.IdempotencyKey,
		FromAccountID: t.Request.FromAccountID, ToAccountID: t.Request.ToAccountID,
		Amount: t.Request.Amount, Currency: t.Request.Currency,
		Status: t.Status, FailureReason: t.FailureReason,
	}
	f.tx.transfers[id] = storedTransfer{saved, t.Request}
	return saved, true, nil
}

func (f fakeTransfers) FindByID(context.Context, string, string) (entities.Transfer, error) {
	panic("not used inside transactions")
}

type fakeLedger struct{ tx *fakeTx }

func (l fakeLedger) Append(_ context.Context, entries ...entities.LedgerEntry) error {
	if l.tx.store.ledgerErr != nil {
		return l.tx.store.ledgerErr
	}
	l.tx.ledger = append(l.tx.ledger, entries...)
	return nil
}

func request(amount int64) entities.CreateTransfer {
	return entities.CreateTransfer{FromAccountID: "alice-pen", ToAccountID: "bob-pen", Amount: amount, Currency: "PEN"}
}

func TestCreateMovesMoneyAndWritesLedger(t *testing.T) {
	store := newFakeStore()
	s := NewService(store, nil)

	transfer, created, err := s.Create(context.Background(), "alice", "key-1", request(300))
	if err != nil || !created || transfer.Status != valueobjects.TransferStatusCompleted {
		t.Fatalf("created=%v status=%s err=%v", created, transfer.Status, err)
	}
	if store.accounts["alice-pen"].Balance != 700 || store.accounts["bob-pen"].Balance != 300 {
		t.Fatalf("unexpected balances: %+v", store.accounts)
	}
	if len(store.ledger) != 2 || store.ledger[0].Amount+store.ledger[1].Amount != 0 {
		t.Fatalf("ledger must have a balanced pair of entries: %+v", store.ledger)
	}
	if store.ledger[0].BalanceAfter != 700 || store.ledger[1].BalanceAfter != 300 {
		t.Fatalf("ledger must record resulting balances: %+v", store.ledger)
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	store := newFakeStore()
	s := NewService(store, nil)

	first, created, err := s.Create(context.Background(), "alice", "key-1", request(300))
	if err != nil || !created {
		t.Fatal(err)
	}
	for range 3 {
		retry, created, err := s.Create(context.Background(), "alice", "key-1", request(300))
		if err != nil || created || retry.ID != first.ID {
			t.Fatalf("retry must replay the original: created=%v err=%v", created, err)
		}
	}
	if store.accounts["alice-pen"].Balance != 700 || len(store.ledger) != 2 {
		t.Fatal("retries moved money more than once")
	}

	if _, _, err := s.Create(context.Background(), "alice", "key-1", request(301)); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("reusing a key with another body must conflict, got %v", err)
	}
}

func TestCreateWithInsufficientFundsIsRecordedAsFailed(t *testing.T) {
	store := newFakeStore()
	s := NewService(store, nil)

	transfer, created, err := s.Create(context.Background(), "alice", "key-1", request(5000))
	if err != nil || !created {
		t.Fatal(err)
	}
	if transfer.Status != valueobjects.TransferStatusFailed || transfer.FailureReason != valueobjects.FailureReasonInsufficientFunds {
		t.Fatalf("expected a failed transfer, got %+v", transfer)
	}
	if store.accounts["alice-pen"].Balance != 1000 || store.accounts["bob-pen"].Balance != 0 || len(store.ledger) != 0 {
		t.Fatal("a failed transfer must not touch balances")
	}

	// Even if Alice gets money later, the retry keeps the original answer.
	alice := store.accounts["alice-pen"]
	alice.Balance = 10_000
	store.accounts["alice-pen"] = alice
	retry, created, err := s.Create(context.Background(), "alice", "key-1", request(5000))
	if err != nil || created || retry.Status != valueobjects.TransferStatusFailed {
		t.Fatalf("retry changed the outcome: created=%v status=%s err=%v", created, retry.Status, err)
	}
}

func TestCreateRejections(t *testing.T) {
	dbError := errors.New("database unavailable")
	for _, tc := range []struct {
		name    string
		userID  string
		request entities.CreateTransfer
		setup   func(*fakeStore)
		wantErr error
	}{
		{"empty identity", "", request(1), nil, ErrForbidden},
		{"invalid request", "alice", request(0), nil, entities.ErrInvalidTransfer},
		{"someone else's account", "bob", request(1), nil, ErrForbidden},
		{"unknown source", "alice", entities.CreateTransfer{FromAccountID: "ghost", ToAccountID: "bob-pen", Amount: 1, Currency: "PEN"}, nil, ErrForbidden},
		{"unknown destination", "alice", entities.CreateTransfer{FromAccountID: "alice-pen", ToAccountID: "ghost", Amount: 1, Currency: "PEN"}, nil, ErrDestinationNotFound},
		{"currency mismatch", "alice", entities.CreateTransfer{FromAccountID: "alice-pen", ToAccountID: "bob-usd", Amount: 1, Currency: "PEN"}, nil, ErrCurrencyMismatch},
		{"request currency differs", "alice", entities.CreateTransfer{FromAccountID: "alice-pen", ToAccountID: "bob-pen", Amount: 1, Currency: "USD"}, nil, ErrCurrencyMismatch},
		{"credit overflow", "alice", request(1), func(s *fakeStore) {
			bob := s.accounts["bob-pen"]
			bob.Balance = 1<<63 - 1
			s.accounts["bob-pen"] = bob
		}, entities.ErrBalanceOverflow},
		{"lock error", "alice", request(1), func(s *fakeStore) { s.lockErr = dbError }, dbError},
		{"insert error", "alice", request(1), func(s *fakeStore) { s.insertErr = dbError }, dbError},
		{"save error", "alice", request(1), func(s *fakeStore) { s.saveErr = dbError }, dbError},
		{"ledger error", "alice", request(1), func(s *fakeStore) { s.ledgerErr = dbError }, dbError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			if tc.setup != nil {
				tc.setup(store)
			}
			before := maps.Clone(store.accounts)
			_, created, err := NewService(store, nil).Create(context.Background(), tc.userID, "key-1", tc.request)
			if !errors.Is(err, tc.wantErr) || created {
				t.Fatalf("created=%v err=%v", created, err)
			}
			if !maps.Equal(before, store.accounts) || len(store.transfers) != 0 || len(store.ledger) != 0 {
				t.Fatal("a rejected transfer must leave no trace")
			}
		})
	}
}

type readerStub struct {
	transfer entities.Transfer
	err      error
	userID   string
}

func (r *readerStub) CreateIdempotent(context.Context, repository.NewTransfer) (entities.Transfer, bool, error) {
	panic("not used")
}

func (r *readerStub) FindByID(_ context.Context, userID, _ string) (entities.Transfer, error) {
	r.userID = userID
	return r.transfer, r.err
}

func TestGet(t *testing.T) {
	reader := &readerStub{transfer: entities.Transfer{ID: "t-1"}}
	transfer, err := NewService(nil, reader).Get(context.Background(), "alice", "t-1")
	if err != nil || transfer.ID != "t-1" || reader.userID != "alice" {
		t.Fatalf("transfer=%+v err=%v", transfer, err)
	}

	reader.err = repository.ErrNotFound
	if _, err := NewService(nil, reader).Get(context.Background(), "alice", "t-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
