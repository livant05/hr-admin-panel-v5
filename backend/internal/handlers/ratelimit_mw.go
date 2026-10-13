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
// Every denial is logged (see logRateLimited) under name, so operators can see
// who is being throttled and by which budget.
//
// A nil limiter panics when the routes are built: a public endpoint must never
// silently run unthrottled because wiring was forgotten.
func (a *API) rateLimited(name string, l *ratelimit.Limiter, next http.Handler) http.Handler {
	if l == nil {
		panic("handlers: rateLimited requires a non-nil limiter")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := ratelimit.Key(ratelimit.ClientIP(r, a.TrustedProxies))
		if ok, retryAfter := l.Allow(key); !ok {
			a.logRateLimited(name, key, retryAfter)
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
			a.logRateLimited(limiterAuthAttempt, key, retryAfter)
			writeRateLimited(w, retryAfter)
			return
		}
		ok, retryAfter := strict.Allow(key)
		if !ok {
			a.logRateLimited(limiterAuth, key, retryAfter)
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

// Limiter names used in rate-limit log lines.
const (
	limiterAuth        = "auth"         // strict budget: failed logins (and register)
	limiterAuthAttempt = "auth_attempt" // looser budget: every login attempt
)

// logRateLimited records one 429 from a limiter. It logs only the limiter name,
// the client key (the IP, or the /64 for IPv6, that the limiter charged) and
// the Retry-After it answered with; never the request body, headers or any
// credential. A shared NAT locking its users out is otherwise invisible.
func (a *API) logRateLimited(limiter, clientKey string, retryAfter time.Duration) {
	if a.Log == nil {
		return
	}
	a.Log.Warn("rate limited", "limiter", limiter, "client", clientKey, "retry_after_s", int(retryAfter.Seconds()))
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
