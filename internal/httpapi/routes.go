package httpapi

import (
	"encoding/json"
	"net/http"

	"bankmonitoring/internal/service/auth"
)

func NewHandler(tokens *auth.TokenService, transfers TransferCreator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("GET /me", tokens.Authenticate(http.HandlerFunc(me)))
	mux.Handle("POST /transfers", tokens.Authenticate(
		auth.RequirePermission(auth.PermissionTransfersCreate, createTransfer(transfers))))
	return mux
}

func me(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		UserID      string            `json:"user_id"`
		Permissions []auth.Permission `json:"permissions"`
	}{UserID: claims.Subject, Permissions: claims.Permissions})
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
}
