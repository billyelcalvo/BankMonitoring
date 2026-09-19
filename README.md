# BankMonitoring

Go backend using `net/http`, `github.com/golang-jwt/jwt/v5` for JWT, and `pgx/v5` for PostgreSQL.

## Requirements

- Go 1.25 or later (required by pgx v5.11).
- PostgreSQL 13 or later.

## Project structure

```text
cmd/api/main.go                  Entry point, configuration, and server lifecycle
internal/service/auth/          JWT issuance, authentication, and permissions
internal/service/transfer/      Transfer creation and account ownership checks
internal/httpapi/routes.go       HTTP routes and handlers
internal/domain/entities/       Transfer entities
internal/domain/valueobjects/   Transfer statuses
internal/domain/repository/     Domain persistence contracts
internal/repository/            pgx pool and persistence implementation
internal/repository/migrations/ SQL migrations
go.mod                          Module and dependencies
```

## Running the server

```sh
export JWT_SECRET="$(openssl rand -base64 32)"
export DATABASE_URL='postgres://user:password@localhost:5432/bankmonitoring?sslmode=disable'
go run ./cmd/api
```

`JWT_SECRET` is a base64-encoded random key containing at least 32 bytes.
The server rejects missing, invalid, or excessively short keys. Keep the same
key across restarts and instances; changing it invalidates previously issued tokens.
Do not store it in the repository.

The server listens on `:8080`. To change the address in Bash:

```sh
HTTP_ADDR=127.0.0.1:3000 go run ./cmd/api
```

Configuration is read from environment variables; `.env` files are not loaded automatically.

`DATABASE_URL` is read by `repository.NewPool`. The pool is created at startup
and closed when the server shuts down; no `Ping` is performed. The connection
example is intended for local development. Adjust credentials and TLS settings
for your PostgreSQL server.

## Persistence and idempotency

Before using the repository, apply each pending migration once, in order:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f internal/repository/migrations/001_create_transfers.sql
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f internal/repository/migrations/002_create_accounts.sql
```

The application does not run migrations automatically.

The `accounts` table maps `id` (account) to `user_id` (the JWT's `sub`).
These relationships must be provisioned through a trusted server-side process;
they are not created from transfer requests. A nonexistent account or an account
owned by another user returns 403. There is no account creation endpoint.

The frontend must generate a key with `crypto.randomUUID()` for each operation
and reuse it when retrying that operation. The idempotency key is independent
of the transfer ID, which PostgreSQL generates.
It is sent exclusively in the `Idempotency-Key` header. The `Transfer` entity
includes `IdempotencyKey`, read from `transfers.idempotency_key` and returned as
`idempotency_key` in the response. `CreateTransfer` does not include this field.

The service uses the repository as follows:

```go
transfers := repository.NewTransferRepository(pool)
transfer, created, err := transfers.CreateIdempotent(ctx, userID, idempotencyKey, request)
```

`userID` must come from the verified JWT's `sub`. Key lookups are scoped to that
user so that another user cannot retrieve their transfers.

- New key: saves the transfer with `pending` status and returns `created=true`.
- Same key and fields: returns the existing transfer with its current status
  and `created=false`.
- Same key with a different source, destination, amount, currency, or description:
  returns `repository.ErrIdempotencyConflict` (mapped to HTTP 409).
- Invalid UUID, nonpositive amount, empty or identical accounts, or a currency
  without three uppercase letters: returns `repository.ErrInvalidTransfer`.

The original request is stored as JSONB for comparison even if the transfer's
status changes. Comparisons use field values, not the order or whitespace of the
incoming JSON. Do not modify `original_request`, `user_id`, or `idempotency_key`,
or delete records while retries are supported.

The unique constraint on `(user_id, idempotency_key)`, together with
`INSERT ... ON CONFLICT DO NOTHING`, prevents concurrent duplicates. If another
request inserts first, its result is retrieved in a new statement and the
requests are compared. See [PostgreSQL ON CONFLICT](https://www.postgresql.org/docs/current/sql-insert.html).

This implementation persists pending transfers; it does not move funds.
The route validates the JWT and permission, and the service checks account
ownership before calling the repository, including on retries.

## Creating a transfer

`POST /transfers` requires a JWT with `transfers:create` and a source account
associated with its `sub`. Identity comes exclusively from the validated token;
the body does not accept `user_id`, `idempotency_key`, or unknown fields.

```sh
curl -i http://localhost:8080/transfers \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Idempotency-Key: e764bdae-5f99-44a2-8344-9c41c8d48449' \
  -H 'Content-Type: application/json' \
  -d '{"from_account_id":"account-1","to_account_id":"account-2","amount":1000,"currency":"PEN","description":"Payment"}'
