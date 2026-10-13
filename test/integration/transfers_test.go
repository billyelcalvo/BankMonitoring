//go:build integration

// These tests drive the full stack (HTTP handler -> service -> PostgreSQL)
// with many requests released at the same instant, to show the invariants
// hold under real lock contention and not just in unit tests.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/httpapi"
	"bankmonitoring/internal/repository"
	"bankmonitoring/internal/service/account"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"
	"bankmonitoring/internal/testutil/pgtest"

	"github.com/jackc/pgx/v5/pgxpool"
)

var databaseURL string

func TestMain(m *testing.M) { pgtest.Main(m, &databaseURL) }

type app struct {
	handler http.Handler
	pool    *pgxpool.Pool
	tokens  *auth.TokenService
}

func newApp(t *testing.T) app {
	t.Helper()
	pool := pgtest.Pool(t, databaseURL, 20)
	tokens, err := auth.NewTokenService([]byte(strings.Repeat("k", 32)), "bankmonitoring", "bankmonitoring-api")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(httpapi.Dependencies{
		Tokens:    tokens,
		Login:     auth.NewLoginService(repository.NewUserRepository(pool), tokens),
		Transfers: transfer.NewService(repository.NewStore(pool), repository.NewTransferRepository(pool)),
		Accounts:  account.NewService(repository.NewAccountRepository(pool)),
		Ready:     pool.Ping,
	})
	return app{handler: handler, pool: pool, tokens: tokens}
}

func (a app) token(t *testing.T, userID string) string {
	t.Helper()
	raw, err := a.tokens.Issue(userID, []auth.Permission{auth.PermissionTransfersCreate, auth.PermissionTransfersRead})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (a app) transfer(token, key, from, to string, amount int64) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"from_account_id":%q,"to_account_id":%q,"amount":%d,"currency":"PEN"}`, from, to, amount)
	r := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	return w
}

func newKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// fireAtOnce runs n requests, all released together by closing a channel, and
// counts the status codes they got back.
func fireAtOnce(n int, request func(i int) int) map[int]int {
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	statuses := map[int]int{}
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code := request(i)
			mu.Lock()
			statuses[code]++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	return statuses
}

// 1,000 transfers race to drain the same account. Alice can afford exactly
// 100 of them; if any balance read were stale we'd see more than 100
// successes and a negative balance (or a constraint error turned into 500s).
func TestThousandConcurrentTransfersNeverOverspend(t *testing.T) {
	a := newApp(t)
	pgtest.CreateUser(t, a.pool, "alice", map[string]int64{"alice-pen": 10_000})
	pgtest.CreateUser(t, a.pool, "bob", map[string]int64{"bob-pen": 500})
	token := a.token(t, "alice")

	statuses := fireAtOnce(1000, func(int) int {
		return a.transfer(token, newKey(), "alice-pen", "bob-pen", 100).Code
	})

	if statuses[http.StatusCreated] != 100 || statuses[http.StatusUnprocessableEntity] != 900 || len(statuses) != 2 {
		t.Fatalf("expected 100 completed and 900 rejected, got %v", statuses)
	}
	if got := pgtest.Balance(t, a.pool, "alice-pen"); got != 0 {
		t.Fatalf("alice should end at 0, got %d", got)
	}
	if got := pgtest.Balance(t, a.pool, "bob-pen"); got != 10_500 {
		t.Fatalf("bob should end at 10500, got %d", got)
	}
	assertLedgerReconciles(t, a.pool, map[string]int64{"alice-pen": 10_000, "bob-pen": 500})
}

// Transfers going both ways between three accounts at the same time. Opposite
// directions are the classic deadlock recipe; ordered locking prevents it,
// and money is neither created nor destroyed.
func TestConcurrentTransfersInBothDirectionsKeepTotals(t *testing.T) {
	a := newApp(t)
	opening := map[string]int64{"a": 5_000, "b": 5_000, "c": 5_000}
	for id, balance := range opening {
		pgtest.CreateUser(t, a.pool, "owner-"+id, map[string]int64{id: balance})
	}
	ids := []string{"a", "b", "c"}
	tokens := map[string]string{}
	for _, id := range ids {
		tokens[id] = a.token(t, "owner-"+id)
	}

	statuses := fireAtOnce(1000, func(i int) int {
		from := ids[i%3]
		to := ids[(i+1+i/3%2)%3] // alternate the target so every pair goes both ways
		amount, _ := rand.Int(rand.Reader, big.NewInt(400))
		return a.transfer(tokens[from], newKey(), from, to, amount.Int64()+1).Code
	})
	if statuses[http.StatusInternalServerError] != 0 || statuses[http.StatusCreated] == 0 {
		t.Fatalf("unexpected outcome (a 500 would mean a deadlock or constraint error): %v", statuses)
	}

	var total int64
	for _, id := range ids {
		balance := pgtest.Balance(t, a.pool, id)
		if balance < 0 {
			t.Fatalf("account %s went negative: %d", id, balance)
		}
		total += balance
	}
	if total != 15_000 {
		t.Fatalf("money was created or destroyed: total=%d", total)
	}
	assertLedgerReconciles(t, a.pool, opening)
}

// Clients retry on timeouts, often several times and in parallel. 50 logical
// transfers are each sent 20 times concurrently: exactly one execution per key.
func TestConcurrentDuplicateRetriesExecuteOnce(t *testing.T) {
	a := newApp(t)
	pgtest.CreateUser(t, a.pool, "alice", map[string]int64{"alice-pen": 1_000_000})
	pgtest.CreateUser(t, a.pool, "bob", map[string]int64{"bob-pen": 0})
	token := a.token(t, "alice")

	const transfers, retries = 50, 20
	keys := make([]string, transfers)
	for i := range keys {
		keys[i] = newKey()
	}
	var mu sync.Mutex
	ids := map[string]map[string]bool{}
	statuses := fireAtOnce(transfers*retries, func(i int) int {
		key := keys[i%transfers]
		w := a.transfer(token, key, "alice-pen", "bob-pen", 250)
		var body entities.Transfer
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		mu.Lock()
		if ids[key] == nil {
			ids[key] = map[string]bool{}
		}
		ids[key][body.ID] = true
		mu.Unlock()
		return w.Code
	})

	if statuses[http.StatusCreated] != transfers || statuses[http.StatusOK] != transfers*(retries-1) {
		t.Fatalf("expected %d creations and %d replays, got %v", transfers, transfers*(retries-1), statuses)
	}
	for key, seen := range ids {
		if len(seen) != 1 {
			t.Fatalf("key %s produced several transfers: %v", key, seen)
		}
	}
	if got := pgtest.Balance(t, a.pool, "bob-pen"); got != transfers*250 {
		t.Fatalf("bob should have received exactly %d, got %d", transfers*250, got)
	}
	var stored int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM transfers`).Scan(&stored); err != nil || stored != transfers {
		t.Fatalf("expected %d stored transfers, got %d (%v)", transfers, stored, err)
	}
}

