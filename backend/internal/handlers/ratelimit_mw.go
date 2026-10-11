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
		ok, retryAfter := l.Allow(ratelimit.ClientIP(r, a.TrustedProxies))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeErrCode(w, http.StatusTooManyRequests, codeRateLimited, "too many requests, retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}
