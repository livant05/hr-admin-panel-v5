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
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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

	t.Run("patch other tenant user is 404 and the row is unchanged", func(t *testing.T) {
		before := snapshotUser(t, pool, bUserID)
		for _, body := range []map[string]any{
			{"role": "admin"},
			{"status": "inactive"},
			{"first_name": "Hijacked", "email": uniqueEmail("hijack")},
		} {
			rec := testutil.Do(t, h, http.MethodPatch, "/api/users/"+bUserID, ta, body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("patch %v: expected 404 (never 403), got %d (%s)", body, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "not_found" {
				t.Fatalf("error.code = %q, want not_found", code)
			}
			assertNoHashLeak(t, rec.Body.String())
		}
		if after := snapshotUser(t, pool, bUserID); after != before {
			t.Fatalf("cross-tenant patch changed the row:\nbefore %+v\nafter  %+v", before, after)
		}
	})

	t.Run("delete other tenant user is 404 and the row is unchanged", func(t *testing.T) {
		before := snapshotUser(t, pool, bUserID)
		rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+bUserID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403/204), got %d (%s)", rec.Code, rec.Body.String())
		}
		if after := snapshotUser(t, pool, bUserID); after != before {
			t.Fatalf("cross-tenant delete changed the row:\nbefore %+v\nafter  %+v", before, after)
		}
		if before.Status != "active" {
			t.Fatalf("fixture must start active, got %q", before.Status)
		}
	})

	t.Run("owner can still patch its own user", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/users/"+bUserID, tb, map[string]any{"first_name": "Renamed"})
		if rec.Code != http.StatusOK {
			t.Fatalf("owner patch: got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := snapshotUser(t, pool, bUserID).FirstName; got != "Renamed" {
			t.Fatalf("first_name = %q, want Renamed", got)
		}
	})
}

func TestUsers_NonAdminIsForbiddenOnReadAndCreate(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersNonAdminCo")
	userID := seedLoginUser(t, pool, c, uniqueEmail("na"), "correct-horse", "active")

	for _, role := range []string{"empleado", "manager"} {
		tok := c.TokenAs(t, pool, signer, role)
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
		"users 403":   testutil.Do(t, h, http.MethodGet, "/api/users", c.TokenAs(t, pool, signer, "empleado"), nil).Body.String(),
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

// userSnapshot is every column of a users row, hash included, so "unchanged"
// means byte-identical.
type userSnapshot struct {
	CompanyID, Email, Role, FirstName, LastName, Status, PasswordHash, CreatedAt string
}

func snapshotUser(t *testing.T, pool *pgxpool.Pool, id string) userSnapshot {
	t.Helper()
	var u userSnapshot
	err := pool.QueryRow(context.Background(),
		`SELECT company_id::text, coalesce(email,''), role, coalesce(first_name,''), coalesce(last_name,''),
		        status, password_hash, created_at::text
		 FROM users WHERE id = $1`, id,
	).Scan(&u.CompanyID, &u.Email, &u.Role, &u.FirstName, &u.LastName, &u.Status, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		t.Fatalf("snapshot user %s: %v", id, err)
	}
	return u
}

// seedUserAs inserts a user with an explicit role and status and a real
// password hash (so login tests can use it).
func seedUserAs(t *testing.T, pool *pgxpool.Pool, tn testutil.Tenant, role, status string) (id, email string) {
	t.Helper()
	id = seedLoginUser(t, pool, tn, uniqueEmail(role), "correct-horse", status)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role = $2 WHERE id = $1`, id, role); err != nil {
		t.Fatalf("set role: %v", err)
	}
	return id, snapshotUser(t, pool, id).Email
}

func countActiveAdmins(t *testing.T, pool *pgxpool.Pool, companyID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE company_id = $1 AND role = 'admin' AND status = 'active'`, companyID,
	).Scan(&n); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	return n
}

func patchUser(t *testing.T, h http.Handler, token, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, h, http.MethodPatch, "/api/users/"+id, token, body)
}

