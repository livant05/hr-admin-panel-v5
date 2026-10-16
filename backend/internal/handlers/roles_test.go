package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type roleRow struct {
	ID          string          `json:"id"`
	CompanyID   string          `json:"company_id"`
	Name        string          `json:"name"`
	Permissions json.RawMessage `json:"permissions"`
}

// TestRoles_CrossTenantLeak mirrors TestDepartments_CrossTenantLeak (design
// P5.3) with the table name and payload field changed. Roles has no
// RESTRICT/rename-propagation relationship to employees (spec's
// "Applicability note"), so this leak test is the only per-table integrity
// pin roles needs beyond plain CRUD.
func TestRoles_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "RoleLeakA")
	b := testutil.Company(t, pool, "RoleLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/roles", ta, map[string]any{"name": "Supervisor", "permissions": map[string]any{}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[roleRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	roleA := rows[0]

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+roleA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+roleA.ID, tb, map[string]any{"name": "Hacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+roleA.ID, ta, nil)
		var got roleRow
		if err := decodeJSON(getRec, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Name != "Supervisor" {
			t.Fatalf("expected name to remain Supervisor, got %q", got.Name)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+roleA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+roleA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected role to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tb, map[string]any{"name": "Auditor", "permissions": map[string]any{}})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodGet, "/api/roles", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != roleA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.Name == "Supervisor" {
				found = true
			}
			if r.Name == "Auditor" {
				t.Fatalf("company A's list leaked company B's role")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Supervisor")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/roles", ta, map[string]any{
			"name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/roles", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "RoleEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/roles", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[roleRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// users.role holds the role NAME and permissions are looked up by it, so a
// rename must carry the company's users along: otherwise they silently lose
// every module (the lookup fails closed). Other companies are untouched.
func TestRoles_RenameCarriesTheUsersAlong(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleRenameCo")
	other := testutil.Company(t, pool, "RoleRenameOtherCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Supervisor", "permissions": map[string]any{"payroll": true}})
	role := testutil.DecodeRows[roleRow](t, createRec)[0]
	staff := c.TokenAs(t, pool, signer, "Supervisor")
	other.TokenAs(t, pool, signer, "Supervisor")
	if rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history", staff, nil); rec.Code != http.StatusOK {
		t.Fatalf("before rename: payroll got %d, want 200", rec.Code)
	}

	rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+role.ID, tok, map[string]any{"name": "Jefe de Planilla"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history", staff, nil); rec.Code != http.StatusOK {
		t.Fatalf("after rename: payroll got %d, want 200 (the user must keep the role's permissions)", rec.Code)
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "Jefe de Planilla"); n != 1 {
		t.Fatalf("users renamed in the company = %d, want 1", n)
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "Supervisor"); n != 0 {
		t.Fatalf("users left on the old name = %d, want 0", n)
	}
	if n := countUsersWithRole(t, pool, other.CompanyID, "Supervisor"); n != 1 {
		t.Fatalf("other company's Supervisor users = %d, want 1 (untouched)", n)
	}

	t.Run("a permissions-only PATCH leaves the users alone (unchanged)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+role.ID, tok, map[string]any{"permissions": map[string]any{"payroll": true, "vacations": true}})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if n := countUsersWithRole(t, pool, c.CompanyID, "Jefe de Planilla"); n != 1 {
			t.Fatalf("users on the role = %d, want 1", n)
		}
	})
}

// A role some user still holds cannot be deleted: those users would lose every
// module without notice. 409 with the count, like a posting with applications.
func TestRoles_DeleteOfARoleInUseIs409(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleDeleteInUseCo")
	tok := c.Token(t, signer)

	used := testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Cajero", "permissions": map[string]any{}}))[0]
	c.TokenAs(t, pool, signer, "Cajero")
	c.TokenAs(t, pool, signer, "Cajero")

	rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+used.ID, tok, nil)
	if rec.Code != http.StatusConflict || testutil.ErrCode(t, rec) != "conflict" {
		t.Fatalf("got %d (%s), want 409 conflict", rec.Code, rec.Body.String())
	}
	if msg := errMessage(t, rec); msg != "2 usuario(s) tienen este rol" {
		t.Fatalf("message = %q", msg)
	}
	if get := testutil.Do(t, h, http.MethodGet, "/api/roles/"+used.ID, tok, nil); get.Code != http.StatusOK {
		t.Fatalf("role after a refused delete: GET got %d, want 200", get.Code)
	}

	unused := testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Libre", "permissions": map[string]any{}}))[0]
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+unused.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete of an unused role: got %d (%s), want 204", rec.Code, rec.Body.String())
	}
}

// "admin" is the built-in role every check bypasses on: a roles row with that
// name (in any case) would make its users administrators, so it is reserved.
func TestRoles_AdminNameIsReserved(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleReservedCo")
	tok := c.Token(t, signer)

	for _, name := range []string{"admin", "Admin", " ADMIN "} {
		rec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": name, "permissions": map[string]any{}})
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" || errField(t, rec, "name") == "" {
			t.Fatalf("create %q: got %d (%s), want 400 validation_failed with fields.name", name, rec.Code, rec.Body.String())
		}
	}
	role := testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Ventas", "permissions": map[string]any{}}))[0]
	rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+role.ID, tok, map[string]any{"name": "Admin"})
	if rec.Code != http.StatusBadRequest || errField(t, rec, "name") == "" {
		t.Fatalf("rename to Admin: got %d (%s), want 400 with fields.name", rec.Code, rec.Body.String())
	}
}

