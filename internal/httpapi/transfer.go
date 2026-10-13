package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/domain/valueobjects"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"

	"github.com/jackc/pgx/v5/pgtype"
)

func createTransfer(service TransferService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeError(w, http.StatusBadRequest, "a single Idempotency-Key header is required")
			return
		}
		key := strings.TrimSpace(keys[0])
		var uuid pgtype.UUID
		if err := uuid.Scan(key); err != nil || !uuid.Valid || uuid.Bytes == [16]byte{} {
			writeError(w, http.StatusBadRequest, "Idempotency-Key must be a nonzero UUID")
			return
		}
		var request entities.CreateTransfer
		if !decodeJSON(w, r, &request) {
			return
		}

		result, created, err := service.Create(r.Context(), claims.Subject, key, request)
		switch {
		case errors.Is(err, transfer.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, entities.ErrInvalidTransfer):
			writeError(w, http.StatusBadRequest, "invalid transfer request")
		case errors.Is(err, repository.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency key already used with a different request")
		case errors.Is(err, transfer.ErrDestinationNotFound):
			writeError(w, http.StatusUnprocessableEntity, "destination account not found")
		case errors.Is(err, transfer.ErrCurrencyMismatch):
			writeError(w, http.StatusUnprocessableEntity, "currency does not match the accounts")
		case errors.Is(err, entities.ErrBalanceOverflow):
			writeError(w, http.StatusUnprocessableEntity, "destination balance limit reached")
		case err != nil:
			logger.ErrorContext(r.Context(), "create transfer failed",
				"request_id", requestIDFrom(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "could not create transfer")
		default:
			if !created {
				w.Header().Set("Idempotent-Replayed", "true")
			}
			writeJSON(w, transferStatusCode(result, created), result)
		}
	}
}

// transferStatusCode keeps retries consistent: a failed transfer answers 422
// every time, and a completed one answers 201 first and 200 on replays.
func transferStatusCode(t entities.Transfer, created bool) int {
	switch {
	case t.Status == valueobjects.TransferStatusFailed:
		return http.StatusUnprocessableEntity
	case created:
		return http.StatusCreated
	default:
		return http.StatusOK
	}
}

func getTransfer(service TransferService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := auth.ClaimsFromContext(r.Context())
		result, err := service.Get(r.Context(), claims.Subject, r.PathValue("id"))
		switch {
		case errors.Is(err, transfer.ErrNotFound):
			writeError(w, http.StatusNotFound, "transfer not found")
		case err != nil:
			logger.ErrorContext(r.Context(), "get transfer failed",
				"request_id", requestIDFrom(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "could not load transfer")
		default:
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, result)
		}
	}
}
