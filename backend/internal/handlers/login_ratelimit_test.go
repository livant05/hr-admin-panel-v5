package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/ratelimit"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// stepClock is a manually advanced clock for the limiter under test.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// limitedAPI builds an API of its own (testutil.Server keeps the production
// limiter defaults, so a limiter test must never share it) whose AuthLimiter
// runs on a fake clock. It returns the handler, the clock and a counter of
// password comparisons performed.
func limitedAPI(t *testing.T, pool *pgxpool.Pool, perMinute, burst int, trusted ...string) (http.Handler, *stepClock, *int) {
	t.Helper()
	signer := auth.NewSigner(testutil.TestSecret, loginTTL)
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	clk := &stepClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	api.AuthLimiter = ratelimit.New(perMinute, burst)
	api.AuthLimiter.Now = clk.Now
	for _, c := range trusted {
		api.TrustedProxies = append(api.TrustedProxies, netip.MustParsePrefix(c))
	}
	compares := 0
	api.PasswordCheck = func(hash, plain string) bool {
		compares++
		return auth.CheckPassword(hash, plain)
	}
	return api.Routes(signer), clk, &compares
}

// loginFrom posts a login from the given peer address, optionally carrying an
// X-Real-IP header.
func loginFrom(h http.Handler, remoteAddr, realIP, email, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	if realIP != "" {
		req.Header.Set("X-Real-IP", realIP)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginRateLimit_ExceedingBudgetReturns429WithRetryAfterAndEnvelope(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 3)
	const peer = "198.51.100.7:40000"

	for i := 1; i <= 3; i++ {
		rec := loginFrom(h, peer, "", "nobody@corp.test", "x")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d inside the budget must reach the handler (401), got %d", i, rec.Code)
		}
	}

	rec := loginFrom(h, peer, "", "nobody@corp.test", "x")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 4 must be rate limited, got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "6" {
		t.Fatalf("Retry-After = %q, want %q (10/min refills one token per 6s)", got, "6")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if code := testutil.ErrCode(t, rec); code != "rate_limited" {
		t.Fatalf("error.code = %q, want rate_limited", code)
	}
	assertNoHashLeak(t, rec.Body.String())
}

func TestLoginRateLimit_BudgetRefillsAfterRetryAfter(t *testing.T) {
	pool := testutil.Pool(t)
	h, clk, _ := limitedAPI(t, pool, 10, 1)
	const peer = "198.51.100.7:40000"

	loginFrom(h, peer, "", "nobody@corp.test", "x")
	if rec := loginFrom(h, peer, "", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("budget of 1 must be exhausted, got %d", rec.Code)
	}

	clk.Advance(6 * time.Second)
	if rec := loginFrom(h, peer, "", "nobody@corp.test", "x"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after the Retry-After interval the request must reach the handler, got %d", rec.Code)
	}
}

func TestLoginRateLimit_RejectedRequestsDoNoPasswordWork(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, compares := limitedAPI(t, pool, 10, 2)
	const peer = "198.51.100.7:40000"

	loginFrom(h, peer, "", "nobody@corp.test", "x")
	loginFrom(h, peer, "", "nobody@corp.test", "x")
	if *compares != 2 {
		t.Fatalf("two admitted logins perform two comparisons, got %d", *compares)
	}
	for i := 0; i < 5; i++ {
		if rec := loginFrom(h, peer, "", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d", rec.Code)
		}
	}
	if *compares != 2 {
		t.Fatalf("a limited request must not reach bcrypt: %d comparisons", *compares)
	}
}

func TestLoginRateLimit_OtherClientIPIsUnaffected(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 1)

	loginFrom(h, "198.51.100.7:40000", "", "nobody@corp.test", "x")
	if rec := loginFrom(h, "198.51.100.7:40001", "", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP, different source port shares the bucket: got %d", rec.Code)
	}
	if rec := loginFrom(h, "203.0.113.20:40000", "", "nobody@corp.test", "x"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a different client IP must have its own budget, got %d", rec.Code)
	}
}

