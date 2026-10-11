package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

type userRow struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type createdUser struct {
	userRow
	TemporaryPassword string `json:"temporary_password"`
}

// userKeys are exactly the keys of the users DTO (design D6): no password,
// no hash, nothing else.
var userKeys = []string{"company_id", "created_at", "email", "first_name", "id", "last_name", "role", "status"}

func newUserBody(email string) map[string]any {
	return map[string]any{"email": email, "first_name": "Nueva", "last_name": "Persona", "role": "empleado"}
}

func postUser(t *testing.T, h http.Handler, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, h, http.MethodPost, "/api/users", token, body)
}

func assertKeys(t *testing.T, raw []byte, want []string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode object: %v (%s)", err, raw)
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want exactly %v", got, want)
	}
}

func TestUsers_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "UsersLeakA")
	b := testutil.Company(t, pool, "UsersLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	bEmail := uniqueEmail("leak-b")
	bUserID := seedLoginUser(t, pool, b, bEmail, "correct-horse", "active")

	t.Run("get other tenant user is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/users/"+bUserID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403), got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		assertNoHashLeak(t, rec.Body.String())
	})

	t.Run("owner reads own user", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/users/"+bUserID, tb, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("owner get: got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRow[userRow](t, rec)
		if got.ID != bUserID || got.CompanyID != b.CompanyID || got.Email != bEmail {
			t.Fatalf("unexpected row %+v", got)
		}
		assertKeys(t, rec.Body.Bytes(), userKeys)
	})

	t.Run("list returns only own company users", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/users", ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: got %d (%s)", rec.Code, rec.Body.String())
		}
		rows := testutil.DecodeRows[userRow](t, rec)
		sawOwnAdmin := false
		for _, r := range rows {
			if r.CompanyID != a.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
			if r.ID == a.UserID {
				sawOwnAdmin = true
			}
		}
		if !sawOwnAdmin {
			t.Fatalf("company A's list must contain its own admin, got %+v", rows)
		}
		if strings.Contains(rec.Body.String(), bEmail) {
			t.Fatal("company A's list body contains company B's email")
		}
	})

	t.Run("id filter cannot reach another tenant", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/users?id="+bUserID, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		if rows := testutil.DecodeRows[userRow](t, rec); len(rows) != 0 {
			t.Fatalf("filtering by another tenant's id must return [], got %+v", rows)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		if rec := testutil.Do(t, h, http.MethodGet, "/api/users", "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401", rec.Code)
		}
	})
}

func TestUsers_NonAdminIsForbiddenOnReadAndCreate(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersNonAdminCo")
	userID := seedLoginUser(t, pool, c, uniqueEmail("na"), "correct-horse", "active")

	for _, role := range []string{"empleado", "manager"} {
		tok := c.TokenAs(t, signer, role)
		calls := []struct {
			name, method, path string
			body               any
		}{
			{"list", http.MethodGet, "/api/users", nil},
			{"get", http.MethodGet, "/api/users/" + userID, nil},
			{"create", http.MethodPost, "/api/users", newUserBody(uniqueEmail("denied"))},
			{"create with unknown field is still 403, not 400", http.MethodPost, "/api/users", map[string]any{"password": "x"}},
		}
		for _, call := range calls {
			rec := testutil.Do(t, h, call.method, call.path, tok, call.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s as %s: got %d, want 403 (%s)", call.name, role, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "forbidden" {
				t.Fatalf("%s as %s: error.code = %q, want forbidden", call.name, role, code)
			}
		}
	}

	// The rejected creates must not have inserted anything.
	adminTok := c.Token(t, signer)
	for _, r := range testutil.DecodeRows[userRow](t, testutil.Do(t, h, http.MethodGet, "/api/users", adminTok, nil)) {
		if strings.HasPrefix(r.Email, "denied-") {
			t.Fatalf("a forbidden create inserted %+v", r)
		}
	}
}