// A legacy roles row named "admin" (imported data predates the reserved name)
// must not be renamed: the rename would carry every administrator along and
// leave the company with none, past the last-admin guard.
func TestRoles_LegacyAdminRowCannotBeRenamed(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleLegacyAdminCo")
	tok := c.Token(t, signer)
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO roles (company_id, name, permissions) VALUES ($1, 'admin', '{}') RETURNING id`, c.CompanyID).Scan(&id); err != nil {
		t.Fatalf("seed legacy admin row: %v", err)
	}
	admins := countUsersWithRole(t, pool, c.CompanyID, "admin")

	rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+id, tok, map[string]any{"name": "Gerente"})
	if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" || errField(t, rec, "name") == "" {
		t.Fatalf("got %d (%s), want 400 validation_failed with fields.name", rec.Code, rec.Body.String())
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "admin"); n != admins || n == 0 {
		t.Fatalf("admins after the refused rename = %d, want %d", n, admins)
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "Gerente"); n != 0 {
		t.Fatalf("users moved to Gerente = %d, want 0", n)
	}
	t.Run("its permissions can still be edited (no name sent)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+id, tok, map[string]any{"permissions": map[string]any{"dashboard": true}})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s), want 200", rec.Code, rec.Body.String())
		}
	})
}

func countUsersWithRole(t *testing.T, pool *pgxpool.Pool, companyID, role string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE company_id = $1 AND role = $2`, companyID, role).Scan(&n); err != nil {
		t.Fatalf("count users with role: %v", err)
	}
	return n
}

// TestRoles_PermissionsRoundTripAsJSONObject pins design P2.4: roles
// carries a real JSON object through create/read/update, never a base64
// string (the pre-override sqlc default for a JSONB->[]byte mapping).
func TestRoles_PermissionsRoundTripAsJSONObject(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RolePermsRoundTripCo")
	tok := c.Token(t, signer)

	perms := map[string]any{
		"dashboard": true,
		"employees": map[string]any{"list": true, "new": false},
	}

	createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "RRHH", "permissions": perms})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", createRec.Code, createRec.Body.String())
	}
	created := testutil.DecodeRows[roleRow](t, createRec)[0]
	assertPermissionsObject(t, created.Permissions, "dashboard")

	getRec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+created.ID, tok, nil)
	var fetched roleRow
	if err := decodeJSON(getRec, &fetched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fetchedObj := assertPermissionsObject(t, fetched.Permissions, "dashboard")
	if fetchedObj["dashboard"] != true {
		t.Fatalf("expected permissions.dashboard=true, got %v", fetchedObj["dashboard"])
	}

	newPerms := map[string]any{"reports": true}
	updRec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+created.ID, tok, map[string]any{"permissions": newPerms})
	if updRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", updRec.Code, updRec.Body.String())
	}
	updated := testutil.DecodeRows[roleRow](t, updRec)[0]
	if updated.Name != "RRHH" {
		t.Fatalf("expected name to remain unchanged by a permissions-only PATCH, got %q", updated.Name)
	}
	updatedObj := assertPermissionsObject(t, updated.Permissions, "reports")
	if updatedObj["reports"] != true {
		t.Fatalf("expected permissions.reports=true after update, got %v", updatedObj["reports"])
	}
	if _, stillHasDashboard := updatedObj["dashboard"]; stillHasDashboard {
		t.Fatalf("expected the permissions object to be replaced wholesale, but dashboard survived: %v", updatedObj)
	}
}

