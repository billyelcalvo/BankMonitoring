package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestIDIsPropagatedOrGenerated(t *testing.T) {
	h := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(requestIDFrom(r.Context())))
	}))

	w := serve(h, request{method: "GET", path: "/", headers: map[string][]string{"X-Request-ID": {"abc-123"}}})
	if w.Body.String() != "abc-123" || w.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("caller's request ID should be reused, got %q", w.Body.String())
	}

	w = serve(h, request{method: "GET", path: "/", headers: map[string][]string{"X-Request-ID": {"bad id with spaces\n"}}})
	if got := w.Body.String(); len(got) != 16 || got == "bad id with spaces\n" {
		t.Fatalf("unsafe request IDs must be replaced, got %q", got)
	}
}

func TestAccessLogIsStructured(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	routeOf := func(*http.Request) string { return "GET /brew" }
	h := requestID(accessLog(logger, routeOf, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	serve(h, request{method: "GET", path: "/brew"})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %s", buf.String())
	}
	if entry["status"] != float64(418) || entry["path"] != "/brew" || entry["route"] != "GET /brew" || entry["method"] != "GET" || entry["request_id"] == "" {
		t.Fatalf("missing fields in %v", entry)
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatal("missing duration")
	}
}

func TestRecoverPanics(t *testing.T) {
	var buf bytes.Buffer
	h := recoverPanics(slog.New(slog.NewJSONHandler(&buf, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something broke")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError || !bytes.Contains(buf.Bytes(), []byte("something broke")) {
		t.Fatalf("status=%d log=%s", w.Code, buf.String())
	}
}

func TestWithTimeoutSetsDeadline(t *testing.T) {
	h := withTimeout(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("request context has no deadline")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestServerTimingHeader(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	for name, handler := range map[string]http.HandlerFunc{
		"explicit status": func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Millisecond)
			w.WriteHeader(http.StatusCreated)
		},
		"implicit status": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			routeOf := func(*http.Request) string { return "" }
			w := serve(accessLog(logger, routeOf, handler), request{method: http.MethodGet, path: "/"})
			var dur float64
			if _, err := fmt.Sscanf(w.Header().Get("Server-Timing"), "app;dur=%f", &dur); err != nil {
				t.Fatalf("missing or malformed Server-Timing: %q", w.Header().Get("Server-Timing"))
			}
			if name == "explicit status" && dur < 5 {
				t.Fatalf("duration should include the handler's work, got %.1f ms", dur)
			}
		})
	}
}
