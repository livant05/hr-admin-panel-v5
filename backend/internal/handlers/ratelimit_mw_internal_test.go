package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/ratelimit"
)

// The generic limiter middleware (register, ADMS) keeps consume-on-every-request
// semantics: only login refunds successes.
func TestRateLimited_GenericMiddlewareConsumesOnEveryRequest(t *testing.T) {
	a := &API{}
	l := ratelimit.New(10, 2)
	h := a.rateLimited("auth", l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = "198.51.100.7:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	want := []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v: successes must consume tokens here", codes, want)
		}
	}
}

// logged builds an API whose logger writes to the returned buffer.
func logged() (*API, *bytes.Buffer) {
	var buf bytes.Buffer
	return &API{Log: slog.New(slog.NewTextHandler(&buf, nil))}, &buf
}

// postFrom sends a login-shaped request carrying credentials and an
// Authorization header from the given peer, so tests can prove neither leaks
// into the log.
func postFrom(h http.Handler, peer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"email":"victim@x.test","password":"hunter2-secret"}`))
	req.Header.Set("Authorization", "Bearer tok-secret-123")
	req.RemoteAddr = peer
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertNoSecrets(t *testing.T, logs string) {
	t.Helper()
	for _, secret := range []string{"hunter2-secret", "victim@x.test", "tok-secret-123"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log leaks %q: %s", secret, logs)
		}
	}
}

// Every 429 from a limiter is logged with the limiter name and the client key
// and nothing from the request body or headers.
func TestRateLimited_LogsEvery429WithLimiterAndClientKey(t *testing.T) {
	a, buf := logged()
	l := ratelimit.New(10, 1)
	h := a.rateLimited("auth", l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	if rec := postFrom(h, "198.51.100.7:1"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", rec.Code)
	}
	if buf.Len() != 0 {
		t.Fatalf("an allowed request must not log: %s", buf)
	}

	for i := 1; i <= 2; i++ {
		if rec := postFrom(h, "198.51.100.7:1"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("denied request %d = %d, want 429", i, rec.Code)
		}
	}
	logs := buf.String()
	if got := strings.Count(logs, "rate limited"); got != 2 {
		t.Fatalf("logged %d rate-limit lines, want one per 429 (2): %s", got, logs)
	}
	for _, want := range []string{"limiter=auth", "client=198.51.100.7", "level=WARN"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q: %s", want, logs)
		}
	}
	assertNoSecrets(t, logs)
}

// The two login budgets log under their own names so an operator can tell a
// failure lockout from an attempt-cap rejection.
func TestRateLimitedLogin_LogsWhichBudgetRejected(t *testing.T) {
	a, buf := logged()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	// Strict budget of 1 failure, generous attempt budget.
	h := a.rateLimitedLogin(ratelimit.New(10, 1), ratelimit.New(100, 100), ok)
	postFrom(h, "203.0.113.9:1") // consumes the only strict token (401: not refunded)
	if rec := postFrom(h, "203.0.113.9:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("strict rejection = %d, want 429", rec.Code)
	}
	logs := buf.String()
	if !strings.Contains(logs, "limiter=auth ") || !strings.Contains(logs, "client=203.0.113.9") {
		t.Fatalf("strict rejection not logged with name and key: %s", logs)
	}
	assertNoSecrets(t, logs)

	// Attempt budget of 1, generous strict budget.
	buf.Reset()
	h = a.rateLimitedLogin(ratelimit.New(100, 100), ratelimit.New(10, 1), ok)
	postFrom(h, "203.0.113.10:1")
	if rec := postFrom(h, "203.0.113.10:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt rejection = %d, want 429", rec.Code)
	}
	logs = buf.String()
	if !strings.Contains(logs, "limiter=auth_attempt") || !strings.Contains(logs, "client=203.0.113.10") {
		t.Fatalf("attempt rejection not logged with name and key: %s", logs)
	}
	assertNoSecrets(t, logs)
}
