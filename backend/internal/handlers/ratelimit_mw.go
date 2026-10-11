package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/livant05/rrhh-go/internal/ratelimit"
)

// rateLimited wraps a public handler so each client IP (see ratelimit.ClientIP
// and API.TrustedProxies) draws from l. A denied request never reaches next:
// it gets 429, a Retry-After header in whole seconds and the A2 envelope with
// code rate_limited.
//
// A nil limiter panics when the routes are built: a public endpoint must never
// silently run unthrottled because wiring was forgotten.
func (a *API) rateLimited(l *ratelimit.Limiter, next http.Handler) http.Handler {
	if l == nil {
		panic("handlers: rateLimited requires a non-nil limiter")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, retryAfter := l.Allow(ratelimit.Key(ratelimit.ClientIP(r, a.TrustedProxies)))
		if !ok {
			writeRateLimited(w, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitedLogin is rateLimited for login with two budgets per client.
//
// The strict budget (failures) reserves a token up front (an empty bucket is
// rejected before any bcrypt work, even if the password would have been right)
// and refunds it unless the handler answered 401. Successful logins therefore
// do not consume it, which keeps users behind a shared NAT from locking each
// other out, while every failed credential guess still costs one token. Other
// statuses (400 for a malformed body, 500 for a server fault) are not
// credential failures and are refunded too; they perform no bcrypt comparison
// against a guess.
//
// The looser budget (all) is charged for every attempt that reaches it and is
// never refunded: it bounds the bcrypt CPU a single client can force with
// valid credentials, which the strict budget deliberately does not. It is
// checked first, so a request it rejects never touches the strict budget.
func (a *API) rateLimitedLogin(strict, all *ratelimit.Limiter, next http.Handler) http.Handler {
	if strict == nil || all == nil {
		panic("handlers: rateLimitedLogin requires non-nil limiters")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ratelimit.Key(ratelimit.ClientIP(r, a.TrustedProxies))
		if ok, retryAfter := all.Allow(key); !ok {
			writeRateLimited(w, retryAfter)
			return
		}
		ok, retryAfter := strict.Allow(key)
		if !ok {
			writeRateLimited(w, retryAfter)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if sw.status != http.StatusUnauthorized {
				strict.Refund(key)
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// writeRateLimited writes the 429 response shared by every limiter: a
// Retry-After header in whole seconds and the A2 envelope with code
// rate_limited.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
	writeErrCode(w, http.StatusTooManyRequests, codeRateLimited, "too many requests, retry later")
}

// statusWriter records the status code written by the wrapped handler. A
// handler that writes a body without WriteHeader implies 200.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}
