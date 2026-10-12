package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/testutil"
)

const registerPath = "/api/auth/register"

// registerCompany returns a unique company name and removes any company
// registered under it when the test ends (register creates tenants that
// testutil.Company knows nothing about; users cascade with the company).
func registerCompany(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	name := "RegCo-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE name = $1`, name)
	})
	return name
}

type sessionPayload struct {
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

func registerBody(company, email, password string) map[string]string {
	return map[string]string{
		"company_name": company,
		"first_name":   "Ana",
		"last_name":    "Perez",
		"email":        email,
		"password":     password,
	}
}

func countCompanies(t *testing.T, pool *pgxpool.Pool, name string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM companies WHERE name = $1`, name).Scan(&n); err != nil {
		t.Fatalf("count companies: %v", err)
	}
	return n
}

func countUsersByEmail(t *testing.T, pool *pgxpool.Pool, email string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// registerAPI is an API with a generous AuthLimiter so tests that are not
// about throttling never trip it (testutil.Server keeps the 10-attempt default
// and every httptest request shares one client address).
func registerAPI(t *testing.T, pool *pgxpool.Pool) http.Handler {
	t.Helper()
	h, _, _ := limitedAPI(t, pool, 1000, 1000)
	return h
}

// assertErrorEnvelope checks XC-3: failures are JSON, never a bare string.
func assertErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("error Content-Type = %q, want application/json (XC-3)", ct)
	}
}

func TestRegister_SuccessReturnsLoginShapedPayloadAndTokenAuthenticatesAsNewAdmin(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)
	company := registerCompany(t, pool)
	email := uniqueEmail("reg")

	rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, email, "correct-horse"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}

	got := testutil.DecodeRow[sessionPayload](t, rec)

	if got.AccessToken == "" || got.TokenType != "bearer" || got.ExpiresIn != 86400 {
		t.Fatalf("access_token/token_type/expires_in = %q/%q/%d", got.AccessToken, got.TokenType, got.ExpiresIn)
	}
	if got.CompanyName != company {
		t.Fatalf("company_name = %q, want %q", got.CompanyName, company)
	}
	if got.User.Email != email || got.User.Role != "admin" || got.User.FirstName != "Ana" || got.User.LastName != "Perez" {
		t.Fatalf("unexpected user payload: %+v", got.User)
	}
	wantMeta := map[string]string{
		"role": "admin", "company_id": got.User.CompanyID,
		"first_name": "Ana", "last_name": "Perez", "company_name": company,
	}
	if len(got.User.UserMetadata) != len(wantMeta) {
		t.Fatalf("user_metadata keys = %v, want exactly %v", got.User.UserMetadata, wantMeta)
	}
	for k, v := range wantMeta {
		if got.User.UserMetadata[k] != v {
			t.Fatalf("user_metadata[%s] = %q, want %q", k, got.User.UserMetadata[k], v)
		}
	}
	assertNoHashLeak(t, rec.Body.String())

	// The token is a real session: it identifies the new admin of the new company.
	me := testutil.Do(t, h, http.MethodGet, "/api/me", got.AccessToken, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("GET /api/me with the register token = %d (%s)", me.Code, me.Body.String())
	}
	meBody := testutil.DecodeRow[struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Company struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"company"`
	}](t, me)
	if meBody.ID != got.User.ID || meBody.Role != "admin" || meBody.Company.ID != got.User.CompanyID || meBody.Company.Name != company {
		t.Fatalf("/api/me = %+v, want admin %s of company %s", meBody, got.User.ID, got.User.CompanyID)
	}

	// Exactly one company and one admin user were created, and the stored
	// credential is a bcrypt hash of the password the caller chose.
	if n := countCompanies(t, pool, company); n != 1 {
		t.Fatalf("companies named %q = %d, want 1", company, n)
	}
	var users int
	var role, status, hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) OVER (), role, status, password_hash FROM users WHERE company_id = $1`,
		got.User.CompanyID).Scan(&users, &role, &status, &hash); err != nil {
		t.Fatalf("read created user: %v", err)
	}
	if users != 1 || role != "admin" || status != "active" {
		t.Fatalf("users=%d role=%q status=%q, want one active admin", users, role, status)
	}
	if hash == "" || hash == "correct-horse" || !auth.CheckPassword(hash, "correct-horse") {
		t.Fatalf("stored password_hash must be a bcrypt hash of the chosen password")
	}

	// The registered credentials work through the normal login.
	login := postLogin(t, h, email, "correct-horse")
	if login.Code != http.StatusOK {
		t.Fatalf("login after register = %d (%s)", login.Code, login.Body.String())
	}
}

func TestRegister_DuplicateEmailIs409AndCreatesNothing(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)
	existing := testutil.Company(t, pool, "RegDupOwner-"+uuid.NewString()[:6])
	email := uniqueEmail("dup")
	seedLoginUser(t, pool, existing, email, "irrelevant-pw", "active")

	cases := map[string]string{
		"same case":         email,
		"upper case":        strings.ToUpper(email),
		"padded mixed case": "  " + strings.ToUpper(email[:3]) + email[3:] + " ",
	}
	for name, attempt := range cases {
		t.Run(name, func(t *testing.T) {
			company := registerCompany(t, pool)
			rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, attempt, "correct-horse"))
			if rec.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d (%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "conflict" {
				t.Fatalf("error code = %q, want conflict", code)
			}
			assertErrorEnvelope(t, rec)
			assertNoHashLeak(t, rec.Body.String())
			// Atomicity (AUTH-6): the company insert is rolled back with the user insert.
			if n := countCompanies(t, pool, company); n != 0 {
				t.Fatalf("company %q survived a failed register (%d rows)", company, n)
			}
			if n := countUsersByEmail(t, pool, email); n != 1 {
				t.Fatalf("users with email = %d, want only the pre-existing one", n)
			}
		})
	}
}

func TestRegister_ConcurrentSameEmailCreatesExactlyOneTenant(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)
	email := uniqueEmail("race")
	const n = 6

	companies := make([]string, n)
	for i := range companies {
		companies[i] = registerCompany(t, pool)
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// httptest recorders and requests are built per goroutine; the
			// assertions run on the test goroutine after Wait.
			codes[i] = postRaw(t, h, registerPath,
				`{"company_name":"`+companies[i]+`","first_name":"Ana","email":"`+email+`","password":"correct-horse"}`).Code
		}()
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != n-1 {
		t.Fatalf("codes = %v, want one 201 and %d 409", codes, n-1)
	}
	total := 0
	for _, c := range companies {
		total += countCompanies(t, pool, c)
	}
	if total != 1 {
		t.Fatalf("%d companies exist for %d racing registrations, want exactly 1", total, n)
	}
}

func TestRegister_PasswordPolicyIsEnforcedInBytes(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)

	reject := map[string]string{
		"7 bytes":                      strings.Repeat("a", 7),
		"empty":                        "",
		"73 bytes":                     strings.Repeat("a", 73),
		"multibyte 74 bytes, 37 chars": strings.Repeat("é", 37),
	}
	for name, pw := range reject {
		t.Run("rejects "+name, func(t *testing.T) {
			company := registerCompany(t, pool)
			email := uniqueEmail("pw")
			rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, email, pw))
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
				t.Fatalf("got %d %s, want 400 validation_failed", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"password"`) {
				t.Fatalf("expected fields.password in %s", rec.Body.String())
			}
			assertNoHashLeak(t, rec.Body.String())
			assertErrorEnvelope(t, rec)
			if countCompanies(t, pool, company) != 0 || countUsersByEmail(t, pool, email) != 0 {
				t.Fatal("a rejected password must create nothing")
			}
		})
	}

	accept := map[string]string{
		"exactly 8 bytes":               strings.Repeat("a", 8),
		"exactly 72 bytes":              strings.Repeat("a", 72),
		"multibyte exactly 72 bytes":    strings.Repeat("é", 36),
		"multibyte 8 bytes, 4 chars":    strings.Repeat("é", 4),
		"three-byte runes, 24 chars/72": strings.Repeat("€", 24),
	}
	for name, pw := range accept {
		t.Run("accepts "+name, func(t *testing.T) {
			company := registerCompany(t, pool)
			email := uniqueEmail("pwok")
			rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, email, pw))
			if rec.Code != http.StatusCreated {
				t.Fatalf("got %d %s, want 201", rec.Code, rec.Body.String())
			}
			if login := postLogin(t, h, email, pw); login.Code != http.StatusOK {
				t.Fatalf("login with the registered password = %d", login.Code)
			}
		})
	}
}

