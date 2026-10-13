package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"bankmonitoring/internal/service/auth"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func login(service Authenticator, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var request loginRequest
		if !decodeJSON(w, r, &request) {
			return
		}
		token, err := service.Login(r.Context(), request.Email, request.Password)
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid email or password")
		case err != nil:
			logger.ErrorContext(r.Context(), "login failed",
				"request_id", requestIDFrom(r.Context()), "error", err)
			writeError(w, http.StatusInternalServerError, "could not log in")
		default:
			writeJSON(w, http.StatusOK, loginResponse{
				AccessToken: token, TokenType: "Bearer", ExpiresIn: int(auth.AccessTokenTTL.Seconds()),
			})
		}
	}
}