// TestRoles_UpdateNameOnlyLeavesPermissionsUnchanged pins the frontend's
// updateRole call site (name-only PATCH) — the counterpart to
// saveRolePerms (permissions-only PATCH) already covered above. Both call
// sites must be true partial updates.
func TestRoles_UpdateNameOnlyLeavesPermissionsUnchanged(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RoleNameOnlyPatchCo")
	tok := c.Token(t, signer)

	perms := map[string]any{"dashboard": true}
	createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Original", "permissions": perms})
	created := testutil.DecodeRows[roleRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+created.ID, tok, map[string]any{"name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	updated := testutil.DecodeRows[roleRow](t, rec)[0]
	if updated.Name != "Renamed" {
		t.Fatalf("expected name=Renamed, got %q", updated.Name)
	}
	obj := assertPermissionsObject(t, updated.Permissions, "dashboard")
	if obj["dashboard"] != true {
		t.Fatalf("expected permissions.dashboard=true to survive a name-only PATCH, got %v", obj["dashboard"])
	}
}

// TestRoles_RejectsEmptyName pins the spec's "Reject empty name" scenario
// for the create path, and the update path when a name is explicitly sent.
func TestRoles_RejectsEmptyName(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RoleRejectEmptyNameCo")
	tok := c.Token(t, signer)

	t.Run("create with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "Supervisor", "permissions": map[string]any{}})
	role := testutil.DecodeRows[roleRow](t, createRec)[0]

	t.Run("update with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+role.ID, tok, map[string]any{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	getRec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+role.ID, tok, nil)
	var got roleRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Supervisor" {
		t.Fatalf("expected role name to remain Supervisor after a rejected rename, got %q", got.Name)
	}
}

// assertPermissionsObject fails the test if raw does not decode as a JSON
// object (proving it round-tripped as real JSON, not a base64 string), and
// asserts key is present. Returns the decoded object for further assertions.
func assertPermissionsObject(t *testing.T, raw json.RawMessage, key string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("expected permissions to decode as a JSON object, got %s: %v", string(raw), err)
	}
	if _, ok := obj[key]; !ok {
		t.Fatalf("expected permissions object to contain key %q, got %v", key, obj)
	}
	return obj
}

// TestRoles_WritesAreAdminOnly pins UM-5 (privilege-escalation fix): the
// permission keys live in roles rows, so a non-admin who could edit them
// could grant themselves anything. Reads stay open to every authenticated
// role; the 403 comes before the body is parsed.
func TestRoles_WritesAreAdminOnly(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RoleAdminGateCo")
	admin := c.Token(t, signer)
	for _, role := range []string{"empleado", "manager"} {
		t.Run(role, func(t *testing.T) {
			tok := c.TokenAs(t, pool, signer, role)

			seed := testutil.Do(t, h, http.MethodPost, "/api/roles", admin, map[string]any{
				"name": "Gate-" + role, "permissions": map[string]any{"dashboard": true},
			})
			if seed.Code != http.StatusCreated {
				t.Fatalf("admin seed create: got %d (%s)", seed.Code, seed.Body.String())
			}
			seeded := testutil.DecodeRows[roleRow](t, seed)[0]

			writes := []struct {
				name, method, path string
				body               any
			}{
				{"create", http.MethodPost, "/api/roles", map[string]any{"name": "Escalated"}},
				{"create with malformed body still 403", http.MethodPost, "/api/roles", "{not json"},
				{"patch permissions", http.MethodPatch, "/api/roles/" + seeded.ID, map[string]any{"permissions": map[string]any{"settings": true}}},
				{"delete", http.MethodDelete, "/api/roles/" + seeded.ID, nil},
			}
			for _, w := range writes {
				rec := testutil.Do(t, h, w.method, w.path, tok, w.body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s as %s: got %d, want 403 (%s)", w.name, role, rec.Code, rec.Body.String())
				}
				if code := testutil.ErrCode(t, rec); code != "forbidden" {
					t.Fatalf("%s as %s: error.code = %q, want forbidden", w.name, role, code)
				}
			}

			// Nothing changed: the seeded role survives with its permissions,
			// and the rejected create left no row behind.
			got := testutil.DecodeRow[roleRow](t, testutil.Do(t, h, http.MethodGet, "/api/roles/"+seeded.ID, admin, nil))
			obj := assertPermissionsObject(t, got.Permissions, "dashboard")
			if _, escalated := obj["settings"]; escalated || obj["dashboard"] != true {
				t.Fatalf("permissions changed by a rejected write: %v", obj)
			}
			for _, r := range testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodGet, "/api/roles", admin, nil)) {
				if r.Name == "Escalated" {
					t.Fatal("a rejected create must not insert a row")
				}
			}

			// Reads are unchanged for non-admins.
			if rec := testutil.Do(t, h, http.MethodGet, "/api/roles", tok, nil); rec.Code != http.StatusOK {
				t.Fatalf("list as %s: got %d, want 200", role, rec.Code)
			}
			if rec := testutil.Do(t, h, http.MethodGet, "/api/roles/"+seeded.ID, tok, nil); rec.Code != http.StatusOK {
				t.Fatalf("get as %s: got %d, want 200", role, rec.Code)
			}
		})
	}

	t.Run("admin still creates, patches and deletes", func(t *testing.T) {
		create := testutil.Do(t, h, http.MethodPost, "/api/roles", admin, map[string]any{"name": "AdminOk", "permissions": map[string]any{}})
		if create.Code != http.StatusCreated {
			t.Fatalf("admin create: got %d", create.Code)
		}
		id := testutil.DecodeRows[roleRow](t, create)[0].ID
		if rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+id, admin, map[string]any{"name": "AdminOk2"}); rec.Code != http.StatusOK {
			t.Fatalf("admin patch: got %d", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+id, admin, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("admin delete: got %d", rec.Code)
		}
	})
}