func TestRegister_EmailIsTrimmedAndLowercasedOnStorage(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)
	company := registerCompany(t, pool)
	suffix := uuid.NewString()[:8]

	rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, "  Ana."+suffix+"@Corp.COM ", "correct-horse"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var stored string
	if err := pool.QueryRow(context.Background(),
		`SELECT u.email FROM users u JOIN companies c ON c.id = u.company_id WHERE c.name = $1`, company).Scan(&stored); err != nil {
		t.Fatalf("read stored email: %v", err)
	}
	if want := "ana." + suffix + "@corp.com"; stored != want {
		t.Fatalf("stored email = %q, want %q", stored, want)
	}
	if !strings.Contains(rec.Body.String(), `"email":"ana.`+suffix+`@corp.com"`) {
		t.Fatalf("response must carry the normalized email: %s", rec.Body.String())
	}
}

func TestRegister_BodyIsStrict(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)
	company := registerCompany(t, pool)
	email := uniqueEmail("body")
	valid := `"company_name":"` + company + `","first_name":"Ana","email":"` + email + `","password":"correct-horse"`

	cases := map[string]string{
		"unknown field company_id": `{` + valid + `,"company_id":"` + uuid.NewString() + `"}`,
		"unknown field role":       `{` + valid + `,"role":"admin"}`,
		"unknown field status":     `{` + valid + `,"status":"active"}`,
		"malformed json":           `{"company_name":`,
		"not json":                 `company_name=x`,
		"trailing value":           `{` + valid + `}{}`,
		"over 4 KiB":               `{` + valid + `,"last_name":"` + strings.Repeat("x", 5000) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postRaw(t, h, registerPath, body)
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "bad_request" {
				t.Fatalf("got %d %s, want 400 bad_request", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec)
			if countCompanies(t, pool, company) != 0 || countUsersByEmail(t, pool, email) != 0 {
				t.Fatal("a rejected body must create nothing")
			}
		})
	}
}