func TestLoginRateLimit_ForgedXRealIPFromUntrustedPeerIsIgnored(t *testing.T) {
	pool := testutil.Pool(t)
	// Only 10.0.0.0/8 is a trusted proxy; the attacker connects directly.
	h, _, _ := limitedAPI(t, pool, 10, 2, "10.0.0.0/8")
	const attacker = "198.51.100.7:40000"

	loginFrom(h, attacker, "203.0.113.1", "nobody@corp.test", "x")
	loginFrom(h, attacker, "203.0.113.2", "nobody@corp.test", "x")
	for i := 3; i <= 6; i++ {
		rec := loginFrom(h, attacker, "203.0.113."+strconv.Itoa(i), "nobody@corp.test", "x")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("rotating a forged X-Real-IP must not mint fresh budget (attempt %d): got %d", i, rec.Code)
		}
	}
}

func TestLoginRateLimit_TrustedProxyKeysOnXRealIP(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 1, "10.0.0.0/8")
	const proxy = "10.2.3.4:50000"

	loginFrom(h, proxy, "203.0.113.1", "nobody@corp.test", "x")
	if rec := loginFrom(h, proxy, "203.0.113.1", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the same end client behind the proxy shares one bucket, got %d", rec.Code)
	}
	if rec := loginFrom(h, proxy, "203.0.113.2", "nobody@corp.test", "x"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a different end client behind the same proxy must not be throttled by it, got %d", rec.Code)
	}
}

func TestLoginRateLimit_HealthIsNotLimited(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 1)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.RemoteAddr = "198.51.100.7:40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("health request %d: got %d, want 200 (only auth endpoints are limited)", i, rec.Code)
		}
	}
}

func TestNew_DefaultsMatchProductionLimits(t *testing.T) {
	pool := testutil.Pool(t)
	api := handlers.New(pool, auth.NewSigner(testutil.TestSecret, loginTTL), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if api.AuthLimiter == nil || api.ADMSLimiter == nil {
		t.Fatal("New must install both limiters")
	}
	if handlers.DefaultAuthRatePerMin != 10 || handlers.DefaultADMSRatePerMin != 600 {
		t.Fatalf("defaults = %d/%d, want 10/600", handlers.DefaultAuthRatePerMin, handlers.DefaultADMSRatePerMin)
	}
	if len(api.TrustedProxies) != 0 {
		t.Fatalf("default trusts nobody, got %v", api.TrustedProxies)
	}
	allowed := 0
	for i := 0; i < 20; i++ {
		if ok, _ := api.AuthLimiter.Allow("k"); ok {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("default auth burst = %d, want 10", allowed)
	}
}

func TestLoginRateLimit_IPv6ClientsShareTheirSlash64Budget(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 1)

	loginFrom(h, "[2001:db8:1:2::1]:40000", "", "nobody@corp.test", "x")
	if rec := loginFrom(h, "[2001:db8:1:2:dead:beef::7]:40000", "", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("another address in the same /64 must share the exhausted budget, got %d", rec.Code)
	}
	if rec := loginFrom(h, "[2001:db8:1:3::1]:40000", "", "nobody@corp.test", "x"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a different /64 keeps its own budget, got %d", rec.Code)
	}
}

func TestLoginRateLimit_IPv4MappedPeerSharesTheIPv4Budget(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 1)

	loginFrom(h, "203.0.113.9:40000", "", "nobody@corp.test", "x")
	if rec := loginFrom(h, "[::ffff:203.0.113.9]:40000", "", "nobody@corp.test", "x"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("an IPv4-mapped peer is the same client as its IPv4 form, got %d", rec.Code)
	}
}

