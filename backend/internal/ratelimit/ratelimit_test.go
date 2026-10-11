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

func TestAllow_SweepsIdleFullBucketsAboveKeyLimit(t *testing.T) {
	l, clk := newTestLimiter(60, 1)

	for i := 0; i <= maxKeys; i++ { // maxKeys+1 distinct keys, each spends its token
		l.Allow(fmt.Sprintf("key-%d", i))
	}
	if got := len(l.buckets); got != maxKeys+1 {
		t.Fatalf("precondition: %d keys tracked, want %d", got, maxKeys+1)
	}

	clk.Advance(10 * time.Second) // every bucket refilled to full (1/s)
	l.Allow("fresh-key")          // triggers the sweep

	if got := len(l.buckets); got != 1 {
		t.Fatalf("idle full buckets must be evicted, only the fresh key remains: %d tracked", got)
	}
}

func TestAllow_SweepKeepsBucketsThatAreNotFull(t *testing.T) {
	l, clk := newTestLimiter(60, 1)

	for i := 0; i <= maxKeys; i++ {
		l.Allow(fmt.Sprintf("key-%d", i))
	}
	l.Allow("hot") // spent token, not refilled by the time of the sweep
	clk.Advance(500 * time.Millisecond)
	l.Allow("hot") // sweep runs here; "hot" has 0.5 tokens

	if _, ok := l.buckets["hot"]; !ok {
		t.Fatal("a bucket that is not full carries debt and must survive the sweep")
	}
	if ok, _ := l.Allow("hot"); ok {
		t.Fatal("the surviving bucket must still deny until it refills")
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