func TestRetriesKeepTheirOriginalAnswer(t *testing.T) {
	a := newApp(t)
	pgtest.CreateUser(t, a.pool, "alice", map[string]int64{"alice-pen": 100})
	pgtest.CreateUser(t, a.pool, "bob", map[string]int64{"bob-pen": 0})
	token := a.token(t, "alice")

	key := newKey()
	if w := a.transfer(token, key, "alice-pen", "bob-pen", 500); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected insufficient funds, got %d %s", w.Code, w.Body.String())
	}
	// Topping up afterwards doesn't turn the old request into a success.
	if _, err := a.pool.Exec(context.Background(), `UPDATE accounts SET balance = 10000 WHERE id = 'alice-pen'`); err != nil {
		t.Fatal(err)
	}
	w := a.transfer(token, key, "alice-pen", "bob-pen", 500)
	if w.Code != http.StatusUnprocessableEntity || w.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry changed outcome: %d %s", w.Code, w.Body.String())
	}
	if w := a.transfer(token, key, "alice-pen", "bob-pen", 501); w.Code != http.StatusConflict {
		t.Fatalf("same key with a different body must be 409, got %d", w.Code)
	}
	if pgtest.Balance(t, a.pool, "bob-pen") != 0 {
		t.Fatal("no money should have moved")
	}
}

func TestOwnershipAndLookups(t *testing.T) {
	a := newApp(t)
	pgtest.CreateUser(t, a.pool, "alice", map[string]int64{"alice-pen": 1000})
	pgtest.CreateUser(t, a.pool, "mallory", map[string]int64{"mallory-pen": 0})

	if w := a.transfer(a.token(t, "mallory"), newKey(), "alice-pen", "mallory-pen", 10); w.Code != http.StatusForbidden {
		t.Fatalf("spending someone else's money must be forbidden, got %d", w.Code)
	}
	if w := a.transfer(a.token(t, "alice"), newKey(), "alice-pen", "nowhere", 10); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown destination should be 422, got %d", w.Code)
	}

	w := a.transfer(a.token(t, "alice"), newKey(), "alice-pen", "mallory-pen", 10)
	var created entities.Transfer
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for user, want := range map[string]int{"alice": 200, "mallory": 404} {
		r := httptest.NewRequest(http.MethodGet, "/transfers/"+created.ID, nil)
		r.Header.Set("Authorization", "Bearer "+a.token(t, user))
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s: expected %d, got %d", user, want, rec.Code)
		}
	}
}

func TestLoginAgainstDatabase(t *testing.T) {
	a := newApp(t)
	hash, err := auth.HashPassword("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `INSERT INTO users (id, email, password_hash, permissions)
		VALUES ('alice', 'alice@example.com', $1, '{accounts:read}')`, hash); err != nil {
		t.Fatal(err)
	}
	for password, want := range map[string]int{"s3cret-pass": 200, "wrong": 401} {
		body := fmt.Sprintf(`{"email":"alice@example.com","password":%q}`, password)
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("password %q: expected %d, got %d", password, want, w.Code)
		}
	}
}

// assertLedgerReconciles checks that every account's balance equals its
// opening balance plus its ledger entries, and that each transfer nets to zero.
func assertLedgerReconciles(t *testing.T, pool *pgxpool.Pool, opening map[string]int64) {
	t.Helper()
	ctx := context.Background()
	for id, start := range opening {
		var moved int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(amount), 0) FROM ledger_entries WHERE account_id = $1`, id).Scan(&moved); err != nil {
			t.Fatal(err)
		}
		if got := pgtest.Balance(t, pool, id); got != start+moved {
			t.Fatalf("account %s: balance %d but ledger says %d", id, got, start+moved)
		}
	}
	var unbalanced, completed, entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT transfer_id FROM ledger_entries GROUP BY transfer_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM transfers WHERE status = 'completed'),
		(SELECT count(*) FROM ledger_entries)`).Scan(&completed, &entries); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 || entries != completed*2 {
		t.Fatalf("ledger out of sync: unbalanced=%d completed=%d entries=%d", unbalanced, completed, entries)
	}
}