// A user write that assigns a custom role takes that role's row lock (shared)
// before touching any user, so it serializes with a rename or delete of the
// role: a role deleted or renamed meanwhile is a 400, never a user left on a
// name that no longer exists. The test holds the role row as a rename/delete
// would, waits until the user write is parked on it, then deletes or renames
// the role (the rename carries the existing users along, as the handler does).
func TestRoles_UserWritesSerializeWithARoleChange(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleRaceCo")
	tok := c.Token(t, signer)
	ctx := context.Background()
	target, _ := seedUserAs(t, pool, c, "empleado", "active")

	changes := []struct {
		name string
		sql  string // $1 role id, $2 company id; the role is named Cajero
	}{
		{"delete", `DELETE FROM roles WHERE id = $1 AND company_id = $2`},
		{"rename", `WITH r AS (UPDATE roles SET name = 'Supervisor ' || left(md5(random()::text), 8) WHERE id = $1 RETURNING name)
			UPDATE users SET role = (SELECT name FROM r) WHERE company_id = $2 AND role = 'Cajero'`},
	}
	writes := []struct {
		name   string
		method string
		path   string
		body   func() map[string]any
	}{
		{"create", http.MethodPost, "/api/users", func() map[string]any {
			return map[string]any{"email": uniqueEmail("race"), "first_name": "Ana", "role": "Cajero"}
		}},
		{"patch", http.MethodPatch, "/api/users/" + target, func() map[string]any { return map[string]any{"role": "Cajero"} }},
	}
	for _, ch := range changes {
		for _, tc := range writes {
			t.Run(ch.name+"/"+tc.name, func(t *testing.T) {
				var roleID string
				if err := pool.QueryRow(ctx, `INSERT INTO roles (company_id, name, permissions) VALUES ($1, 'Cajero', '{}') RETURNING id`, c.CompanyID).Scan(&roleID); err != nil {
					t.Fatalf("seed role: %v", err)
				}
				holder, err := pool.Begin(ctx)
				if err != nil {
					t.Fatalf("begin holder: %v", err)
				}
				t.Cleanup(func() { _ = holder.Rollback(ctx) })
				var holderPID int
				if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
					t.Fatalf("holder pid: %v", err)
				}
				if _, err := holder.Exec(ctx, `SELECT id FROM roles WHERE id = $1 FOR UPDATE`, roleID); err != nil {
					t.Fatalf("hold role row: %v", err)
				}

				payload, _ := json.Marshal(tc.body())
				req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+tok)
				rec := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); h.ServeHTTP(rec, req) }()

				waitUntilBlockedBy(t, pool, holderPID)
				if _, err := holder.Exec(ctx, ch.sql, roleID, c.CompanyID); err != nil {
					t.Fatalf("%s role: %v", ch.name, err)
				}
				if err := holder.Commit(ctx); err != nil {
					t.Fatalf("commit holder: %v", err)
				}
				<-done

				if rec.Code != http.StatusBadRequest || errField(t, rec, "role") == "" {
					t.Fatalf("got %d %s, want 400 with fields.role", rec.Code, rec.Body.String())
				}
				if n := countUsersWithRole(t, pool, c.CompanyID, "Cajero"); n != 0 {
					t.Fatalf("users left on the old role name = %d, want 0", n)
				}
			})
		}
	}
}

