package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type departmentRow struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
}

// TestDepartments_CrossTenantLeak is the canonical leak test (design P5.3):
// positions/branches/roles/employees copy this file with the table name and
// payload field changed.
func TestDepartments_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "CrossTenantLeakA")
	b := testutil.Company(t, pool, "CrossTenantLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/departments", ta, map[string]string{"name": "Ventas"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[departmentRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	depA := rows[0]

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+depA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/departments/"+depA.ID, tb, map[string]string{"name": "Hacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+depA.ID, ta, nil)
		var got departmentRow
		if err := decodeJSON(getRec, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Name != "Ventas" {
			t.Fatalf("expected name to remain Ventas, got %q", got.Name)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/departments/"+depA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+depA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected department to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/departments", tb, map[string]string{"name": "Compras"})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[departmentRow](t, testutil.Do(t, h, http.MethodGet, "/api/departments", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != depA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.Name == "Ventas" {
				found = true
			}
			if r.Name == "Compras" {
				t.Fatalf("company A's list leaked company B's department")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ventas")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/departments", ta, map[string]string{
			"name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/departments", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "EmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/departments", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[departmentRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestDepartments_DeleteRestrictedWhileReferenced pins design P6.1: DELETE
// must return 409 while an employee's TEXT `department` column still
// matches the department's name, and must leave both rows unchanged.
func TestDepartments_DeleteRestrictedWhileReferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RestrictDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": "Ventas"})
	dep := testutil.DecodeRows[departmentRow](t, createRec)[0]

	seedEmployee(t, pool, c.CompanyID, "Ana", "Diaz", "Ventas")

	rec := testutil.Do(t, h, http.MethodDelete, "/api/departments/"+dep.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 while an employee references the department, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+dep.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the department to survive the blocked delete, got %d", getRec.Code)
	}
}

// TestDepartments_DeleteSucceedsWhenUnreferenced pins the complementary
// scenario: no employee references the department, so DELETE succeeds.
func TestDepartments_DeleteSucceedsWhenUnreferenced(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "DeleteOkCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": "Finanzas"})
	dep := testutil.DecodeRows[departmentRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodDelete, "/api/departments/"+dep.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+dep.ID, tok, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected the department to be gone, got %d", getRec.Code)
	}
}

// TestDepartments_RenamePropagatesToEmployees pins design P6.1's other half:
// a rename must update every employee row's `department` TEXT column that
// matched the old name, atomically, and leave unrelated employees alone.
func TestDepartments_RenamePropagatesToEmployees(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RenamePropagateCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": "Ventas"})
	dep := testutil.DecodeRows[departmentRow](t, createRec)[0]

	e1 := seedEmployee(t, pool, c.CompanyID, "Ana", "Diaz", "Ventas")
	e2 := seedEmployee(t, pool, c.CompanyID, "Luis", "Gomez", "Ventas")
	e3 := seedEmployee(t, pool, c.CompanyID, "Rosa", "Perez", "Compras")

	rec := testutil.Do(t, h, http.MethodPatch, "/api/departments/"+dep.ID, tok, map[string]string{"name": "Comercial"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	renamed := testutil.DecodeRows[departmentRow](t, rec)[0]
	if renamed.Name != "Comercial" {
		t.Fatalf("expected renamed department name Comercial, got %q", renamed.Name)
	}

	if got := employeeDepartment(t, pool, e1); got != "Comercial" {
		t.Fatalf("expected e1.department = Comercial, got %q", got)
	}
	if got := employeeDepartment(t, pool, e2); got != "Comercial" {
		t.Fatalf("expected e2.department = Comercial, got %q", got)
	}
	if got := employeeDepartment(t, pool, e3); got != "Compras" {
		t.Fatalf("expected e3.department to remain Compras, got %q", got)
	}
}

// TestDepartments_RejectsEmptyName pins the spec's "Reject empty name"
// scenario (phase1-spec, "Lookup tables" requirement): POST and PATCH with
// an empty or whitespace-only name must return 400 validation_failed with a
// fields.name detail, and must not create or mutate any row. Runtime
// behavior already worked (verify report CRITICAL-1); this is the missing
// coverage, and the canonical pattern positions/branches/roles copy
// verbatim in slices 1b/1c.
func TestDepartments_RejectsEmptyName(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "RejectEmptyNameCo")
	tok := c.Token(t, signer)

	t.Run("create with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("create with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	// Confirm neither rejected create actually inserted a row for this tenant.
	rows := testutil.DecodeRows[departmentRow](t, testutil.Do(t, h, http.MethodGet, "/api/departments", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no department to be created by a rejected empty-name request, got %d", len(rows))
	}

	// Seed a real department to exercise the PATCH side of the scenario.
	createRec := testutil.Do(t, h, http.MethodPost, "/api/departments", tok, map[string]string{"name": "Ventas"})
	dep := testutil.DecodeRows[departmentRow](t, createRec)[0]

	t.Run("update with empty name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/departments/"+dep.ID, tok, map[string]string{"name": ""})
		assertRejectedEmptyName(t, rec)
	})

	t.Run("update with whitespace-only name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/departments/"+dep.ID, tok, map[string]string{"name": "   "})
		assertRejectedEmptyName(t, rec)
	})

	// Confirm the department survives both rejected PATCH attempts unchanged.
	getRec := testutil.Do(t, h, http.MethodGet, "/api/departments/"+dep.ID, tok, nil)
	var got departmentRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Ventas" {
		t.Fatalf("expected department name to remain Ventas after rejected renames, got %q", got.Name)
	}
}

// assertRejectedEmptyName asserts the shared shape of a rejected empty/
// whitespace-only name request: 400, error.code=validation_failed, and a
// fields.name detail (A2's per-field validation envelope).
func assertRejectedEmptyName(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
	if msg := errField(t, rec, "name"); msg == "" {
		t.Fatalf("expected error.fields.name detail in body, got none (body=%s)", rec.Body.String())
	}
}

// errField extracts error.fields[field] (A2's per-field validation detail)
// for assertions on validation_failed responses.
func errField(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := decodeJSON(rec, &body); err != nil {
		t.Fatalf("decode error body (body=%s): %v", rec.Body.String(), err)
	}
	return body.Error.Fields[field]
}

// decodeJSON decodes a recorded response body into v.
func decodeJSON(rec *httptest.ResponseRecorder, v any) error {
	return json.Unmarshal(rec.Body.Bytes(), v)
}

// seedEmployee inserts a minimal employee row directly (bypassing the HTTP
// layer, which is out of scope for slice 1a) so RESTRICT/rename tests can
// assert against employees.department, a plain TEXT column with no FK to
// department (design P6).
func seedEmployee(t *testing.T, pool *pgxpool.Pool, companyID, firstName, lastName, department string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO employees (id, company_id, first_name, last_name, department)
		 VALUES ($1, $2, $3, $4, $5)`,
		id, companyID, firstName, lastName, department,
	)
	if err != nil {
		t.Fatalf("seed employee: %v", err)
	}
	return id
}

// employeeDepartment reads back an employee's department column for
// rename-propagation assertions.
func employeeDepartment(t *testing.T, pool *pgxpool.Pool, employeeID string) string {
	t.Helper()
	var dept *string
	err := pool.QueryRow(context.Background(),
		`SELECT department FROM employees WHERE id = $1`, employeeID,
	).Scan(&dept)
	if err != nil {
		t.Fatalf("read employee department: %v", err)
	}
	if dept == nil {
		return ""
	}
	return *dept
}