func TestUsers_NonAdminIsForbiddenOnPatchAndDelete(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersNonAdminWriteCo")
	userID, _ := seedUserAs(t, pool, c, "empleado", "active")
	before := snapshotUser(t, pool, userID)

	for _, role := range []string{"empleado", "manager"} {
		tok := c.TokenAs(t, pool, signer, role)
		calls := []struct {
			name, method, path string
			body               any
		}{
			{"patch role", http.MethodPatch, "/api/users/" + userID, map[string]any{"role": "admin"}},
			{"patch with unknown field is still 403, not 400", http.MethodPatch, "/api/users/" + userID, map[string]any{"password": "x"}},
			{"delete", http.MethodDelete, "/api/users/" + userID, nil},
			{"delete unknown id is still 403, not 404", http.MethodDelete, "/api/users/" + uuid.NewString(), nil},
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
	if after := snapshotUser(t, pool, userID); after != before {
		t.Fatalf("a forbidden write changed the row:\nbefore %+v\nafter  %+v", before, after)
	}
}

// UM-3: a change that would leave the company with zero active admins is
// refused with 409 and leaves the row untouched.
func TestUsers_PatchRefusesToRemoveTheLastActiveAdmin(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersLastAdminCo") // exactly one active admin: c.UserID
	tok := c.Token(t, signer)

	t.Run("demoting the only admin is 409 and unchanged", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"role": "empleado"},
			{"role": "empleado", "first_name": "Also renamed"},
			{"status": "inactive"},
			{"role": "empleado", "status": "inactive"},
		} {
			before := snapshotUser(t, pool, c.UserID)
			rec := patchUser(t, h, tok, c.UserID, body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("patch %v: got %d, want 409 (%s)", body, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "conflict" {
				t.Fatalf("error.code = %q, want conflict", code)
			}
			if after := snapshotUser(t, pool, c.UserID); after != before {
				t.Fatalf("patch %v changed the row:\nbefore %+v\nafter  %+v", body, before, after)
			}
		}
		if n := countActiveAdmins(t, pool, c.CompanyID); n != 1 {
			t.Fatalf("active admins = %d, want 1", n)
		}
	})

	t.Run("changes that keep the admin active are allowed", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"first_name": "Renamed"},
			{"role": "admin", "status": "active"},
			{"email": uniqueEmail("renamed-admin")},
		} {
			if rec := patchUser(t, h, tok, c.UserID, body); rec.Code != http.StatusOK {
				t.Fatalf("patch %v: got %d (%s)", body, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("an inactive admin does not count as a second admin", func(t *testing.T) {
		seedUserAs(t, pool, c, "admin", "inactive")
		if rec := patchUser(t, h, tok, c.UserID, map[string]any{"role": "empleado"}); rec.Code != http.StatusConflict {
			t.Fatalf("got %d, want 409: an inactive admin cannot keep the company administered", rec.Code)
		}
	})

	t.Run("a non-admin target is never blocked by the guard", func(t *testing.T) {
		id, _ := seedUserAs(t, pool, c, "empleado", "active")
		for _, body := range []map[string]any{{"status": "inactive"}, {"role": "empleado", "status": "active"}} {
			if rec := patchUser(t, h, tok, id, body); rec.Code != http.StatusOK {
				t.Fatalf("patch %v: got %d (%s)", body, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("with two active admins one can be demoted, the survivor cannot", func(t *testing.T) {
		second, _ := seedUserAs(t, pool, c, "admin", "active")
		if rec := patchUser(t, h, tok, second, map[string]any{"role": "empleado"}); rec.Code != http.StatusOK {
			t.Fatalf("demote one of two admins: got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := testutil.DecodeRows[userRow](t, patchUser(t, h, tok, second, map[string]any{"role": "admin"}))[0].Role; got != "admin" {
			t.Fatalf("re-promote role = %q, want admin", got)
		}
		if rec := patchUser(t, h, tok, c.UserID, map[string]any{"status": "inactive"}); rec.Code != http.StatusOK {
			t.Fatalf("deactivate one of two admins: got %d (%s)", rec.Code, rec.Body.String())
		}
		// The caller just deactivated itself, so its token is dead (session
		// check); the survivor acts with its own.
		survivorTok, err := signer.Sign(second, c.CompanyID, "admin", 0)
		if err != nil {
			t.Fatal(err)
		}
		if rec := patchUser(t, h, survivorTok, second, map[string]any{"status": "inactive"}); rec.Code != http.StatusConflict {
			t.Fatalf("deactivate the survivor: got %d, want 409", rec.Code)
		}
		if n := countActiveAdmins(t, pool, c.CompanyID); n != 1 {
			t.Fatalf("active admins = %d, want 1", n)
		}
	})

	t.Run("promoting a user frees the previous admin to be demoted", func(t *testing.T) {
		c2 := testutil.Company(t, pool, "UsersPromoteCo")
		tok2 := c2.Token(t, signer)
		next, _ := seedUserAs(t, pool, c2, "empleado", "active")
		if rec := patchUser(t, h, tok2, next, map[string]any{"role": "admin"}); rec.Code != http.StatusOK {
			t.Fatalf("promote: got %d", rec.Code)
		}
		if rec := patchUser(t, h, tok2, c2.UserID, map[string]any{"role": "empleado"}); rec.Code != http.StatusOK {
			t.Fatalf("demote original admin after promotion: got %d (%s)", rec.Code, rec.Body.String())
		}
	})
}

// The guard must hold under concurrency: two admins demoting each other at the
// same time must never leave the company without an active admin. A COUNT
// without row locks passes the single-request tests above and fails here.
func TestUsers_ConcurrentDemotionOfTwoAdminsKeepsOneActive(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersRaceCo")
	tok := c.Token(t, signer)
	second, _ := seedUserAs(t, pool, c, "admin", "active")
	ids := [2]string{c.UserID, second}

	bodies := []map[string]any{{"role": "empleado"}, {"status": "inactive"}}
	const rounds = 25
	guardRaced := 0
	for round := 0; round < rounds; round++ {
		if _, err := pool.Exec(context.Background(),
			`UPDATE users SET role = 'admin', status = 'active' WHERE id = ANY($1)`, ids[:]); err != nil {
			t.Fatalf("reset admins: %v", err)
		}

		// Requests are built on the test goroutine; the racing goroutines only
		// serve them and record the recorder, so no t.Fatalf / testutil.Do
		// ever runs off the test goroutine.
		reqs := make([]*http.Request, len(ids))
		recs := make([]*httptest.ResponseRecorder, len(ids))
		for i := range ids {
			// Alternate role/status demotion so both guard paths race.
			payload, err := json.Marshal(bodies[(round+i)%2])
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reqs[i] = httptest.NewRequest(http.MethodPatch, "/api/users/"+ids[i], bytes.NewReader(payload))
			reqs[i].Header.Set("Content-Type", "application/json")
			reqs[i].Header.Set("Authorization", "Bearer "+tok)
			recs[i] = httptest.NewRecorder()
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				h.ServeHTTP(recs[i], reqs[i])
			}(i)
		}
		close(start)
		wg.Wait()
		codes := make([]int, len(recs))
		for i, rec := range recs {
			codes[i] = rec.Code
		}

		if n := countActiveAdmins(t, pool, c.CompanyID); n < 1 {
			t.Fatalf("round %d: company left with %d active admins (codes %v)", round, n, codes)
		}
		ok, conflict, lost := 0, 0, 0
		for _, code := range codes {
			switch code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				conflict++
			case http.StatusForbidden, http.StatusUnauthorized:
				// The caller is itself one of the two admins: when the other
				// request's demotion commits before this request's session
				// check, the caller is correctly refused (role/status come
				// from the database on every request).
				lost++
			default:
				t.Fatalf("round %d: unexpected status %v (a deadlock or server error must not escape)", round, codes)
			}
		}
		if ok != 1 || conflict+lost != 1 {
			t.Fatalf("round %d: statuses %v, want exactly one 200 and one refusal", round, codes)
		}
		if conflict == 1 {
			guardRaced++
		}
	}
	if guardRaced == 0 {
		t.Fatalf("no round reached the last-admin guard with both requests in flight; the race is not being exercised")
	}
}

func TestUsers_PatchValidatesAndReturnsTheUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "UsersPatchA")
	b := testutil.Company(t, pool, "UsersPatchB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	if rec := testutil.Do(t, h, http.MethodPost, "/api/roles", ta, map[string]any{"name": "Supervisor", "permissions": map[string]any{}}); rec.Code != http.StatusCreated {
		t.Fatalf("seed role in A: got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodPost, "/api/roles", tb, map[string]any{"name": "OnlyInB", "permissions": map[string]any{}}); rec.Code != http.StatusCreated {
		t.Fatalf("seed role in B: got %d", rec.Code)
	}
	id, _ := seedUserAs(t, pool, a, "empleado", "active")
	otherID, otherEmail := seedUserAs(t, pool, a, "empleado", "active")

	t.Run("updates fields, normalizes email and returns an array with the DTO", func(t *testing.T) {
		newEmail := "  New." + uuid.NewString()[:6] + "@Corp.TEST "
		rec := patchUser(t, h, ta, id, map[string]any{
			"first_name": "  Luisa ", "last_name": "Gomez", "email": newEmail, "role": "Supervisor", "status": "inactive",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		assertNoHashLeak(t, rec.Body.String())
		var arr []json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil || len(arr) != 1 {
			t.Fatalf("want a one-element array (A3), got %s", rec.Body.String())
		}
		assertKeys(t, arr[0], userKeys)
		got := testutil.DecodeRows[userRow](t, rec)[0]
		want := userRow{
			ID: id, CompanyID: a.CompanyID, Email: strings.ToLower(strings.TrimSpace(newEmail)),
			Role: "Supervisor", FirstName: "Luisa", LastName: "Gomez", Status: "inactive",
		}
		want.CreatedAt = got.CreatedAt
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if s := snapshotUser(t, pool, id); s.Email != want.Email || s.Role != "Supervisor" || s.Status != "inactive" {
			t.Fatalf("not persisted: %+v", s)
		}
	})

	t.Run("omitted fields are left alone and an empty body is a no-op", func(t *testing.T) {
		before := snapshotUser(t, pool, otherID)
		if rec := patchUser(t, h, ta, otherID, map[string]any{}); rec.Code != http.StatusOK {
			t.Fatalf("empty body: got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec := patchUser(t, h, ta, otherID, map[string]any{"last_name": "Solo"}); rec.Code != http.StatusOK {
			t.Fatalf("partial body: got %d", rec.Code)
		}
		after := snapshotUser(t, pool, otherID)
		before.LastName = "Solo"
		if after != before {
			t.Fatalf("partial patch touched other columns:\nwant %+v\ngot  %+v", before, after)
		}
	})

	t.Run("an empty name clears it and the password hash is never touched", func(t *testing.T) {
		before := snapshotUser(t, pool, otherID)
		if rec := patchUser(t, h, ta, otherID, map[string]any{"first_name": ""}); rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		after := snapshotUser(t, pool, otherID)
		if after.FirstName != "" || after.PasswordHash != before.PasswordHash || after.PasswordHash == "" {
			t.Fatalf("first_name %q, hash changed=%v", after.FirstName, after.PasswordHash != before.PasswordHash)
		}
	})

	rejected := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"unknown role", map[string]any{"role": "superuser"}, "role"},
		{"role that exists only in another company", map[string]any{"role": "OnlyInB"}, "role"},
		{"role differing in case from a custom role", map[string]any{"role": "supervisor"}, "role"},
		{"empty role", map[string]any{"role": ""}, "role"},
		{"invalid status", map[string]any{"status": "banana"}, "status"},
		{"empty status", map[string]any{"status": ""}, "status"},
		{"email without @", map[string]any{"email": "not-an-email"}, "email"},
		{"empty email", map[string]any{"email": ""}, "email"},
		{"email over 254 chars", map[string]any{"email": strings.Repeat("a", 250) + "@x.test"}, "email"},
		{"last_name over 100 runes", map[string]any{"last_name": strings.Repeat("ñ", 101)}, "last_name"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			before := snapshotUser(t, pool, otherID)
			rec := patchUser(t, h, ta, otherID, tc.body)
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
			if after := snapshotUser(t, pool, otherID); after != before {
				t.Fatalf("rejected patch changed the row: %+v -> %+v", before, after)
			}
		})
	}

	t.Run("rejects fields the client must never set", func(t *testing.T) {
		before := snapshotUser(t, pool, otherID)
		for _, body := range []string{
			`{"company_id":"` + b.CompanyID + `"}`, `{"password":"hunter2hunter2"}`, `{"password_hash":"x"}`,
			`{"id":"` + uuid.NewString() + `"}`, `{"created_at":"2020-01-01T00:00:00Z"}`,
			`{"role":"empleado"} {"role":"admin"}`, `not json`, ``,
		} {
			req := httptest.NewRequest(http.MethodPatch, "/api/users/"+otherID, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+ta)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q: got %d, want 400 (%s)", body, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "bad_request" {
				t.Fatalf("body %q: error.code = %q, want bad_request", body, code)
			}
		}
		if after := snapshotUser(t, pool, otherID); after != before {
			t.Fatalf("rejected patch changed the row")
		}
	})

	t.Run("email already used is 409 in any case and company, row unchanged", func(t *testing.T) {
		bEmail := uniqueEmail("taken-b")
		seedLoginUser(t, pool, b, bEmail, "correct-horse", "active")
		before := snapshotUser(t, pool, otherID)
		idEmail := snapshotUser(t, pool, id).Email // the first subtest renamed it
		for _, taken := range []string{idEmail, strings.ToUpper(idEmail), bEmail, strings.ToUpper(bEmail)} {
			rec := patchUser(t, h, ta, otherID, map[string]any{"email": taken})
			if rec.Code != http.StatusConflict {
				t.Fatalf("email %q: got %d, want 409 (%s)", taken, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "conflict" {
				t.Fatalf("error.code = %q, want conflict", code)
			}
		}
		if after := snapshotUser(t, pool, otherID); after != before {
			t.Fatalf("conflicting patch changed the row")
		}
	})

	t.Run("re-sending a user's own email in another case is not a conflict", func(t *testing.T) {
		if rec := patchUser(t, h, ta, otherID, map[string]any{"email": strings.ToUpper(otherEmail)}); rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed and unknown ids are 404", func(t *testing.T) {
		for _, bad := range []string{"not-a-uuid", uuid.NewString()} {
			if rec := patchUser(t, h, ta, bad, map[string]any{"first_name": "x"}); rec.Code != http.StatusNotFound {
				t.Fatalf("id %q: got %d, want 404", bad, rec.Code)
			}
		}
	})
}

// UM-4: DELETE deactivates, never removes; the user can no longer log in.
func TestUsers_DeleteIsSoftIdempotentAndBlocksLogin(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UsersDeleteCo")
	tok := c.Token(t, signer)
	id, email := seedUserAs(t, pool, c, "empleado", "active")
	bystanderID, _ := seedUserAs(t, pool, c, "empleado", "active")

	if login := postLogin(t, h, email, "correct-horse"); login.Code != http.StatusOK {
		t.Fatalf("precondition: login before delete = %d", login.Code)
	}
	before := snapshotUser(t, pool, id)
	bystander := snapshotUser(t, pool, bystanderID)

	rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+id, tok, nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("delete: got %d body %q, want 204 with no body", rec.Code, rec.Body.String())
	}
	after := snapshotUser(t, pool, id)
	if after.Status != "inactive" {
		t.Fatalf("status = %q, want inactive (row must remain)", after.Status)
	}
	before.Status = "inactive"
	if after != before {
		t.Fatalf("soft delete changed more than status:\nwant %+v\ngot  %+v", before, after)
	}
	if got := snapshotUser(t, pool, bystanderID); got != bystander {
		t.Fatalf("delete touched another user: %+v", got)
	}

	if login := postLogin(t, h, email, "correct-horse"); login.Code != http.StatusUnauthorized {
		t.Fatalf("login after delete = %d, want 401", login.Code)
	}

	t.Run("deleted user is still readable as inactive", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/users/"+id, tok, nil)
		if rec.Code != http.StatusOK || testutil.DecodeRow[userRow](t, rec).Status != "inactive" {
			t.Fatalf("get after delete: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("deleting again is 204 and changes nothing", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+id, tok, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("re-delete %d: got %d, want 204", i, rec.Code)
			}
		}
		if got := snapshotUser(t, pool, id); got != after {
			t.Fatalf("re-delete changed the row: %+v", got)
		}
	})

	t.Run("malformed and unknown ids are 404", func(t *testing.T) {
		for _, bad := range []string{"not-a-uuid", uuid.NewString()} {
			if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+bad, tok, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("id %q: got %d, want 404", bad, rec.Code)
			}
		}
	})

	t.Run("the last active admin cannot be deleted", func(t *testing.T) {
		adminBefore := snapshotUser(t, pool, c.UserID)
		rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+c.UserID, tok, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("got %d, want 409 (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "conflict" {
			t.Fatalf("error.code = %q, want conflict", code)
		}
		if got := snapshotUser(t, pool, c.UserID); got != adminBefore {
			t.Fatalf("refused delete changed the row")
		}
	})

	t.Run("with a second active admin an admin can be deleted, then the survivor cannot", func(t *testing.T) {
		second, _ := seedUserAs(t, pool, c, "admin", "active")
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+second, tok, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("delete one of two admins: got %d", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+c.UserID, tok, nil); rec.Code != http.StatusConflict {
			t.Fatalf("delete the survivor: got %d, want 409", rec.Code)
		}
		if n := countActiveAdmins(t, pool, c.CompanyID); n != 1 {
			t.Fatalf("active admins = %d, want 1", n)
		}
	})
}
