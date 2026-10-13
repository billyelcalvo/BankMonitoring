package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/service/auth"
)

type TransferService interface {
	Create(ctx context.Context, userID, key string, request entities.CreateTransfer) (entities.Transfer, bool, error)
	Get(ctx context.Context, userID, transferID string) (entities.Transfer, error)
}

type AccountService interface {
	List(ctx context.Context, userID string) ([]entities.Account, error)
	Get(ctx context.Context, userID, accountID string) (entities.Account, error)
}

type Authenticator interface {
	Login(ctx context.Context, email, password string) (string, error)
}

type Dependencies struct {
	Tokens         *auth.TokenService
	Login          Authenticator
	Transfers      TransferService
	Accounts       AccountService
	Ready          func(context.Context) error
	Logger         *slog.Logger
	RequestTimeout time.Duration
}

func NewHandler(d Dependencies) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.RequestTimeout <= 0 {
		d.RequestTimeout = 10 * time.Second
	}
	protect := func(permission auth.Permission, h http.HandlerFunc) http.Handler {
		return d.Tokens.Authenticate(auth.RequirePermission(permission, h))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /ready", ready(d.Ready))
	mux.HandleFunc("GET /openapi.yaml", openAPISpec)
	mux.HandleFunc("GET /docs", swaggerUI)
	mux.HandleFunc("POST /auth/login", login(d.Login, d.Logger))
	mux.Handle("GET /me", d.Tokens.Authenticate(http.HandlerFunc(me)))
	mux.Handle("GET /accounts", protect(auth.PermissionAccountsRead, listAccounts(d.Accounts, d.Logger)))
	mux.Handle("GET /accounts/{id}", protect(auth.PermissionAccountsRead, getAccount(d.Accounts, d.Logger)))
	mux.Handle("POST /transfers", protect(auth.PermissionTransfersCreate, createTransfer(d.Transfers, d.Logger)))
	mux.Handle("GET /transfers/{id}", protect(auth.PermissionTransfersRead, getTransfer(d.Transfers, d.Logger)))

	// Outermost first: recover from panics, tag the request, log it, then
	// put a deadline on everything downstream (including database calls).
	return recoverPanics(d.Logger, requestID(accessLog(d.Logger, withTimeout(d.RequestTimeout, mux))))
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready checks the database too, so a load balancer stops routing traffic to
// an instance that lost its connection.
func ready(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := check(ctx); err != nil {
				writeError(w, http.StatusServiceUnavailable, "database unavailable")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func me(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		UserID      string            `json:"user_id"`
		Permissions []auth.Permission `json:"permissions"`
	}{UserID: claims.Subject, Permissions: claims.Permissions})
}
