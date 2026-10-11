// Package ratelimit is a small in-process, per-key token-bucket limiter used
// to throttle the public endpoints (login, register, device ingestion). State
// lives in memory, so limits apply per backend instance; with a single
// instance that is exact, with several it is a per-instance budget.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// maxKeys is the number of tracked keys above which an Allow call sweeps idle
// buckets. It bounds memory under a flood of distinct source addresses.
const maxKeys = 10_000

// epsilon absorbs float64 rounding in refill arithmetic, so a bucket that is
// mathematically at exactly one token (or an exact whole-second wait) is not
// misjudged by a 1e-16 error.
const epsilon = 1e-9

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter hands out one token per request from a per-key bucket that holds at
// most burst tokens and refills at perMinute tokens per minute. The zero value
// is not usable; build one with New.
type Limiter struct {
	// Now is the clock. Tests replace it; New sets time.Now.
	Now func() time.Time

	mu       sync.Mutex
	ratePerS float64 // tokens added per second
	burst    float64
	buckets  map[string]*bucket
}

// New returns a Limiter refilling perMinute tokens per minute with room for
// burst tokens. Non-positive arguments are raised to 1 so the limiter can
// never deadlock a key forever; callers validate configuration beforehand.
func New(perMinute, burst int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		Now:      time.Now,
		ratePerS: float64(perMinute) / 60,
		burst:    float64(burst),
		buckets:  make(map[string]*bucket),
	}
}

// Allow consumes one token for key. When none is available it returns false
// and the wait until one will be, rounded up to whole seconds (and at least
// one second) so it can be sent verbatim as a Retry-After value.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.Now()
	if len(l.buckets) > maxKeys {
		l.sweep(now)
	}

	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens = l.refilled(b, now)
		b.last = now
	}

	if b.tokens >= 1-epsilon {
		b.tokens--
		return true, 0
	}
	wait := (1 - b.tokens) / l.ratePerS
	return false, time.Duration(math.Ceil(wait-epsilon)) * time.Second
}

// refilled returns b's token count as of now, capped at the burst.
func (l *Limiter) refilled(b *bucket, now time.Time) float64 {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	return math.Min(l.burst, b.tokens+elapsed*l.ratePerS)
}

// sweep drops every bucket that has refilled to full: forgetting it changes
// nothing, because a missing key starts full. Buckets still carrying debt are
// kept. The caller holds l.mu.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if l.refilled(b, now) >= l.burst-epsilon {
			delete(l.buckets, k)
		}
	}
}