// Built-in names: a legacy "admin" row backs nobody (admins bypass every
// check), so it can be deleted even though every admin holds that name; an
// "empleado" row backs the default role new users get, so it can be edited
// and deleted under the usual rules but never renamed (new users would keep
// "empleado" while the existing ones moved away).
func TestRoles_BuiltInNameRows(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "RoleBuiltInCo")
	tok := c.Token(t, signer)
	ctx := context.Background()

	var adminRow string
	if err := pool.QueryRow(ctx, `INSERT INTO roles (company_id, name, permissions) VALUES ($1, 'admin', '{}') RETURNING id`, c.CompanyID).Scan(&adminRow); err != nil {
		t.Fatalf("seed admin row: %v", err)
	}
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+adminRow, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete legacy admin row: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "admin"); n == 0 {
		t.Fatal("deleting the row must not touch the admins")
	}

	// "Admin" (another case) is NOT the built-in role: the bypass compares the
	// exact name, so its users get their modules from this row like any custom
	// role, and the usual in-use rule applies.
	var capAdmin string
	if err := pool.QueryRow(ctx, `INSERT INTO roles (company_id, name, permissions) VALUES ($1, 'Admin', '{"payroll": true}') RETURNING id`, c.CompanyID).Scan(&capAdmin); err != nil {
		t.Fatalf("seed Admin row: %v", err)
	}
	c.TokenAs(t, pool, signer, "Admin")
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+capAdmin, tok, nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete of a case-variant Admin row in use: got %d %s, want 409", rec.Code, rec.Body.String())
	}

	emp := testutil.DecodeRows[roleRow](t, testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "empleado", "permissions": map[string]any{"vacations": true}}))[0]
	c.TokenAs(t, pool, signer, "empleado")
	rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+emp.ID, tok, map[string]any{"name": "Operario"})
	if rec.Code != http.StatusBadRequest || errField(t, rec, "name") == "" {
		t.Fatalf("rename empleado row: got %d %s, want 400 with fields.name", rec.Code, rec.Body.String())
	}
	if n := countUsersWithRole(t, pool, c.CompanyID, "Operario"); n != 0 {
		t.Fatalf("users moved to Operario = %d, want 0", n)
	}
	if rec := testutil.Do(t, h, http.MethodPatch, "/api/roles/"+emp.ID, tok, map[string]any{"permissions": map[string]any{"vacations": true, "payroll": true}}); rec.Code != http.StatusOK {
		t.Fatalf("permissions of the empleado row stay editable: got %d", rec.Code)
	}
}
