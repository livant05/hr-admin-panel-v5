// Package ratelimit is a small in-process, per-key token-bucket limiter used
// to throttle the public endpoints (login, register, device ingestion). State
// lives in memory, so limits apply per backend instance; with a single
// instance that is exact, with several it is a per-instance budget.
package ratelimit

import (
	"container/list"
	"math"
	"sync"
	"time"
)

// defaultMaxKeys is the hard cap on tracked keys. It bounds memory, and the
// work done under the lock, however many distinct source addresses arrive.
const defaultMaxKeys = 10_000

// epsilon absorbs float64 rounding in refill arithmetic, so a bucket that is
// mathematically at exactly one token (or an exact whole-second wait) is not
// misjudged by a 1e-16 error.
const epsilon = 1e-9

type bucket struct {
	key    string
	tokens float64
	last   time.Time
}

// Limiter hands out one token per request from a per-key bucket that holds at
// most burst tokens and refills at perMinute tokens per minute. The zero value
// is not usable; build one with New.
//
// Tracked keys are capped (defaultMaxKeys). At the cap, admitting a new key
// evicts the least recently used one in O(1): there is no sweep on the hot
// path. Eviction forgets that key's debt, so it starts full if it returns.
// Tradeoff: an attacker able to present more than the cap of distinct keys can
// flush a victim's debt. Keying IPv6 by /64 (see Key) makes distinct keys
// costly to mint, and the cap is large relative to legitimate traffic.
type Limiter struct {
	// Now is the clock. Tests replace it; New sets time.Now.
	Now func() time.Time

	mu       sync.Mutex
	ratePerS float64 // tokens added per second
	burst    float64
	maxKeys  int
	buckets  map[string]*list.Element // key -> element holding *bucket
	order    *list.List               // front = most recently used
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
		maxKeys:  defaultMaxKeys,
		buckets:  make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Allow consumes one token for key. When none is available it returns false
// and the wait until one will be, rounded up to whole seconds (and at least
// one second) so it can be sent verbatim as a Retry-After value.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.Now()

	var b *bucket
	if el, found := l.buckets[key]; found {
		b = el.Value.(*bucket)
		b.tokens = l.refilled(b, now)
		b.last = now
		l.order.MoveToFront(el)
	} else {
		if len(l.buckets) >= l.maxKeys {
			l.evictOldest()
		}
		b = &bucket{key: key, tokens: l.burst, last: now}
		l.buckets[key] = l.order.PushFront(b)
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

// evictOldest removes the least recently used bucket. The caller holds l.mu.
func (l *Limiter) evictOldest() {
	el := l.order.Back()
	if el == nil {
		return
	}
	l.order.Remove(el)
	delete(l.buckets, el.Value.(*bucket).key)
}