func TestRegister_FieldValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h := registerAPI(t, pool)

	cases := []struct {
		name   string
		mutate func(m map[string]string)
		field  string
	}{
		{"company_name empty", func(m map[string]string) { m["company_name"] = "" }, "company_name"},
		{"company_name blank", func(m map[string]string) { m["company_name"] = "   " }, "company_name"},
		{"company_name 201 runes", func(m map[string]string) { m["company_name"] = strings.Repeat("ñ", 201) }, "company_name"},
		{"first_name empty", func(m map[string]string) { m["first_name"] = "" }, "first_name"},
		{"first_name 101 runes", func(m map[string]string) { m["first_name"] = strings.Repeat("a", 101) }, "first_name"},
		{"last_name 101 runes", func(m map[string]string) { m["last_name"] = strings.Repeat("a", 101) }, "last_name"},
		{"email empty", func(m map[string]string) { m["email"] = "" }, "email"},
		{"email without @", func(m map[string]string) { m["email"] = "ana.corp.test" }, "email"},
		{"email two @", func(m map[string]string) { m["email"] = "a@b@corp.test" }, "email"},
		{"email with space", func(m map[string]string) { m["email"] = "a na@corp.test" }, "email"},
		{"email over 254", func(m map[string]string) { m["email"] = strings.Repeat("a", 250) + "@c.test" }, "email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			company := registerCompany(t, pool)
			body := registerBody(company, uniqueEmail("fv"), "correct-horse")
			tc.mutate(body)
			rec := testutil.Do(t, h, http.MethodPost, registerPath, "", body)
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
				t.Fatalf("got %d %s, want 400 validation_failed", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Fatalf("expected fields.%s in %s", tc.field, rec.Body.String())
			}
			assertNoHashLeak(t, rec.Body.String())
			var n int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM companies WHERE name = $1`, company).Scan(&n); err != nil || n != 0 {
				t.Fatalf("rejected input created a company (n=%d err=%v)", n, err)
			}
		})
	}

	t.Run("company_name of exactly 200 runes and an empty last_name are accepted", func(t *testing.T) {
		company := strings.Repeat("ñ", 200)
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE name = $1`, company) })
		body := registerBody(company, uniqueEmail("fv200"), "correct-horse")
		delete(body, "last_name")
		rec := testutil.Do(t, h, http.MethodPost, registerPath, "", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("got %d %s, want 201", rec.Code, rec.Body.String())
		}
	})

	t.Run("company_name is trimmed", func(t *testing.T) {
		company := registerCompany(t, pool)
		rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody("  "+company+"  ", uniqueEmail("trim"), "correct-horse"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("got %d %s, want 201", rec.Code, rec.Body.String())
		}
		if n := countCompanies(t, pool, company); n != 1 {
			t.Fatalf("trimmed company name not stored (n=%d)", n)
		}
	})
}

