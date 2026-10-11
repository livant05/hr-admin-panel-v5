package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

const loginTTL = 24 * time.Hour

// loginAPI builds an API with a 24h signer (the production TTL, which
// testutil.Server does not use) and an optional password-check spy.
func loginAPI(t *testing.T, pool *pgxpool.Pool, check func(hash, plain string) bool) (http.Handler, *auth.Signer) {
	t.Helper()
	signer := auth.NewSigner(testutil.TestSecret, loginTTL)
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	api.PasswordCheck = check
	return api.Routes(signer), signer
}

// seedLoginUser inserts a user with a bcrypt hash of pw. An empty pw stores
// password_hash = ” (the 0002 column default).
func seedLoginUser(t *testing.T, pool *pgxpool.Pool, tn testutil.Tenant, email, pw, status string) string {
	t.Helper()
	hash := ""
	if pw != "" {
		var err error
		if hash, err = auth.HashPassword(pw); err != nil {
			t.Fatalf("hash password: %v", err)
		}
	}
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, company_id, email, role, first_name, last_name, status, password_hash)
		 VALUES ($1, $2, $3, 'empleado', 'Ana', 'Perez', $4, $5)`,
		id, tn.CompanyID, email, status, hash)
	if err != nil {
		t.Fatalf("insert login user: %v", err)
	}
	return id
}

func postLogin(t *testing.T, h http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, h, http.MethodPost, "/api/auth/login", "",
		map[string]string{"email": email, "password": password})
}

func postRaw(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func uniqueEmail(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8] + "@corp.test"
}

// assertNoHashLeak enforces XC-2 over a raw response body.
func assertNoHashLeak(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, needle := range []string{"password_hash", "$2a$", "$2b$"} {
		if strings.Contains(lower, needle) {
			t.Fatalf("response body leaks %q: %s", needle, body)
		}
	}
}

func TestLogin_HappyPath(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := loginAPI(t, pool, nil)
	coName := "LoginHappyCo-" + uuid.NewString()[:6]
	tn := testutil.Company(t, pool, coName)
	email := uniqueEmail("ana")
	id := seedLoginUser(t, pool, tn, email, "correct-horse", "active")

	rec := postLogin(t, h, email, "correct-horse")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		CompanyName string `json:"company_name"`
		User        struct {
			ID           string            `json:"id"`
			CompanyID    string            `json:"company_id"`
			Email        string            `json:"email"`
			Role         string            `json:"role"`
			FirstName    string            `json:"first_name"`
			LastName     string            `json:"last_name"`
			UserMetadata map[string]string `json:"user_metadata"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TokenType != "bearer" || got.ExpiresIn != 86400 {
		t.Fatalf("token_type/expires_in = %q/%d", got.TokenType, got.ExpiresIn)
	}
	if got.CompanyName != coName {
		t.Fatalf("company_name = %q, want %q", got.CompanyName, coName)
	}
	if got.User.ID != id || got.User.CompanyID != tn.CompanyID || got.User.Email != email ||
		got.User.Role != "empleado" || got.User.FirstName != "Ana" || got.User.LastName != "Perez" {
		t.Fatalf("unexpected user payload: %+v", got.User)
	}
	wantMeta := map[string]string{
		"role": "empleado", "company_id": tn.CompanyID,
		"first_name": "Ana", "last_name": "Perez", "company_name": coName,
	}
	if len(got.User.UserMetadata) != len(wantMeta) {
		t.Fatalf("user_metadata keys = %v, want exactly %v", got.User.UserMetadata, wantMeta)
	}
	for k, v := range wantMeta {
		if got.User.UserMetadata[k] != v {
			t.Fatalf("user_metadata[%s] = %q, want %q", k, got.User.UserMetadata[k], v)
		}
	}

	claims, err := signer.Parse(got.AccessToken)
	if err != nil {
		t.Fatalf("parse issued token: %v", err)
	}
	if lifetime := claims.ExpiresAt.Sub(claims.IssuedAt.Time); lifetime != loginTTL {
		t.Fatalf("token lifetime = %s, want %s", lifetime, loginTTL)
	}
	assertNoHashLeak(t, rec.Body.String())
}

func TestLogin_FailuresAreIdentical401(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := loginAPI(t, pool, nil)
	tn := testutil.Company(t, pool, "LoginFailCo-"+uuid.NewString()[:6])
	active := uniqueEmail("active")
	inactive := uniqueEmail("inactive")
	seedLoginUser(t, pool, tn, active, "correct-horse", "active")
	seedLoginUser(t, pool, tn, inactive, "correct-horse", "inactive")

	wrong := postLogin(t, h, active, "wrong-password")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: expected 401, got %d", wrong.Code)
	}
	if code := testutil.ErrCode(t, wrong); code != "unauthorized" {
		t.Fatalf("wrong password code = %q", code)
	}

	cases := map[string]*httptest.ResponseRecorder{
		"inactive user with correct password": postLogin(t, h, inactive, "correct-horse"),
		"unknown email":                       postLogin(t, h, uniqueEmail("ghost"), "correct-horse"),
	}
	for name, rec := range cases {
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d (%s)", name, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != wrong.Body.String() {
			t.Fatalf("%s: body %q differs from wrong-password body %q", name, rec.Body.String(), wrong.Body.String())
		}
		if strings.Contains(rec.Body.String(), "access_token") {
			t.Fatalf("%s: response carries a token", name)
		}
	}
}