func TestUsers_CreateReturnsTemporaryPasswordOnceAndUserCanLogIn(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersCreateCo")
	admin := c.Token(t, signer)

	local := "Nueva." + uuid.NewString()[:8]
	rec := postUser(t, h, admin, newUserBody("  "+local+"@Corp.TEST "))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d (%s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store (the body carries a password)", cc)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil || len(arr) != 1 {
		t.Fatalf("create must return a one-element array (A3): %v (%s)", err, rec.Body.String())
	}
	assertKeys(t, arr[0], append(append([]string{}, userKeys...), "temporary_password"))
	created := testutil.DecodeRows[createdUser](t, rec)[0]

	wantEmail := strings.ToLower(local) + "@corp.test"
	if created.Email != wantEmail {
		t.Fatalf("email = %q, want normalized %q", created.Email, wantEmail)
	}
	if created.CompanyID != c.CompanyID || created.Role != "empleado" || created.Status != "active" {
		t.Fatalf("unexpected created user %+v", created)
	}
	if len(created.TemporaryPassword) != 24 {
		t.Fatalf("temporary password = %q, want 24 characters", created.TemporaryPassword)
	}
	if err := auth.ValidatePassword(created.TemporaryPassword); err != nil {
		t.Fatalf("temporary password violates the policy: %v", err)
	}
	assertNoHashLeak(t, rec.Body.String())

	// Only the bcrypt hash is stored.
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored hash: %v", err)
	}
	if stored == created.TemporaryPassword || !auth.CheckPassword(stored, created.TemporaryPassword) {
		t.Fatal("stored password_hash must be a bcrypt hash of the temporary password, not the password itself")
	}

	// The new user logs in with the password, email case-insensitive, in company A.
	login := postLogin(t, h, strings.ToUpper(wantEmail), created.TemporaryPassword)
	if login.Code != http.StatusOK {
		t.Fatalf("new user login: got %d (%s)", login.Code, login.Body.String())
	}
	var session struct {
		User struct {
			ID        string `json:"id"`
			CompanyID string `json:"company_id"`
			Role      string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if session.User.ID != created.ID || session.User.CompanyID != c.CompanyID || session.User.Role != "empleado" {
		t.Fatalf("login session = %+v, want the created user in company A", session.User)
	}
	assertNoHashLeak(t, login.Body.String())

	t.Run("the password is not retrievable afterwards", func(t *testing.T) {
		getRec := testutil.Do(t, h, http.MethodGet, "/api/users/"+created.ID, admin, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get: got %d", getRec.Code)
		}
		assertKeys(t, getRec.Body.Bytes(), userKeys)

		listRec := testutil.Do(t, h, http.MethodGet, "/api/users", admin, nil)
		for name, body := range map[string]string{"get": getRec.Body.String(), "list": listRec.Body.String()} {
			assertNoHashLeak(t, body)
			if strings.Contains(body, created.TemporaryPassword) || strings.Contains(body, "temporary_password") {
				t.Fatalf("%s response exposes the temporary password: %s", name, body)
			}
		}
		var listed []json.RawMessage
		if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil || len(listed) < 2 {
			t.Fatalf("list should hold the admin and the new user: %v (%s)", err, listRec.Body.String())
		}
		for _, item := range listed {
			assertKeys(t, item, userKeys)
		}
	})

	t.Run("two creates never share a password", func(t *testing.T) {
		other := testutil.DecodeRows[createdUser](t, postUser(t, h, admin, newUserBody(uniqueEmail("second"))))[0]
		if other.TemporaryPassword == created.TemporaryPassword {
			t.Fatal("temporary passwords must be random per user")
		}
	})
}

func TestUsers_CreateNeverLogsThePassword(t *testing.T) {
	pool := testutil.Pool(t)
	var logs bytes.Buffer
	signer := auth.NewSigner(testutil.TestSecret, loginTTL)
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h := api.Routes(signer)
	c := testutil.Company(t, pool, "UsersNoLogCo")

	rec := postUser(t, h, c.Token(t, signer), newUserBody(uniqueEmail("nolog")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d (%s)", rec.Code, rec.Body.String())
	}
	pw := testutil.DecodeRows[createdUser](t, rec)[0].TemporaryPassword
	if pw == "" {
		t.Fatal("expected a temporary password")
	}
	if strings.Contains(logs.String(), pw) {
		t.Fatalf("the temporary password was logged: %s", logs.String())
	}
}

func TestUsers_CreateRejectsClientSuppliedCredentialsAndCompany(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "UsersUnknownA")
	b := testutil.Company(t, pool, "UsersUnknownB")
	admin := a.Token(t, signer)

	for _, field := range []struct {
		name  string
		value any
	}{
		{"password", "Sup3rSecret!"},
		{"password_hash", "$2a$10$abcdefghijklmnopqrstuv"},
		{"company_id", b.CompanyID},
		{"id", uuid.NewString()},
	} {
		t.Run(field.name, func(t *testing.T) {
			body := newUserBody(uniqueEmail("unk-" + field.name))
			body[field.name] = field.value
			rec := postUser(t, h, admin, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "bad_request" {
				t.Fatalf("error.code = %q, want bad_request", code)
			}
			assertNoHashLeak(t, rec.Body.String())
		})
	}

	for _, r := range testutil.DecodeRows[userRow](t, testutil.Do(t, h, http.MethodGet, "/api/users", admin, nil)) {
		if strings.HasPrefix(r.Email, "unk-") {
			t.Fatalf("a rejected create inserted %+v", r)
		}
	}
	if rows := testutil.DecodeRows[userRow](t, testutil.Do(t, h, http.MethodGet, "/api/users", b.Token(t, signer), nil)); len(rows) != 1 {
		t.Fatalf("company B must still hold only its admin, got %+v", rows)
	}
}

func TestUsers_CreateDuplicateEmailIsConflictInAnyCaseAndCompany(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "UsersDupA")
	b := testutil.Company(t, pool, "UsersDupB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	email := uniqueEmail("dup")
	if rec := postUser(t, h, ta, newUserBody(email)); rec.Code != http.StatusCreated {
		t.Fatalf("first create: got %d (%s)", rec.Code, rec.Body.String())
	}

	cases := []struct {
		name, token, email string
	}{
		{"same company same case", ta, email},
		{"same company other case", ta, strings.ToUpper(email)},
		{"other company other case", tb, strings.ToUpper(email)},
		{"surrounding whitespace", ta, "  " + email + " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postUser(t, h, tc.token, newUserBody(tc.email))
			if rec.Code != http.StatusConflict {
				t.Fatalf("got %d, want 409 (%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "conflict" {
				t.Fatalf("error.code = %q, want conflict", code)
			}
		})
	}
}

func TestUsers_CreateValidatesRoleEmailAndStatus(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "UsersValidA")
	b := testutil.Company(t, pool, "UsersValidB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	if rec := testutil.Do(t, h, http.MethodPost, "/api/roles", ta, map[string]any{"name": "Supervisor", "permissions": map[string]any{}}); rec.Code != http.StatusCreated {
		t.Fatalf("seed role in A: got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodPost, "/api/roles", tb, map[string]any{"name": "OnlyInB", "permissions": map[string]any{}}); rec.Code != http.StatusCreated {
		t.Fatalf("seed role in B: got %d", rec.Code)
	}

	accepted := []struct {
		name string
		mut  func(map[string]any)
		role string
	}{
		{"system role admin", func(m map[string]any) { m["role"] = "admin" }, "admin"},
		{"system role empleado", func(m map[string]any) { m["role"] = "empleado" }, "empleado"},
		{"existing custom role of the caller", func(m map[string]any) { m["role"] = "Supervisor" }, "Supervisor"},
		{"omitted role defaults to empleado", func(m map[string]any) { delete(m, "role") }, "empleado"},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			body := newUserBody(uniqueEmail("ok"))
			tc.mut(body)
			rec := postUser(t, h, ta, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
			}
			if got := testutil.DecodeRows[createdUser](t, rec)[0].Role; got != tc.role {
				t.Fatalf("role = %q, want %q", got, tc.role)
			}
		})
	}

	rejected := []struct {
		name  string
		mut   func(map[string]any)
		field string
	}{
		{"unknown role", func(m map[string]any) { m["role"] = "superuser" }, "role"},
		{"role that exists only in another company", func(m map[string]any) { m["role"] = "OnlyInB" }, "role"},
		{"role differing in case from a custom role", func(m map[string]any) { m["role"] = "supervisor" }, "role"},
		{"missing email", func(m map[string]any) { delete(m, "email") }, "email"},
		{"email without @", func(m map[string]any) { m["email"] = "not-an-email" }, "email"},
		{"email over 254 chars", func(m map[string]any) { m["email"] = strings.Repeat("a", 250) + "@x.test" }, "email"},
		{"invalid status", func(m map[string]any) { m["status"] = "banana" }, "status"},
		{"first_name over 100 runes", func(m map[string]any) { m["first_name"] = strings.Repeat("ñ", 101) }, "first_name"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			body := newUserBody(uniqueEmail("bad"))
			tc.mut(body)
			rec := postUser(t, h, ta, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			var env struct {
				Error struct {
					Code   string            `json:"code"`
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error.Code != "validation_failed" || env.Error.Fields[tc.field] == "" {
				t.Fatalf("want validation_failed with fields.%s, got %s", tc.field, rec.Body.String())
			}
		})
	}

	t.Run("inactive status is accepted and that user cannot log in", func(t *testing.T) {
		body := newUserBody(uniqueEmail("inactive"))
		body["status"] = "inactive"
		rec := postUser(t, h, ta, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		created := testutil.DecodeRows[createdUser](t, rec)[0]
		if created.Status != "inactive" {
			t.Fatalf("status = %q, want inactive", created.Status)
		}
		if login := postLogin(t, h, created.Email, created.TemporaryPassword); login.Code != http.StatusUnauthorized {
			t.Fatalf("inactive user login: got %d, want 401", login.Code)
		}
	})
}

func TestUsers_ListFiltersAndRejectsUnknownParams(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersFilterCo")
	admin := c.Token(t, signer)
	activeID := seedLoginUser(t, pool, c, uniqueEmail("act"), "correct-horse", "active")
	inactiveID := seedLoginUser(t, pool, c, uniqueEmail("ina"), "correct-horse", "inactive")

	ids := func(path string) map[string]bool {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, path, admin, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d (%s)", path, rec.Code, rec.Body.String())
		}
		out := map[string]bool{}
		for _, r := range testutil.DecodeRows[userRow](t, rec) {
			out[r.ID] = true
		}
		return out
	}

	if got := ids("/api/users?status=inactive"); len(got) != 1 || !got[inactiveID] {
		t.Fatalf("status=inactive = %v, want only %s", got, inactiveID)
	}
	if got := ids("/api/users?status=active"); !got[activeID] || got[inactiveID] || !got[c.UserID] {
		t.Fatalf("status=active = %v", got)
	}
	if got := ids("/api/users?role=admin"); len(got) != 1 || !got[c.UserID] {
		t.Fatalf("role=admin = %v, want only the admin", got)
	}
	if got := ids("/api/users?id=" + activeID); len(got) != 1 || !got[activeID] {
		t.Fatalf("id filter = %v", got)
	}
	if got := ids("/api/users?_limit=1"); len(got) != 1 {
		t.Fatalf("_limit=1 returned %d rows", len(got))
	}

	for _, bad := range []string{"/api/users?company_id=" + c.CompanyID, "/api/users?email=x", "/api/users?_order=email", "/api/users?id=not-a-uuid"} {
		rec := testutil.Do(t, h, http.MethodGet, bad, admin, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", bad, rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "validation_failed" {
			t.Fatalf("%s: error.code = %q, want validation_failed", bad, code)
		}
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/users/not-a-uuid", admin, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id: got %d, want 404", rec.Code)
	}
}

// XC-2: a raw-body substring sweep over every endpoint that touches a user.
func TestUsers_NoPasswordHashInAnyResponseBody(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersXC2Co")
	admin := c.Token(t, signer)
	email := uniqueEmail("xc2")
	userID := seedLoginUser(t, pool, c, email, "correct-horse", "active")

	bodies := map[string]string{
		"login":       postLogin(t, h, email, "correct-horse").Body.String(),
		"login 401":   postLogin(t, h, email, "wrong-password").Body.String(),
		"me":          testutil.Do(t, h, http.MethodGet, "/api/me", admin, nil).Body.String(),
		"users list":  testutil.Do(t, h, http.MethodGet, "/api/users", admin, nil).Body.String(),
		"users get":   testutil.Do(t, h, http.MethodGet, "/api/users/"+userID, admin, nil).Body.String(),
		"users 404":   testutil.Do(t, h, http.MethodGet, "/api/users/"+uuid.NewString(), admin, nil).Body.String(),
		"users 400":   postUser(t, h, admin, map[string]any{"password": "x"}).Body.String(),
		"users 409":   postUser(t, h, admin, newUserBody(email)).Body.String(),
		"users 403":   testutil.Do(t, h, http.MethodGet, "/api/users", c.TokenAs(t, signer, "empleado"), nil).Body.String(),
		"users post":  postUser(t, h, admin, newUserBody(uniqueEmail("xc2b"))).Body.String(),
		"users query": testutil.Do(t, h, http.MethodGet, "/api/users?role=empleado", admin, nil).Body.String(),
	}
	for name, body := range bodies {
		if body == "" {
			t.Fatalf("%s: empty body", name)
		}
		assertNoHashLeak(t, body)
	}
}
