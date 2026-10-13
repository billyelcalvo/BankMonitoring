package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/service/account"
	"bankmonitoring/internal/service/auth"
)

func listAccounts(service AccountService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.ClaimsFromContext(r.Context())
		accounts, err := service.List(r.Context(), claims.Subject)
		if err != nil {
			logger.ErrorContext(r.Context(), "list accounts failed",
				"request_id", requestIDFrom(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "could not load accounts")
			return
		}
		if accounts == nil {
			accounts = []entities.Account{}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
	}
}

func getAccount(service AccountService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.ClaimsFromContext(r.Context())
		result, err := service.Get(r.Context(), claims.Subject, r.PathValue("id"))
		switch {
		case errors.Is(err, account.ErrNotFound):
			writeError(w, http.StatusNotFound, "account not found")
		case err != nil:
			logger.ErrorContext(r.Context(), "get account failed",
				"request_id", requestIDFrom(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "could not load account")
		default:
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, result)
		}
	}
}
