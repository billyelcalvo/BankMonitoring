package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"bankmonitoring/internal/domain/entities"
	"bankmonitoring/internal/domain/repository"
	"bankmonitoring/internal/service/auth"
	"bankmonitoring/internal/service/transfer"

	"github.com/jackc/pgx/v5/pgtype"
)

type TransferCreator interface {
	Create(context.Context, string, string, entities.CreateTransfer) (entities.Transfer, bool, error)
}

func createTransfer(service TransferCreator) http.HandlerFunc {
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
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request *entities.CreateTransfer
		if err := decoder.Decode(&request); err != nil {
			writeBodyError(w, err)
			return
		}
		if request == nil {
			writeError(w, http.StatusBadRequest, "request must be a JSON object")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeBodyError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, created, err := service.Create(ctx, claims.Subject, key, *request)
		switch {
		case errors.Is(err, transfer.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, repository.ErrInvalidTransfer):
			writeError(w, http.StatusBadRequest, "invalid transfer request")
		case errors.Is(err, repository.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency key already used with a different request")
		case err != nil:
			writeError(w, http.StatusInternalServerError, "could not create transfer")
		default:
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(result)
		}
	}
}

func writeBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON request")
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
