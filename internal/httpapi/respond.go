package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

const maxBodyBytes = 64 << 10

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// decodeJSON accepts exactly one JSON object with known fields only. It writes
// the error response itself and returns false when the body is unusable.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request, dst *T) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var value *T
	if err := decoder.Decode(&value); err != nil {
		writeBodyError(w, err)
		return false
	}
	if value == nil {
		writeError(w, http.StatusBadRequest, "request must be a JSON object")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeBodyError(w, err)
		return false
	}
	*dst = *value
	return true
}

func writeBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON request")
}