func TestLogin_EmailIsCaseInsensitiveAndNormalized(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := loginAPI(t, pool, nil)
	tn := testutil.Company(t, pool, "LoginCaseCo-"+uuid.NewString()[:6])
	suffix := uuid.NewString()[:8]
	stored := "Ana-" + suffix + "@Corp.com"
	seedLoginUser(t, pool, tn, stored, "correct-horse", "active")

	for name, attempt := range map[string]string{
		"upper domain":    "ana-" + suffix + "@corp.COM",
		"all upper":       strings.ToUpper(stored),
		"padded and case": "  ANA-" + suffix + "@Corp.com \t",
	} {
		rec := postLogin(t, h, attempt, "correct-horse")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// A different address must still fail: case-insensitivity is not a prefix match.
	if rec := postLogin(t, h, "ana-"+suffix+"x@corp.com", "correct-horse"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("different email: expected 401, got %d", rec.Code)
	}
}

func TestLogin_BodyValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := loginAPI(t, pool, nil)

	cases := []struct {
		name string
		body string
	}{
		{"unknown field", `{"email":"a@b.test","password":"x","admin":true}`},
		{"over 4 KiB", `{"email":"a@b.test","password":"` + strings.Repeat("p", 4096) + `"}`},
		{"trailing json value", `{"email":"a@b.test","password":"x"}{"email":"c@d.test"}`},
		{"not json", `email=a@b.test`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postRaw(t, h, "/api/auth/login", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "bad_request" {
				t.Fatalf("error.code = %q, want bad_request", code)
			}
		})
	}

	t.Run("missing fields keep validation_failed", func(t *testing.T) {
		rec := postRaw(t, h, "/api/auth/login", `{"email":"","password":""}`)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("expected 400 validation_failed, got %d (%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestLogin_MissingTokenOnProtectedRouteIsA2Envelope(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := loginAPI(t, pool, nil)

	rec := testutil.Do(t, h, http.MethodGet, "/api/me", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if code := testutil.ErrCode(t, rec); code != "unauthorized" {
		t.Fatalf("error.code = %q, want unauthorized", code)
	}
}

// compareSpy records every PasswordCheck invocation and delegates to the
// real comparison so behaviour is unchanged.
type compareSpy struct {
	mu    sync.Mutex
	calls []string // hash argument of each call
}

func (s *compareSpy) check(hash, plain string) bool {
	s.mu.Lock()
	s.calls = append(s.calls, hash)
	s.mu.Unlock()
	return auth.CheckPassword(hash, plain)
}

func (s *compareSpy) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func TestLogin_ExactlyOneComparePerRequest(t *testing.T) {
	pool := testutil.Pool(t)
	tn := testutil.Company(t, pool, "LoginSpyCo-"+uuid.NewString()[:6])
	emptyHashEmail := uniqueEmail("nohash")
	realEmail := uniqueEmail("real")
	inactiveEmail := uniqueEmail("off")
	seedLoginUser(t, pool, tn, emptyHashEmail, "", "active")
	seedLoginUser(t, pool, tn, realEmail, "correct-horse", "active")
	seedLoginUser(t, pool, tn, inactiveEmail, "correct-horse", "inactive")

	run := func(email, password string) (*compareSpy, *httptest.ResponseRecorder) {
		spy := &compareSpy{}
		h, _ := loginAPI(t, pool, spy.check)
		return spy, postLogin(t, h, email, password)
	}

	t.Run("unknown email takes the dummy path", func(t *testing.T) {
		spy, rec := run(uniqueEmail("ghost"), "whatever")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		calls := spy.snapshot()
		if len(calls) != 1 || calls[0] != "" {
			t.Fatalf("expected exactly one compare with the empty (dummy) hash, got %q", calls)
		}
	})

	t.Run("stored empty hash takes the dummy path", func(t *testing.T) {
		spy, rec := run(emptyHashEmail, "anything")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		calls := spy.snapshot()
		if len(calls) != 1 || calls[0] != "" {
			t.Fatalf("expected exactly one compare with the empty hash, got %q", calls)
		}
	})

	t.Run("known user compares the real hash once", func(t *testing.T) {
		spy, rec := run(realEmail, "wrong")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		calls := spy.snapshot()
		if len(calls) != 1 || !strings.HasPrefix(calls[0], "$2") {
			t.Fatalf("expected exactly one compare with a bcrypt hash, got %q", calls)
		}
	})

	t.Run("inactive user still performs one compare", func(t *testing.T) {
		spy, rec := run(inactiveEmail, "correct-horse")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		if n := len(spy.snapshot()); n != 1 {
			t.Fatalf("expected exactly one compare, got %d", n)
		}
	})
}
