package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock injected through Limiter.Now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestLimiter(perMinute, burst int) (*Limiter, *fakeClock) {
	clk := newFakeClock()
	l := New(perMinute, burst)
	l.Now = clk.Now
	return l, clk
}

func TestAllow_BurstThenDeny(t *testing.T) {
	l, _ := newTestLimiter(10, 3)

	for i := 1; i <= 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("request %d within the burst must be allowed", i)
		}
	}
	ok, retry := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("request beyond the burst must be denied")
	}
	if retry <= 0 {
		t.Fatalf("a denial must report a positive retry-after, got %v", retry)
	}
}

func TestAllow_RefillsAtPerMinuteRate(t *testing.T) {
	// 10/min = one token every 6 seconds.
	l, clk := newTestLimiter(10, 2)
	l.Allow("k")
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("bucket must be empty after the burst")
	}

	clk.Advance(5 * time.Second)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("5s is less than one 6s refill interval: still denied")
	}

	clk.Advance(1 * time.Second) // 6s total since empty
	if ok, retry := l.Allow("k"); !ok {
		t.Fatalf("one token must have refilled after 6s (retry-after %v)", retry)
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("exactly one token refilled; the next request is denied")
	}
}

func TestAllow_RefillIsCappedAtBurst(t *testing.T) {
	l, clk := newTestLimiter(10, 2)
	l.Allow("k")
	l.Allow("k")

	clk.Advance(time.Hour) // would refill 600 tokens if uncapped
	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("k"); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("a long idle period may refill only up to the burst: allowed %d, want 2", allowed)
	}
}

func TestAllow_RetryAfterIsCeilSeconds(t *testing.T) {
	tests := []struct {
		name      string
		perMinute int
		advance   time.Duration
		want      time.Duration
	}{
		{"full interval", 10, 0, 6 * time.Second},
		{"fraction rounds up", 10, 500 * time.Millisecond, 6 * time.Second},
		{"whole seconds left", 10, 1 * time.Second, 5 * time.Second},
		{"slow rate", 1, 0, 60 * time.Second},
		{"fast rate still at least one second", 600, 0, 1 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, clk := newTestLimiter(tt.perMinute, 1)
			l.Allow("k")
			clk.Advance(tt.advance)
			ok, retry := l.Allow("k")
			if ok {
				t.Fatal("bucket should still be empty")
			}
			if retry != tt.want {
				t.Fatalf("retry-after = %v, want %v", retry, tt.want)
			}
		})
	}
}

func TestAllow_KeysAreIsolated(t *testing.T) {
	l, _ := newTestLimiter(10, 1)

	if ok, _ := l.Allow("10.0.0.1"); !ok {
		t.Fatal("first key first request must pass")
	}
	if ok, _ := l.Allow("10.0.0.1"); ok {
		t.Fatal("first key is exhausted")
	}
	if ok, _ := l.Allow("10.0.0.2"); !ok {
		t.Fatal("another key must be unaffected by the first key's exhaustion")
	}
}

func TestAllow_TrackedKeysNeverExceedTheCap(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	l.maxKeys = 50

	for i := 0; i < 50*20; i++ { // 20x more distinct keys than the cap
		l.Allow(fmt.Sprintf("flood-%d", i))
		if got := len(l.buckets); got > l.maxKeys {
			t.Fatalf("after %d keys: %d tracked, cap is %d", i+1, got, l.maxKeys)
		}
		if got := l.order.Len(); got != len(l.buckets) {
			t.Fatalf("recency list (%d) and map (%d) diverged", got, len(l.buckets))
		}
	}
	if got := len(l.buckets); got != l.maxKeys {
		t.Fatalf("a flood must leave the table exactly at the cap: %d tracked, cap %d", got, l.maxKeys)
	}
}

func TestAllow_DefaultCapIsTenThousand(t *testing.T) {
	if got := New(10, 1).maxKeys; got != 10_000 {
		t.Fatalf("default cap = %d, want 10000", got)
	}
}

// Eviction is amortised O(1): at the cap, one new key removes exactly one
// bucket (the least recently used). The retired sweep dropped every refilled
// bucket in one O(n) pass, which this test would observe as the table
// collapsing to a single key.
func TestAllow_AtCapEvictsExactlyOneLeastRecentlyUsedKey(t *testing.T) {
	l, clk := newTestLimiter(60, 1)
	l.maxKeys = 3
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	clk.Advance(time.Minute) // every bucket is full again: a sweep would drop all three

	l.Allow("d")

	if got := len(l.buckets); got != 3 {
		t.Fatalf("exactly one key must be evicted, %d tracked, want 3", got)
	}
	if _, ok := l.buckets["a"]; ok {
		t.Fatal("the least recently used key (a) must be the one evicted")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := l.buckets[k]; !ok {
			t.Fatalf("key %q must survive", k)
		}
	}
}

func TestAllow_UseRefreshesRecency(t *testing.T) {
	l, _ := newTestLimiter(60, 2)
	l.maxKeys = 3
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	l.Allow("a") // a is now the most recently used; b is the oldest

	l.Allow("d")

	if _, ok := l.buckets["b"]; ok {
		t.Fatal("b is least recently used and must be evicted")
	}
	if _, ok := l.buckets["a"]; !ok {
		t.Fatal("a was just used and must survive")
	}
}

// Documented tradeoff: evicting a bucket forgets its debt, so a flood of
// distinct keys beyond the cap can reset a victim. The cap and /64 keying make
// that expensive; the behaviour is pinned here so it stays deliberate.
func TestAllow_EvictedKeyStartsWithAFullBucket(t *testing.T) {
	l, _ := newTestLimiter(10, 1)
	l.maxKeys = 2
	l.Allow("victim")
	if ok, _ := l.Allow("victim"); ok {
		t.Fatal("precondition: victim is in debt")
	}
	l.Allow("x")
	l.Allow("y") // evicts victim

	if ok, _ := l.Allow("victim"); !ok {
		t.Fatal("an evicted key is forgotten and starts full")
	}
}

func TestAllow_KeysInDebtKeepRefillingAcrossRecencyMoves(t *testing.T) {
	l, clk := newTestLimiter(60, 1)
	l.maxKeys = 3
	l.Allow("hot")
	clk.Advance(500 * time.Millisecond)
	if ok, _ := l.Allow("hot"); ok {
		t.Fatal("hot has only 0.5 tokens")
	}
	clk.Advance(500 * time.Millisecond)
	if ok, _ := l.Allow("hot"); !ok {
		t.Fatal("hot refilled one token over 1s in total")
	}
}

func TestAllow_ConcurrentUseIsSafe(t *testing.T) {
	l := New(600, 100)
	var wg sync.WaitGroup
	allowed := make(chan bool, 400)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				ok, _ := l.Allow("shared")
				allowed <- ok
			}
		}()
	}
	wg.Wait()
	close(allowed)
	n := 0
	for ok := range allowed {
		if ok {
			n++
		}
	}
	// 400 attempts against a burst of 100 with negligible wall-clock refill.
	if n < 100 || n > 110 {
		t.Fatalf("expected ~100 allowed of 400 concurrent attempts, got %d", n)
	}
}