// Register is a public endpoint: it needs no token and sits behind the same
// per-client limiter as login (AUTH-5).
func TestRegister_SharesTheAuthLimiterBucketWithLogin(t *testing.T) {
	pool := testutil.Pool(t)

	t.Run("failed logins use up the budget register draws from", func(t *testing.T) {
		h, _, _ := limitedAPI(t, pool, 3, 3)
		for i := 0; i < 3; i++ {
			if rec := postLogin(t, h, uniqueEmail("nobody"), "wrong-password"); rec.Code != http.StatusUnauthorized {
				t.Fatalf("login %d = %d, want 401", i+1, rec.Code)
			}
		}
		company := registerCompany(t, pool)
		email := uniqueEmail("limited")
		rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, email, "correct-horse"))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("register after an exhausted shared bucket = %d, want 429 (%s)", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" || testutil.ErrCode(t, rec) != "rate_limited" {
			t.Fatalf("429 must carry Retry-After and rate_limited: %v %s", rec.Header(), rec.Body.String())
		}
		if countCompanies(t, pool, company) != 0 || countUsersByEmail(t, pool, email) != 0 {
			t.Fatal("a throttled register must create nothing")
		}
	})

	t.Run("register attempts use up the budget login draws from", func(t *testing.T) {
		h, _, _ := limitedAPI(t, pool, 3, 3)
		// Every register attempt is charged, whether it succeeds (201), fails
		// validation (400) or conflicts (409): none is refunded.
		company := registerCompany(t, pool)
		email := uniqueEmail("budget")
		if rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, email, "correct-horse")); rec.Code != http.StatusCreated {
			t.Fatalf("register 1 = %d (%s)", rec.Code, rec.Body.String())
		}
		if rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(registerCompany(t, pool), email, "correct-horse")); rec.Code != http.StatusConflict {
			t.Fatalf("register 2 = %d, want 409", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody("", email, "correct-horse")); rec.Code != http.StatusBadRequest {
			t.Fatalf("register 3 = %d, want 400", rec.Code)
		}
		if rec := postLogin(t, h, email, "correct-horse"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("login after three register attempts = %d, want 429 (shared bucket)", rec.Code)
		}
	})

	t.Run("a successful register is not refunded and the budget refills over time", func(t *testing.T) {
		h, clk, _ := limitedAPI(t, pool, 60, 2) // 1 token per second, burst 2
		for i := 0; i < 2; i++ {
			rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(registerCompany(t, pool), uniqueEmail("flood"), "correct-horse"))
			if rec.Code != http.StatusCreated {
				t.Fatalf("register %d = %d (%s)", i+1, rec.Code, rec.Body.String())
			}
		}
		company := registerCompany(t, pool)
		rec := testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, uniqueEmail("flood"), "correct-horse"))
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("third register = %d Retry-After=%q, want 429 and 1", rec.Code, rec.Header().Get("Retry-After"))
		}
		clk.Advance(1100 * time.Millisecond)
		rec = testutil.Do(t, h, http.MethodPost, registerPath, "", registerBody(company, uniqueEmail("flood"), "correct-horse"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("register after refill = %d (%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("another client address keeps its own budget", func(t *testing.T) {
		h, _, _ := limitedAPI(t, pool, 1, 1)
		first := registerBody(registerCompany(t, pool), uniqueEmail("a"), "correct-horse")
		if rec := registerFrom(h, "198.51.100.1:1000", first); rec.Code != http.StatusCreated {
			t.Fatalf("first client = %d", rec.Code)
		}
		second := registerBody(registerCompany(t, pool), uniqueEmail("b"), "correct-horse")
		if rec := registerFrom(h, "198.51.100.1:1001", second); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("same client second try = %d, want 429", rec.Code)
		}
		if rec := registerFrom(h, "198.51.100.2:1000", second); rec.Code != http.StatusCreated {
			t.Fatalf("other client = %d, want 201", rec.Code)
		}
	})
}

// registerFrom posts a register request from the given peer address.
func registerFrom(h http.Handler, remoteAddr string, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, registerPath, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