```

Use a new key for each operation; the example UUID is for illustration only.
The amount is expressed in the currency's smallest unit. The body limit is 64 KiB.

| HTTP | Result |
| --- | --- |
| 201 | Transfer created with `pending` status. |
| 200 | Identical retry; returns the existing transfer. |
| 400 | Missing or invalid header, invalid JSON, or incorrect data. |
| 401 | Missing, invalid, or expired JWT. |
| 403 | Missing `transfers:create` permission or source account ownership. |
| 409 | Key reused with a different request. |
| 413 | Request body too large. |
| 415 | `Content-Type` other than `application/json`. |
| 500 | Internal error; PostgreSQL details are not exposed. |

## Checking the server

```sh
curl -i http://localhost:8080/health
```

Returns HTTP 200 with `Content-Type: application/json`:

```json
{"status":"ok"}
```

The endpoint indicates that the server is running. Unknown routes return 404,
and unsupported methods return 405. `GET /health` also supports `HEAD` through
`net/http` behavior.

## Development

```sh
go fmt ./...
go vet ./...
go test ./...
go build -o bin/api ./cmd/api
```

Tests cover JWT validation, rejection of tampered or expired tokens, HTTP
authentication, permissions, ownership, transfer creation over HTTP, and idempotency.
Repository tests use simulated responses and do not connect to PostgreSQL;
migrations and actual concurrency require subsequent integration tests.

## Authentication and authorization

A single JWT signed with HS256 contains `sub` (internal user ID), `permissions`,
`iss`, `aud`, `iat`, and `exp`. It is valid for 15 minutes.
The issuer is `bankmonitoring`, and the audience is `bankmonitoring-api`.
The payload is readable: it contains no passwords, email addresses, or banking data.

`auth.TokenService.Issue(userID, permissions)` issues tokens from server-side
code. Call it after verifying the user's credentials and retrieving their
permissions from a trusted source. User storage, a login endpoint, token renewal,
and token revocation are not implemented yet. Permissions included in a token
remain valid until it expires.

`GET /health` is public. `GET /me` requires a token and returns `user_id` and
`permissions`:

```sh
curl -i http://localhost:8080/me \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

`ACCESS_TOKEN` must contain a token issued by the server. The `Authenticate`
middleware validates the signature, algorithm, issuer, audience, and timestamps,
then stores the validated claims in the request context. It returns 401 if the
token is missing or invalid.

The available permissions are `transfers:read` and `transfers:create`. To protect
an operation, combine the middleware in this order:

```go
tokens.Authenticate(auth.RequirePermission(auth.PermissionTransfersCreate, handler))
```

`RequirePermission` returns 403 if the authenticated user lacks the permission.
`POST /transfers` applies this middleware and checks that `accounts.user_id`
matches `sub` for the source account before persisting the transfer.

Validation uses the documented options from
[golang-jwt](https://golang-jwt.github.io/jwt/usage/parse/).

Logs are written as JSON to standard output. On `Ctrl+C` or `SIGTERM`, the server
stops accepting connections and waits up to 10 seconds for active requests to finish.
