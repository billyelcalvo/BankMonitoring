# BankMonitoring — Bank Transfer API

[![CI](https://github.com/billyelcalvo/BankMonitoring/actions/workflows/ci.yml/badge.svg)](https://github.com/billyelcalvo/BankMonitoring/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)
![Coverage](https://img.shields.io/badge/coverage-95%25-brightgreen)
![PostgreSQL](https://img.shields.io/badge/postgres-17-336791?logo=postgresql)

A money transfer service in Go and PostgreSQL that stays correct when many
requests fight over the same accounts and when clients retry the same request.

- **Live API:** https://bankmonitoring-api.onrender.com
- **Interactive docs (Swagger UI):** https://bankmonitoring-api.onrender.com/docs
- **OpenAPI spec:** [`api/openapi.yaml`](api/openapi.yaml), also served at `/openapi.yaml`
- **Demo users:** `alice@example.com`, `bob@example.com`, `carol@example.com` (password `demo-password`)

> The free Render instance sleeps when idle, so the first request after a
> while can take ~30 s to wake it up.

---

## The problem

Moving money looks simple: subtract here, add there. It gets hard when:

1. **Two transfers from the same account arrive at the same time.** Both read a
   balance of 100, both decide 80 is affordable, both write. The account
   spent 160 out of 100. This is the classic *double spend* race.
2. **The client retries.** A mobile app times out after the server already
   moved the money, so it sends the request again. Without protection, the
   transfer happens twice.

This service guarantees that, under concurrency and retries:

- a balance never goes below zero and the same money is never spent twice;
- each logical transfer executes **exactly once**, no matter how many times it
  is sent, including retries sent in parallel;
- every movement is recorded in a double-entry ledger that reconciles with the
  balances.

## Architecture

```mermaid
flowchart LR
    client([Client]) -->|HTTPS + JWT| mw

    subgraph api["HTTP layer · internal/httpapi"]
        mw["Middleware<br/>recover · request ID · access log · timeout"]
        authz["JWT auth + permission check"]
        handlers["Handlers<br/>/auth/login · /accounts · /transfers"]
        mw --> authz --> handlers
    end

    subgraph svc["Service layer · internal/service"]
        transfer["transfer.Service<br/>locks, rules, idempotency"]
        account["account.Service"]
        login["auth.LoginService<br/>bcrypt + token issuing"]
    end

    subgraph domain["Domain · internal/domain"]
        entities["Entities<br/>Account.Debit / Credit<br/>CreateTransfer.Validate"]
        contracts["Repository contracts<br/>TxManager · Tx"]
    end

    subgraph repo["Repository · internal/repository"]
        pg["pgx implementation<br/>Store.WithinTx · embedded migrations"]
    end

    handlers --> transfer & account & login
    transfer & account & login --> entities
    transfer & account & login --> contracts
    pg -. implements .-> contracts
    pg --> db[(PostgreSQL<br/>accounts · transfers<br/>ledger_entries · users)]
```

Dependencies point inwards: handlers know services, services know the domain
and the repository **interfaces**, and only `internal/repository` knows pgx and
SQL. That's why the service can be unit tested with an in-memory store
([`service_test.go`](internal/service/transfer/service_test.go)) and the same
code runs against PostgreSQL in the integration tests.

### What happens on `POST /transfers`

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant H as Handler
    participant S as transfer.Service
    participant DB as PostgreSQL

    C->>H: POST /transfers (JWT, Idempotency-Key)
    H->>H: verify JWT, permission, key format, body
    H->>S: Create(userID from JWT, key, request)
    S->>DB: BEGIN (READ COMMITTED)
    S->>DB: SELECT … FROM accounts WHERE id IN (from, to)<br/>ORDER BY id FOR UPDATE
    Note over S,DB: concurrent transfers touching these<br/>accounts wait here, one at a time
    S->>S: owner? same currency? enough balance?
    S->>DB: INSERT INTO transfers … ON CONFLICT (user_id, idempotency_key) DO NOTHING
    alt key already used (retry)
        S->>DB: SELECT stored transfer, compare request
        S->>DB: COMMIT (nothing moved)
        S-->>H: stored transfer → 200 + Idempotent-Replayed (409 if body differs)
    else new and funded
        S->>DB: UPDATE accounts (both balances)
        S->>DB: INSERT 2 ledger entries (-amount, +amount)
        S->>DB: COMMIT
        S-->>H: completed → 201
    else new but insufficient funds
        S->>DB: COMMIT (transfer stored as failed)
        S-->>H: failed → 422
    end
```

## Design decisions and trade-offs

**Pessimistic locking (`SELECT … FOR UPDATE`) instead of optimistic versioning.**
Transfers concentrate on a few hot accounts (payroll, merchants). With
optimistic locking, every collision is a failed attempt that has to be retried,
and under heavy contention most of the work is wasted. Row locks make
competitors wait in line instead, so each one succeeds on the first try. The
cost is that throughput for a *single* account pair is bounded by how fast one
transaction can run, as the load test shows (~1,650 transfers/s on one pair).
Transfers between different accounts don't block each other.

**Locks are taken in ascending account ID order.** A→B and B→A at the same time
is the textbook deadlock. Sorting the IDs before `FOR UPDATE` means every
transaction acquires locks in the same order, so a cycle can't form. The
integration suite runs 1,000 transfers in both directions to prove it.

**READ COMMITTED, not SERIALIZABLE.** Correctness comes from the row locks,
not from the isolation level. SERIALIZABLE would also be correct, but it aborts
conflicting transactions and pushes retry loops into the application.

**Idempotency lives in the same transaction as the money movement.** The
`UNIQUE (user_id, idempotency_key)` constraint decides who wins. If two copies
of the same request race, the second `INSERT` waits for the first transaction
and then does nothing, so there is no window where both can move money.
Keeping the key in Redis or another store would need a distributed lock and
could disagree with the database after a crash.

**Keys are scoped per user and tied to the request body.** One user can't
read another user's transfer by guessing a key, and reusing a key with a
different amount or account is answered with `409` instead of silently
returning the old result.

**Failed transfers are stored too.** A transfer rejected for insufficient funds
is saved as `failed`, so a retry gets the same `422` even if the account was
topped up in between. A retry should never turn into a different operation.

**Money as integers in the smallest unit.** `amount: 1050` is 10.50 PEN. No
floating point, and overflow is checked on credit.

**Double-entry ledger + a database CHECK.** Every completed transfer writes a
debit and a credit that sum to zero, so any balance can be rebuilt from the
ledger. `CHECK (balance >= 0)` is a last line of defence: if a bug ever got
past the service, PostgreSQL would abort the transaction instead of storing an
overdraft.

**Ownership is checked on the locked row, and a missing source answers 403.**
"Doesn't exist" and "isn't yours" look the same from outside, so the API
can't be used to probe which account IDs exist.

**Synchronous transfers.** The client gets the final result in the same
request. A queue-based design would absorb bigger spikes on a single hot
account, but it adds eventual consistency, a worker and a status-polling flow
to the client. For p95 < 100 ms at this scale, it isn't needed.

**Short-lived JWTs (HS256, 15 min) without refresh tokens.** Tokens carry the
user ID and permissions from the `users` table. Revocation means waiting for
expiry; refresh tokens and key rotation are the natural next step.

**Migrations run on startup.** golang-migrate takes a PostgreSQL advisory lock,
so several replicas starting at once is safe. Migrations are embedded in the
binary, versioned (`000001_…up.sql` / `…down.sql`) and can be turned off with
`RUN_MIGRATIONS=false` if a separate release step is preferred.

## Results

### Correctness under concurrency (integration tests, real PostgreSQL)

| Scenario | Result |
| --- | --- |
| 1,000 transfers of 100 released at the same instant from an account holding 10,000 | exactly **100 completed, 900 rejected (422)**, final balance **0**, ledger reconciled, in ~0.6 s |
| Same test with `FOR UPDATE` removed (sanity check that the test catches races) | **175** completed: 75 double spends, test fails |
| 1,000 transfers in both directions between 3 accounts | no deadlocks, no 500s, total money unchanged, ledger reconciled |
| 50 transfers × 20 parallel retries each (1,000 requests) | **50 created, 950 replays**, one transfer per key, money moved exactly 50 times |
| Retry of a failed transfer after topping up the account | still `422`, nothing moved |

### Load test (k6)

All virtual users move money between **the same two accounts**, the worst case
for lock contention, and about 10% of iterations immediately retry with the
same key. Run against the Docker Compose stack (API + PostgreSQL + k6 on one
machine, 8 vCPU / 8 GB for Docker), 30–60 s at steady load.

| Virtual users | Throughput | p50 | p95 | p99 | Errors | Replays verified | Duplicate moves |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | ~1,690 req/s | 4.3 ms | **10.1 ms** | 13.0 ms | 0% | 6,930 | 0 |
| 50 | ~1,525 req/s | 26.6 ms | **57.3 ms** | 81.8 ms | 0% | 10,431 | 0 |
| 100 | ~1,635 req/s | 53.2 ms | **80.1 ms** | 98.9 ms | 0% | 6,710 | 0 |

```mermaid
xychart-beta
    title "POST /transfers p95 latency on one hot account pair"
    x-axis "Virtual users" [10, 50, 100]
    y-axis "p95 (ms)" 0 --> 100
    bar [10.1, 57.3, 80.1]
    line [100, 100, 100]
```

Throughput stays flat at ~1,600–1,700 transfers/s while latency grows with the
number of users. That is the row lock doing its job: transfers on the same
pair run one after another, so extra users only make the queue longer. The
p95 target of **100 ms** holds even at 100 concurrent users on a single pair.
After each run, the teardown checks that the money held by both accounts is
unchanged, and SQL reconciliation shows every balance equals its opening
balance plus its ledger entries.

> **About the live demo on Render.** The numbers above were measured locally.
> The public instance runs on Render's free plan (a fraction of a shared CPU)
> in Oregon, so requests made from far away are slower: most of the time is
> the network round trip (~200 ms from South America), and the small CPU
> adds delay when several requests arrive at once. Inside the server a
> transfer still takes about **7 ms**. Every response includes a
> `Server-Timing` header with the time spent in the API, so you can tell
> server time from network time.

### Other numbers

| | |
| --- | --- |
| Test coverage (unit + integration, `./internal/...`) | **95%** (CI fails below 70%) |
| Runtime image | ~21 MB (distroless, non-root) |
| Cold start to ready (migrations + seed) | ~0.2 s locally |

## Running it

### Everything with one command

```sh
docker compose up --build
```

This starts PostgreSQL, applies migrations, seeds the demo users and serves
the API on http://localhost:8080 (docs at http://localhost:8080/docs).

```sh
# 1. log in
TOKEN=$(curl -s localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"demo-password"}' | jq -r .access_token)

# 2. move 10.50 PEN from Alice to Bob
KEY=$(uuidgen)
curl -i localhost:8080/transfers \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"from_account_id":"acc-alice-pen","to_account_id":"acc-bob-pen","amount":1050,"currency":"PEN"}'

# 3. send exactly the same request again: 200 + Idempotent-Replayed: true, nothing moves
# 4. check balances
curl -s localhost:8080/accounts -H "Authorization: Bearer $TOKEN" | jq
```

### Tests

```sh
go test ./...                                   # unit tests, no Docker needed
go test -race -tags integration ./...           # + integration tests (needs Docker)
```

Integration tests start their own PostgreSQL with
[Testcontainers](https://golang.testcontainers.org/), so nothing else has to be
running.

### Load test

```sh
docker compose up -d --build
docker compose --profile loadtest run --rm k6                         # 50 VUs, 60 s
LOAD_VUS=100 LOAD_DURATION=30s docker compose --profile loadtest run --rm k6
```

The k6 run fails if p95 ≥ 100 ms, errors ≥ 1%, or any retry moves money twice.
A Markdown summary is written to `loadtest/results/summary.md`. To hit
another environment: `BASE_URL=https://… k6 run loadtest/transfers.js`.

### Without Docker

```sh
export DATABASE_URL='postgres://user:pass@localhost:5432/bank?sslmode=disable'
export JWT_SECRET="$(openssl rand -base64 32)"
export SEED_DEMO_DATA=true DEMO_PASSWORD=demo-password
go run ./cmd/api
```

## Configuration

Everything comes from environment variables; see [`.env.example`](.env.example).
The app has no default for secrets and refuses to start without them.

| Variable | Default | Notes |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `JWT_SECRET` | required | base64, at least 32 bytes (`openssl rand -base64 32`) |
| `HTTP_ADDR` / `PORT` | `:8080` | `PORT` is what Render and Fly inject |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `bankmonitoring` / `bankmonitoring-api` | |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DB_MAX_CONNS` | `20` | pool size |
| `REQUEST_TIMEOUT` | `10s` | deadline for each request, database calls included |
| `SHUTDOWN_TIMEOUT` | `10s` | graceful shutdown on SIGTERM |
| `RUN_MIGRATIONS` | `true` | apply pending migrations on startup |
| `SEED_DEMO_DATA` | `false` | create demo users and funded accounts |
| `DEMO_PASSWORD` | — | required when seeding, min. 8 characters |

Logs are JSON on stdout, one line per request with `request_id`, `method`,
`route`, `status` and `duration_ms`. `X-Request-ID` is accepted from the
caller or generated, and returned on every response.

## API

| Method | Path | Permission | Description |
| --- | --- | --- | --- |
| `POST` | `/auth/login` | — | email + password → JWT (15 min) |
| `GET` | `/me` | any token | identity and permissions in the token |
| `GET` | `/accounts` | `accounts:read` | the caller's accounts and balances |
| `GET` | `/accounts/{id}` | `accounts:read` | one account (404 if not the caller's) |
| `POST` | `/transfers` | `transfers:create` | create a transfer; requires `Idempotency-Key` (UUID) |
| `GET` | `/transfers/{id}` | `transfers:read` | a transfer created by the caller |
| `GET` | `/health` | — | liveness |
| `GET` | `/ready` | — | readiness (pings the database) |
| `GET` | `/docs`, `/openapi.yaml` | — | Swagger UI and the OpenAPI 3 spec |

`POST /transfers` answers:

| Status | Meaning |
| --- | --- |
| `201` | completed |
| `200` | retry of a completed transfer (`Idempotent-Replayed: true`), nothing moved |
| `422` | stored as failed for insufficient funds (same answer on retries), unknown destination or currency mismatch |
| `409` | the key was already used with a different request |
| `400` / `413` / `415` | invalid header or body, body over 64 KiB, wrong content type |
| `401` / `403` | missing or invalid token, missing permission or not the source owner |

## CI/CD and deployment

[GitHub Actions](.github/workflows/ci.yml) runs on every push and pull request:

1. **lint**: golangci-lint (gofmt, goimports, govet, gosec, errorlint, …)
2. **test**: unit + integration tests with `-race` against PostgreSQL in
   Testcontainers, failing below 70% coverage
3. **docker**: builds the image and boots the compose stack exactly as
   documented above, then logs in and lists accounts

Deployment is a [Render Blueprint](render.yaml): a Docker web service and a
PostgreSQL database. Render generates `JWT_SECRET`, uses `/ready` as its health
check and only deploys commits whose CI checks passed.

## Project layout

```text
cmd/api/                     entry point, wiring, graceful shutdown, healthcheck
api/openapi.yaml             OpenAPI 3 spec (embedded in the binary)
internal/config/             environment-based configuration
internal/httpapi/            routes, handlers, middleware
internal/service/transfer/   transfer orchestration (locks, rules, idempotency)
internal/service/account/    account queries
internal/service/auth/       JWT, login, permission middleware
internal/domain/             entities, value objects, repository contracts
internal/repository/         pgx implementation and versioned migrations
internal/seed/               optional demo data
test/integration/            end-to-end concurrency and idempotency tests
loadtest/                    k6 script and latest results
```