func TestLoginRateLimit_SuccessfulLoginsDoNotConsumeBudget(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, compares := limitedAPI(t, pool, 10, 3)
	tn := testutil.Company(t, pool, "RateOkCo-"+uuid.NewString()[:6])
	email := uniqueEmail("ok")
	seedLoginUser(t, pool, tn, email, "correct-horse", "active")
	const peer = "198.51.100.7:40000"

	for i := 1; i <= 12; i++ {
		if rec := loginFrom(h, peer, "", email, "correct-horse"); rec.Code != http.StatusOK {
			t.Fatalf("successful login %d must never be throttled, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	if *compares != 12 {
		t.Fatalf("comparisons = %d, want 12", *compares)
	}
}

func TestLoginRateLimit_OnlyFailuresConsumeAndEmptyBucketRejectsEvenCorrectPasswordWithoutBcrypt(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, compares := limitedAPI(t, pool, 10, 10)
	tn := testutil.Company(t, pool, "RateFailCo-"+uuid.NewString()[:6])
	email := uniqueEmail("fail")
	seedLoginUser(t, pool, tn, email, "correct-horse", "active")
	const peer = "198.51.100.7:40000"

	for i := 1; i <= 10; i++ {
		if rec := loginFrom(h, peer, "", email, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d, want 401", i, rec.Code)
		}
	}
	before := *compares
	rec := loginFrom(h, peer, "", email, "correct-horse")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th request must be 429 even with the right password, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "6" {
		t.Fatalf("Retry-After = %q, want 6", rec.Header().Get("Retry-After"))
	}
	if code := testutil.ErrCode(t, rec); code != "rate_limited" {
		t.Fatalf("error.code = %q, want rate_limited", code)
	}
	if *compares != before {
		t.Fatal("a throttled request must not reach bcrypt")
	}
}

func TestLoginRateLimit_SuccessesInterleavedWithFailuresOnlyCountFailures(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 3)
	tn := testutil.Company(t, pool, "RateMixCo-"+uuid.NewString()[:6])
	email := uniqueEmail("mix")
	seedLoginUser(t, pool, tn, email, "correct-horse", "active")
	const peer = "198.51.100.7:40000"

	for i := 1; i <= 3; i++ {
		if rec := loginFrom(h, peer, "", email, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d, want 401", i, rec.Code)
		}
		if i < 3 {
			if rec := loginFrom(h, peer, "", email, "correct-horse"); rec.Code != http.StatusOK {
				t.Fatalf("success after failure %d: got %d, want 200", i, rec.Code)
			}
		}
	}
	if rec := loginFrom(h, peer, "", email, "correct-horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("three failures drain the burst; got %d", rec.Code)
	}
}

func TestLoginRateLimit_EveryUnauthorizedOutcomeConsumes(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 3)
	tn := testutil.Company(t, pool, "RateKindsCo-"+uuid.NewString()[:6])
	inactive := uniqueEmail("off")
	noHash := uniqueEmail("nohash")
	seedLoginUser(t, pool, tn, inactive, "correct-horse", "inactive")
	seedLoginUser(t, pool, tn, noHash, "", "active")
	const peer = "198.51.100.7:40000"

	cases := []struct{ name, email string }{
		{"unknown email", uniqueEmail("ghost")},
		{"inactive user", inactive},
		{"empty stored hash", noHash},
	}
	for _, c := range cases {
		if rec := loginFrom(h, peer, "", c.email, "correct-horse"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401", c.name, rec.Code)
		}
	}
	if rec := loginFrom(h, peer, "", inactive, "correct-horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("three 401 outcomes must drain a burst of 3, got %d", rec.Code)
	}
}

func TestLoginRateLimit_SuccessAfterFailuresKeepsRemainingBudget(t *testing.T) {
	pool := testutil.Pool(t)
	h, _, _ := limitedAPI(t, pool, 10, 3)
	tn := testutil.Company(t, pool, "RateKeepCo-"+uuid.NewString()[:6])
	email := uniqueEmail("keep")
	seedLoginUser(t, pool, tn, email, "correct-horse", "active")
	const peer = "198.51.100.7:40000"

	loginFrom(h, peer, "", email, "wrong")
	loginFrom(h, peer, "", email, "wrong")
	for i := 0; i < 5; i++ {
		if rec := loginFrom(h, peer, "", email, "correct-horse"); rec.Code != http.StatusOK {
			t.Fatalf("success %d with one token left: got %d", i, rec.Code)
		}
	}
	if rec := loginFrom(h, peer, "", email, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the last token must still be available, got %d", rec.Code)
	}
	if rec := loginFrom(h, peer, "", email, "correct-horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("budget spent by 3 failures, got %d", rec.Code)
	}
}
