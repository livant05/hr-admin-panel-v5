package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livant05/rrhh-go/internal/ratelimit"
)

// The generic limiter middleware (register, ADMS) keeps consume-on-every-request
// semantics: only login refunds successes.
func TestRateLimited_GenericMiddlewareConsumesOnEveryRequest(t *testing.T) {
	a := &API{}
	l := ratelimit.New(10, 2)
	h := a.rateLimited(l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
