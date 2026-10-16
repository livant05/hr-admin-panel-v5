package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// setUserSQL runs one UPDATE against the tenant's admin user.
func setUserSQL(t *testing.T, pool *pgxpool.Pool, userID, set string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE users SET `+set+` WHERE id = $1`, userID); err != nil {
		t.Fatalf("update user (%s): %v", set, err)
	}
}

// loginAs gives the tenant's admin a known password and signs in through the
// real login route, returning the issued token.
func loginAs(t *testing.T, pool *pgxpool.Pool, h http.Handler, c testutil.Tenant) string {
	t.Helper()
	hash, err := auth.HashPassword("Sup3rSecret!pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var email string
	if err := pool.QueryRow(context.Background(),
		`UPDATE users SET password_hash = $2 WHERE id = $1 RETURNING email`, c.UserID, hash).Scan(&email); err != nil {
		t.Fatalf("set password: %v", err)
	}
	rec := testutil.Do(t, h, http.MethodPost, "/api/auth/login", "", map[string]string{"email": email, "password": "Sup3rSecret!pw"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: got %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.AccessToken == "" {
		t.Fatalf("decode login: %v (%s)", err, rec.Body.String())
	}
	return body.AccessToken
}

func assertUnauthorized(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%s: expected 401, got %d (%s)", label, rec.Code, rec.Body.String())
	}
}

func TestSessionCheck_DeactivatedUserIsRejectedOnNextRequest(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "SessDeactivated")
	tok := c.Token(t, signer)

	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("active user: got %d (%s)", rec.Code, rec.Body.String())
	}
	setUserSQL(t, pool, c.UserID, `status = 'inactive'`)
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", tok, nil)
	assertUnauthorized(t, "inactive user", rec)
	if code := testutil.ErrCode(t, rec); code != "unauthorized" {
		t.Fatalf("expected the standard 401 envelope, got code %q", code)
	}
}

func TestSessionCheck_UnknownUserIsRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "SessUnknown")
	tok, err := signer.Sign(uuid.NewString(), c.CompanyID, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", tok, nil)
	assertUnauthorized(t, "unknown user", rec)

	notUUID, _ := signer.Sign("user-1", c.CompanyID, "admin", 0)
	rec = testutil.Do(t, h, http.MethodGet, "/api/me", notUUID, nil)
	assertUnauthorized(t, "non-uuid subject", rec)
}

func TestSessionCheck_RoleAndCompanyComeFromTheDatabase(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "SessDemoted")
	other := testutil.Company(t, pool, "SessOtherTenant")
	// The token claims admin of another company; the row says otherwise.
	tok, err := signer.Sign(c.UserID, other.CompanyID, "admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/users", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin by DB row: got %d (%s)", rec.Code, rec.Body.String())
	}
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", tok, nil)
	var me struct {
		Company struct {
			ID string `json:"id"`
		} `json:"company"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Company.ID != c.CompanyID {
		t.Fatalf("company must come from the DB row (%s), got %v (%s)", c.CompanyID, err, rec.Body.String())
	}

	setUserSQL(t, pool, c.UserID, `role = 'empleado'`)
	rec = testutil.Do(t, h, http.MethodGet, "/api/users", tok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("demoted user with a stale admin token: expected 403, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSessionCheck_TokenVersionMismatchIsRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "SessVersion")
	old := loginAs(t, pool, h, c)
	bystander := testutil.Company(t, pool, "SessBystander")
	bystanderTok := bystander.Token(t, signer)

	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", old, nil); rec.Code != http.StatusOK {
		t.Fatalf("fresh login token: got %d (%s)", rec.Code, rec.Body.String())
	}
	setUserSQL(t, pool, c.UserID, `token_version = token_version + 1`)
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", old, nil)
	assertUnauthorized(t, "stale version", rec)

	fresh := loginAs(t, pool, h, c)
	claims, err := signer.Parse(fresh)
	if err != nil || claims.TokenVersion != 1 {
		t.Fatalf("login must embed the current version 1: %v, %+v", err, claims)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", fresh, nil); rec.Code != http.StatusOK {
		t.Fatalf("re-login token: got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", bystanderTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("another user must be unaffected: got %d", rec.Code)
	}
}

func TestSessionCheck_TokenWithoutVersionClaimCountsAsZero(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "SessLegacy")
	// A token minted before this change: no "tv" claim at all.
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": c.UserID, "company_id": c.CompanyID, "role": "admin",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testutil.TestSecret))
	if err != nil {
		t.Fatal(err)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", legacy, nil); rec.Code != http.StatusOK {
		t.Fatalf("legacy token at version 0: got %d (%s)", rec.Code, rec.Body.String())
	}
	setUserSQL(t, pool, c.UserID, `token_version = 1`)
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", legacy, nil)
	assertUnauthorized(t, "legacy token after a bump", rec)
}

func TestSessionCheck_RegisterIssuesVersionZeroToken(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	email := uniqueEmail("sess-reg")
	rec := testutil.Do(t, h, http.MethodPost, "/api/auth/register", "", map[string]string{
		"company_name": "SessRegisterCo", "email": email, "password": "Sup3rSecret!pw", "first_name": "Reg", "last_name": "Ister",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: got %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		User        struct {
			CompanyID string `json:"company_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, body.User.CompanyID)
	})
	claims, err := signer.Parse(body.AccessToken)
	if err != nil || claims.TokenVersion != 0 {
		t.Fatalf("register token version: %v %+v", err, claims)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", body.AccessToken, nil); rec.Code != http.StatusOK {
		t.Fatalf("register token on /api/me: got %d (%s)", rec.Code, rec.Body.String())
	}
}

func tokenVersionOf(t *testing.T, pool *pgxpool.Pool, userID string) int32 {
	t.Helper()
	var v int32
	if err := pool.QueryRow(context.Background(), `SELECT token_version FROM users WHERE id = $1`, userID).Scan(&v); err != nil {
		t.Fatalf("read token_version: %v", err)
	}
	return v
}

func TestLogout_InvalidatesEveryTokenOfTheCallerOnly(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "LogoutCo")
	device1 := loginAs(t, pool, h, c)
	device2 := loginAs(t, pool, h, c)
	bystander := testutil.Company(t, pool, "LogoutBystander")
	bystanderTok := bystander.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/auth/logout", device1, nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("logout: got %d (%q), want 204 with no body", rec.Code, rec.Body.String())
	}
	if v := tokenVersionOf(t, pool, c.UserID); v != 1 {
		t.Fatalf("token_version = %d, want 1", v)
	}
	for label, tok := range map[string]string{"the token used to log out": device1, "the same user's other device": device2} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/me", tok, nil)
		assertUnauthorized(t, label, rec)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", bystanderTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("another user must be unaffected: got %d", rec.Code)
	}
	// A replay with the dead token is a plain 401 and bumps nothing.
	rec = testutil.Do(t, h, http.MethodPost, "/api/auth/logout", device1, nil)
	assertUnauthorized(t, "logout replay", rec)
	if v := tokenVersionOf(t, pool, c.UserID); v != 1 {
		t.Fatalf("replay changed token_version to %d", v)
	}
	// Signing in again works and starts a fresh session.
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", loginAs(t, pool, h, c), nil); rec.Code != http.StatusOK {
		t.Fatalf("login after logout: got %d", rec.Code)
	}
}

func TestLogout_RequiresAuthentication(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := testutil.Server(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/auth/logout", "", nil)
	assertUnauthorized(t, "no token", rec)
}

func TestLogout_NonAdminRoleNeedsNoModulePermission(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "LogoutEmpleado")
	rec := testutil.Do(t, h, http.MethodPost, "/api/auth/logout", c.TokenAs(t, pool, signer, "ghost"), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("role without any permission row: got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUserAdmin_BumpsTokenVersionOnRoleOrStatusChange(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "BumpCo")
	admin := c.Token(t, signer)
	other := testutil.Company(t, pool, "BumpOther")
	otherTok := other.Token(t, signer)

	target, _ := seedUserAs(t, pool, c, "empleado", "active")
	targetTok, err := signer.Sign(target, c.CompanyID, "empleado", 0)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body map[string]any) {
		t.Helper()
		if rec := patchUser(t, h, admin, target, body); rec.Code != http.StatusOK {
			t.Fatalf("patch %v: got %d (%s)", body, rec.Code, rec.Body.String())
		}
	}

	patch(map[string]any{"first_name": "Renamed"})
	patch(map[string]any{"role": "empleado", "status": "active"}) // same values
	if v := tokenVersionOf(t, pool, target); v != 0 {
		t.Fatalf("a name change or a no-op role/status must not bump, version = %d", v)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", targetTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("token must survive a name change: got %d", rec.Code)
	}

	patch(map[string]any{"role": "admin"})
	if v := tokenVersionOf(t, pool, target); v != 1 {
		t.Fatalf("role change: version = %d, want 1", v)
	}
	rec := testutil.Do(t, h, http.MethodGet, "/api/me", targetTok, nil)
	assertUnauthorized(t, "token issued before the role change", rec)

	patch(map[string]any{"status": "inactive"})
	if v := tokenVersionOf(t, pool, target); v != 2 {
		t.Fatalf("status change: version = %d, want 2", v)
	}
	if v := tokenVersionOf(t, pool, c.UserID); v != 0 {
		t.Fatalf("the acting admin must be untouched, version = %d", v)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin token: got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/me", otherTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("other tenant: got %d", rec.Code)
	}
}

func TestUserAdmin_DeleteBumpsTokenVersion(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "DeleteBumpCo")
	admin := c.Token(t, signer)
	target, _ := seedUserAs(t, pool, c, "empleado", "active")

	if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+target, admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d (%s)", rec.Code, rec.Body.String())
	}
	if v := tokenVersionOf(t, pool, target); v != 1 {
		t.Fatalf("delete: version = %d, want 1", v)
	}
	// Deleting an already-inactive user writes nothing, so it bumps nothing.
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/users/"+target, admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat delete: got %d", rec.Code)
	}
	if v := tokenVersionOf(t, pool, target); v != 1 {
		t.Fatalf("repeat delete bumped: version = %d, want 1", v)
	}
}
