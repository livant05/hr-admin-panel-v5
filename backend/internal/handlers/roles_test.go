package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

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

// TestRoles_DeleteSucceedsEvenWhileNameMatchesUserRole proves roles carries
// no RESTRICT: per spec, only users.role (a separate, out-of-scope table)
// references a role's name, and the spec explicitly excludes it from this
// phase's guard. Deleting a role whose name also appears on a user's `role`
// column must still succeed — unlike departments/positions/branches.
func TestRoles_DeleteSucceedsEvenWhileNameMatchesUserRole(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RoleNoRestrictCo")
	tok := c.Token(t, signer)

	// The tenant's own admin user (created by testutil.Company) already has
	// role='admin'. Create a role literally named "admin" to match it.
	createRec := testutil.Do(t, h, http.MethodPost, "/api/roles", tok, map[string]any{"name": "admin", "permissions": map[string]any{}})
	role := testutil.DecodeRows[roleRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodDelete, "/api/roles/"+role.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 — roles has no RESTRICT relationship to employees/users, got %d (body=%s)", rec.Code, rec.Body.String())
	}
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
