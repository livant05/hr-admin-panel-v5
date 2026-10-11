package handlers

import (
	"net/http"
	"strconv"

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
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeErrCode(w, http.StatusTooManyRequests, codeRateLimited, "too many requests, retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitedFailuresOnly is rateLimited for login: the request reserves a
// token up front (an empty bucket is rejected before any bcrypt work, even if
// the password would have been right) and the token is refunded unless the
// handler answered 401. Successful logins therefore do not consume budget,
// which keeps users behind a shared NAT from locking each other out, while
// every failed credential guess still costs one token. Other statuses (400
// for a malformed body, 500 for a server fault) are not credential failures
// and are refunded too; they perform no bcrypt comparison against a guess.
func (a *API) rateLimitedFailuresOnly(l *ratelimit.Limiter, next http.Handler) http.Handler {
	if l == nil {
		panic("handlers: rateLimitedFailuresOnly requires a non-nil limiter")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ratelimit.Key(ratelimit.ClientIP(r, a.TrustedProxies))
		ok, retryAfter := l.Allow(key)
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeErrCode(w, http.StatusTooManyRequests, codeRateLimited, "too many requests, retry later")
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if sw.status != http.StatusUnauthorized {
				l.Refund(key)
			}
		}()
		next.ServeHTTP(sw, r)
	})
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
